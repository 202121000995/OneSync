package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/sync"
	"github.com/202121000995/OneSync/internal/task"
)

// Config provides shared dependencies for task runners.
type Config struct {
	Credentials *auth.CredentialStore
	ServerTLS   *tls.Config
	ClientTLS   *tls.Config
	MaxPayload  uint32
}

// Factory creates authenticated synchronization runners.
type Factory struct {
	credentials *auth.CredentialStore
	serverTLS   *tls.Config
	clientTLS   *tls.Config
	maxPayload  uint32
}

// NewFactory validates and copies runtime TLS configuration.
func NewFactory(config Config) (*Factory, error) {
	if config.Credentials == nil {
		return nil, errors.New("credential store is required")
	}
	if config.ClientTLS == nil || config.ClientTLS.InsecureSkipVerify {
		return nil, errors.New("verified client TLS configuration is required")
	}
	if config.MaxPayload == 0 {
		config.MaxPayload = network.DefaultMaxPayload
	}
	if _, err := network.NewCodec(config.MaxPayload); err != nil {
		return nil, err
	}
	clientTLS := config.ClientTLS.Clone()
	clientTLS.MinVersion = tls.VersionTLS13
	var serverTLS *tls.Config
	if config.ServerTLS != nil {
		if len(config.ServerTLS.Certificates) == 0 {
			return nil, errors.New("server TLS certificate is required")
		}
		serverTLS = config.ServerTLS.Clone()
		serverTLS.MinVersion = tls.VersionTLS13
	}
	return &Factory{
		credentials: config.Credentials,
		serverTLS:   serverTLS,
		clientTLS:   clientTLS,
		maxPayload:  config.MaxPayload,
	}, nil
}

// Create loads private task material and creates one fresh runner.
func (f *Factory) Create(_ context.Context, definition task.Task) (task.Runner, error) {
	credential, err := f.credentials.Load(definition.ID)
	if err != nil {
		return nil, fmt.Errorf("load task credential: %w", err)
	}
	if definition.Role == task.RoleSource && f.serverTLS == nil && credential.RelayEndpoint == "" {
		return nil, errors.New("source task requires a TLS certificate or Relay endpoint")
	}
	if definition.Role == task.RoleTarget && credential.PeerID == "" {
		return nil, errors.New("target credential is missing its peer identity")
	}
	return &runner{
		factory:    f,
		task:       definition,
		credential: credential,
	}, nil
}

type runner struct {
	factory    *Factory
	task       task.Task
	credential auth.Credential
}

func (r *runner) Run(ctx context.Context, taskID string) error {
	if taskID != r.task.ID {
		return errors.New("runner task ID does not match")
	}
	token, err := base64.RawURLEncoding.DecodeString(r.credential.Token)
	if err != nil || len(token) != 32 {
		return errors.New("task credential token is invalid")
	}
	if r.task.Role == task.RoleSource {
		expectedPeerID := ""
		if r.credential.Used {
			expectedPeerID = r.credential.PeerID
		}
		connection, err := connectSource(
			ctx,
			r.credential,
			token,
			[]byte(r.credential.Token),
			expectedPeerID,
			r.factory.serverTLS,
			r.factory.clientTLS,
			r.factory.maxPayload,
		)
		if err != nil {
			return err
		}
		defer connection.session.Close()
		if _, err := r.factory.credentials.Claim(r.task.ID, r.credential.Token, connection.peerID); err != nil {
			return fmt.Errorf("claim target identity: %w", err)
		}
		engine, err := sync.DefaultSourceEngine(r.task.SourcePath, connection.session)
		if err != nil {
			return err
		}
		return engine.Run(ctx, taskID)
	}

	session, err := connectTarget(
		ctx,
		r.credential,
		token,
		[]byte(r.credential.Token),
		r.credential.PeerID,
		r.factory.clientTLS,
		r.factory.maxPayload,
	)
	if err != nil {
		return err
	}
	defer session.Close()
	engine, err := sync.DefaultTargetEngine(r.task.TargetPath, session)
	if err != nil {
		return err
	}
	return engine.Run(ctx, taskID)
}
