package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/filewatch"
	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/scanner"
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
	credential, err := f.loadCredential(definition.ID)
	if err != nil {
		return nil, err
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
			addLog(ctx, reporter, "warning", fmt.Sprintf("本轮连接暂不可用，%s 后重试：%v", r.factory.syncInterval, err))
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
	credential, err := r.factory.loadCredential(r.task.ID)
	if err != nil {
		return err
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
		addLog(ctx, reporter, "info", fmt.Sprintf("源端开始等待连接：直连地址=%s，Relay=%s，已绑定对端=%t", credential.Endpoint, emptyDash(credential.RelayEndpoint), expectedPeerID != ""))
		clientTLS, err := clientTLSForCredential(r.factory.clientTLS, credential)
		if err != nil {
			return err
		}
		connection, err := connectSource(
			ctx,
			credential,
			token,
			[]byte(credential.Token),
			expectedPeerID,
			r.factory.serverTLS,
			clientTLS,
			r.factory.maxPayload,
		)
		if err != nil {
			addLog(ctx, reporter, "warning", fmt.Sprintf("源端连接失败：%v", err))
			return err
		}
		session := meteredSession{base: connection.session, reporter: trafficReporter(reporter)}
		defer session.Close()
		setDevice(ctx, reporter, task.DeviceStats{
			Connected:     1,
			Total:         1,
			PeerID:        connection.peerID,
			Endpoint:      credential.Endpoint,
			RelayEndpoint: credential.RelayEndpoint,
			Connection:    connectionLabel(connection.relayed),
			TLS:           "TLS 1.3",
			ClientVersion: "OneSync",
		})
		addLog(ctx, reporter, "info", fmt.Sprintf("连接成功：方式=%s，对端=%s", connectionLabel(connection.relayed), safePeerID(connection.peerID)))
		if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
			return err
		}
		if _, err := r.factory.credentials.Claim(r.task.ID, credential.Token, connection.peerID); err != nil {
			return fmt.Errorf("claim target identity: %w", err)
		}
		engine, err := sync.DefaultSourceEngineWithOptions(r.task.SourcePath, session, scanner.Options{
			IgnoreRules: r.task.IgnoreRules,
		}, progressReporter(reporter))
		if err != nil {
			return err
		}
		return engine.Run(ctx, taskID)
	}

	clientTLS, err := clientTLSForCredential(r.factory.clientTLS, credential)
	if err != nil {
		return err
	}
	addLog(ctx, reporter, "info", fmt.Sprintf("目标端开始连接源端：直连地址=%s，Relay=%s，对端身份=%s", credential.Endpoint, emptyDash(credential.RelayEndpoint), safePeerID(credential.PeerID)))
	connection, err := connectTarget(
		ctx,
		credential,
		token,
		[]byte(credential.Token),
		credential.PeerID,
		clientTLS,
		r.factory.maxPayload,
	)
	if err != nil {
		addLog(ctx, reporter, "warning", fmt.Sprintf("目标端连接失败：%v", err))
		return err
	}
	session := meteredSession{base: connection.session, reporter: trafficReporter(reporter)}
	defer session.Close()
	setDevice(ctx, reporter, task.DeviceStats{
		Connected:     1,
		Total:         1,
		PeerID:        credential.PeerID,
		Endpoint:      credential.Endpoint,
		RelayEndpoint: credential.RelayEndpoint,
		Connection:    connectionLabel(connection.relayed),
		TLS:           "TLS 1.3",
		ClientVersion: "OneSync",
	})
	addLog(ctx, reporter, "info", fmt.Sprintf("连接成功：方式=%s，源端=%s", connectionLabel(connection.relayed), credential.Endpoint))
	if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
		return err
	}
	engine, err := sync.DefaultTargetEngineWithOptions(r.task.TargetPath, session, scanner.Options{
		IgnoreRules: r.task.IgnoreRules,
	}, progressReporter(reporter))
	if err != nil {
		return err
	}
	return engine.Run(ctx, taskID)
}

func clientTLSForCredential(base *tls.Config, credential auth.Credential) (*tls.Config, error) {
	config := base.Clone()
	if credential.CACertificatePEM == "" {
		return config, nil
	}
	var roots *x509.CertPool
	if config.RootCAs != nil {
		roots = config.RootCAs.Clone()
	} else {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	if !roots.AppendCertsFromPEM([]byte(credential.CACertificatePEM)) {
		return nil, errors.New("task link CA certificate is invalid")
	}
	config.RootCAs = roots
	config.MinVersion = tls.VersionTLS13
	return config, nil
}

func (f *Factory) loadCredential(taskID string) (auth.Credential, error) {
	credential, err := f.credentials.Load(taskID)
	if err == nil {
		return credential, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return auth.Credential{}, errors.New("task credential is missing; for a source task, generate a synchronization link before starting; for a target task, join the link again")
	}
	return auth.Credential{}, fmt.Errorf("load task credential: %w", err)
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

func setDevice(ctx context.Context, reporter task.StateReporter, details task.DeviceStats) {
	deviceReporter, ok := reporter.(task.DeviceReporter)
	if !ok {
		return
	}
	_ = deviceReporter.SetDevice(ctx, details)
}

func connectionLabel(relayed bool) string {
	if relayed {
		return "Relay"
	}
	return "直连"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func safePeerID(peerID string) string {
	if len(peerID) <= 12 {
		return emptyDash(peerID)
	}
	return peerID[:6] + "..." + peerID[len(peerID)-6:]
}

func trafficReporter(reporter task.StateReporter) task.TrafficReporter {
	trafficReporter, _ := reporter.(task.TrafficReporter)
	return trafficReporter
}

func addLog(ctx context.Context, reporter task.StateReporter, level, message string) {
	logReporter, _ := reporter.(task.LogReporter)
	if logReporter != nil {
		_ = logReporter.AddLog(ctx, level, message)
	}
}

type meteredSession struct {
	base     network.Session
	reporter task.TrafficReporter
}

func (s meteredSession) Send(ctx context.Context, message network.Message) error {
	err := s.base.Send(ctx, message)
	if err == nil && s.reporter != nil {
		_ = s.reporter.AddTraffic(ctx, 0, messageWireSize(message))
	}
	return err
}

func (s meteredSession) Receive(ctx context.Context) (network.Message, error) {
	message, err := s.base.Receive(ctx)
	if err == nil && s.reporter != nil {
		_ = s.reporter.AddTraffic(ctx, messageWireSize(message), 0)
	}
	return message, err
}

func (s meteredSession) Close() error {
	return s.base.Close()
}

func messageWireSize(message network.Message) uint64 {
	return uint64(1 + 1 + 8 + 4 + len(message.Payload))
}
