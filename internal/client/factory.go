package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/filewatch"
	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/sync"
	"github.com/202121000995/OneSync/internal/task"
)

const DefaultSyncInterval = 30 * time.Second

// Config provides shared dependencies for task runners.
type Config struct {
	Credentials  *auth.CredentialStore
	ServerTLS    *tls.Config
	ClientTLS    *tls.Config
	MaxPayload   uint32
	SyncInterval time.Duration
}

// Factory creates authenticated synchronization runners.
type Factory struct {
	credentials  *auth.CredentialStore
	serverTLS    *tls.Config
	clientTLS    *tls.Config
	maxPayload   uint32
	syncInterval time.Duration
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
	if config.SyncInterval == 0 {
		config.SyncInterval = DefaultSyncInterval
	}
	if config.SyncInterval < 0 {
		return nil, errors.New("sync interval cannot be negative")
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
		credentials:  config.Credentials,
		serverTLS:    serverTLS,
		clientTLS:    clientTLS,
		maxPayload:   config.MaxPayload,
		syncInterval: config.SyncInterval,
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
		factory: f,
		task:    definition,
	}, nil
}

type runner struct {
	factory *Factory
	task    task.Task
}

func (r *runner) Run(ctx context.Context, taskID string) error {
	return r.run(ctx, taskID, nil)
}

// RunWithReporter runs continuously and reports connection, sync, and waiting phases.
func (r *runner) RunWithReporter(ctx context.Context, taskID string, reporter task.StateReporter) error {
	if reporter == nil {
		return errors.New("state reporter is required")
	}
	return r.run(ctx, taskID, reporter)
}

func (r *runner) run(ctx context.Context, taskID string, reporter task.StateReporter) error {
	if taskID != r.task.ID {
		return errors.New("runner task ID does not match")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reportState(ctx, reporter, task.StateConnecting); err != nil {
			return err
		}
		if err := r.runCycle(ctx, taskID, reporter); err != nil {
			if !errors.Is(err, errConnectionUnavailable) {
				return err
			}
		}
		if err := reportState(ctx, reporter, task.StateIdle); err != nil {
			return err
		}
		if err := filewatch.WaitPeriodic(ctx, r.factory.syncInterval); err != nil {
			return err
		}
	}
}

func (r *runner) runCycle(ctx context.Context, taskID string, reporter task.StateReporter) error {
	credential, err := r.factory.credentials.Load(r.task.ID)
	if err != nil {
		return fmt.Errorf("load task credential: %w", err)
	}
	token, err := base64.RawURLEncoding.DecodeString(credential.Token)
	if err != nil || len(token) != 32 {
		return errors.New("task credential token is invalid")
	}
	if r.task.Role == task.RoleSource {
		expectedPeerID := ""
		if credential.Used {
			expectedPeerID = credential.PeerID
		}
		connection, err := connectSource(
			ctx,
			credential,
			token,
			[]byte(credential.Token),
			expectedPeerID,
			r.factory.serverTLS,
			r.factory.clientTLS,
			r.factory.maxPayload,
		)
		if err != nil {
			return err
		}
		defer connection.session.Close()
		if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
			return err
		}
		if _, err := r.factory.credentials.Claim(r.task.ID, credential.Token, connection.peerID); err != nil {
			return fmt.Errorf("claim target identity: %w", err)
		}
		engine, err := sync.DefaultSourceEngine(r.task.SourcePath, connection.session, progressReporter(reporter))
		if err != nil {
			return err
		}
		return engine.Run(ctx, taskID)
	}

	session, err := connectTarget(
		ctx,
		credential,
		token,
		[]byte(credential.Token),
		credential.PeerID,
		r.factory.clientTLS,
		r.factory.maxPayload,
	)
	if err != nil {
		return err
	}
	defer session.Close()
	if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
		return err
	}
	engine, err := sync.DefaultTargetEngine(r.task.TargetPath, session, progressReporter(reporter))
	if err != nil {
		return err
	}
	return engine.Run(ctx, taskID)
}

func reportState(ctx context.Context, reporter task.StateReporter, state string) error {
	if reporter == nil {
		return nil
	}
	return reporter.SetState(ctx, state, "")
}

func progressReporter(reporter task.StateReporter) sync.ProgressReporter {
	progressReporter, _ := reporter.(sync.ProgressReporter)
	return progressReporter
}
