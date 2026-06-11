package client

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/filewatch"
	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/scanner"
	"github.com/202121000995/OneSync/internal/sync"
	"github.com/202121000995/OneSync/internal/task"
	"github.com/202121000995/OneSync/internal/transfer"
)

const DefaultSyncInterval = 10 * time.Second

const maxConnectionRetryDelay = 30 * time.Second
const maxConnectedIdleInterval = 2 * time.Second

// Config provides shared dependencies for task runners.
type Config struct {
	Credentials  *auth.CredentialStore
	ServerTLS    *tls.Config
	ClientTLS    *tls.Config
	MaxPayload   uint32
	SyncInterval time.Duration
	// TransferPipelineChunks controls how many file chunks may be in flight before waiting for acknowledgements.
	TransferPipelineChunks int
}

// Factory creates authenticated synchronization runners.
type Factory struct {
	credentials            *auth.CredentialStore
	serverTLS              *tls.Config
	clientTLS              *tls.Config
	maxPayload             uint32
	syncInterval           time.Duration
	transferPipelineChunks int
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
	if config.TransferPipelineChunks < 0 {
		return nil, errors.New("transfer pipeline chunks cannot be negative")
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
		credentials:            config.Credentials,
		serverTLS:              serverTLS,
		clientTLS:              clientTLS,
		maxPayload:             config.MaxPayload,
		syncInterval:           config.SyncInterval,
		transferPipelineChunks: config.TransferPipelineChunks,
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
	retryFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reportState(ctx, reporter, task.StateConnecting); err != nil {
			return err
		}
		if err := r.runCycle(ctx, taskID, reporter); err != nil {
			if !retryableRunError(err) {
				return err
			}
			retryFailures++
			delay := connectionRetryDelay(retryFailures, r.factory.syncInterval)
			addLog(ctx, reporter, "warning", fmt.Sprintf("本轮连接或网络暂不可用，%s 后自动重试：%v", delay, err))
			if err := filewatch.WaitPeriodic(ctx, delay); err != nil {
				return err
			}
			continue
		}
		retryFailures = 0
		if err := reportState(ctx, reporter, task.StateIdle); err != nil {
			return err
		}
		changed, err := filewatch.WaitForChangeOrPeriodic(ctx, r.localRoot(), r.task.IgnoreRules, r.factory.syncInterval)
		if err != nil {
			return err
		}
		if changed {
			addLog(ctx, reporter, "info", fmt.Sprintf("检测到同步目录变化，并已等待 %s 确认文件写入稳定，提前开始下一轮同步", filewatch.DescribeChangeWait()))
		}
	}
}

func retryableRunError(err error) bool {
	if errors.Is(err, errConnectionUnavailable) {
		return true
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "authentication failed") || strings.Contains(text, "认证失败") {
		return false
	}
	for _, marker := range []string{
		"timeout",
		"connection reset",
		"connection refused",
		"broken pipe",
		"remote host closed",
		"read frame header: eof",
		"unexpected eof",
		"use of closed network connection",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func connectionRetryDelay(failures int, syncInterval time.Duration) time.Duration {
	if failures < 1 {
		failures = 1
	}
	delay := time.Duration(1<<min(failures-1, 4)) * 5 * time.Second
	if syncInterval > 0 && delay > syncInterval {
		delay = syncInterval
	}
	if delay > maxConnectionRetryDelay {
		delay = maxConnectionRetryDelay
	}
	if syncInterval >= 5*time.Second && delay < 5*time.Second {
		delay = 5 * time.Second
	}
	return delay
}

func (r *runner) localRoot() string {
	if r.task.Role == task.RoleSource {
		return r.task.SourcePath
	}
	return r.task.TargetPath
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
		session := sessionDigest(credential.SessionID)
		addLog(ctx, reporter, "info", fmt.Sprintf("源端开始等待连接：会话=%s，直连地址=%s，Relay=%s，已绑定对端=%t", session, credential.Endpoint, emptyDash(credential.RelayEndpoint), expectedPeerID != ""))
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
		transferSession := meteredSession{base: connection.session, reporter: trafficReporter(reporter)}
		defer transferSession.Close()
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
		addLog(ctx, reporter, "info", fmt.Sprintf("连接成功：会话=%s，方式=%s，对端=%s", session, connectionLabel(connection.relayed), safePeerID(connection.peerID)))
		if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
			return err
		}
		if _, err := r.factory.credentials.Claim(r.task.ID, credential.Token, connection.peerID); err != nil {
			return fmt.Errorf("claim target identity: %w", err)
		}
		engine, err := sync.DefaultSourceEngineWithTransferOptions(r.task.SourcePath, transferSession, scanner.Options{
			IgnoreRules: r.task.IgnoreRules,
		}, transfer.Sender{
			PipelineChunks: r.factory.transferPipelineChunks,
		}, progressReporter(reporter))
		if err != nil {
			return err
		}
		var wake wakeController
		if _, ok := connection.session.(wakeController); ok {
			wake = transferSession
		}
		return r.runConnectedCycles(ctx, taskID, reporter, engine, wake)
	}

	clientTLS, err := clientTLSForCredential(r.factory.clientTLS, credential)
	if err != nil {
		return err
	}
	session := sessionDigest(credential.SessionID)
	addLog(ctx, reporter, "info", fmt.Sprintf("目标端开始连接源端：会话=%s，直连地址=%s，Relay=%s，对端身份=%s", session, credential.Endpoint, emptyDash(credential.RelayEndpoint), safePeerID(credential.PeerID)))
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
	transferSession := meteredSession{base: connection.session, reporter: trafficReporter(reporter)}
	defer transferSession.Close()
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
	addLog(ctx, reporter, "info", fmt.Sprintf("连接成功：会话=%s，方式=%s，源端=%s", session, connectionLabel(connection.relayed), credential.Endpoint))
	if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
		return err
	}
	engine, err := sync.DefaultTargetEngineWithOptions(r.task.TargetPath, transferSession, scanner.Options{
		IgnoreRules: r.task.IgnoreRules,
	}, progressReporter(reporter))
	if err != nil {
		return err
	}
	var wake wakeController
	if _, ok := connection.session.(wakeController); ok {
		wake = transferSession
	}
	return r.runConnectedCycles(ctx, taskID, reporter, engine, wake)
}

type wakeController interface {
	SendWake(context.Context) error
	WaitWake(context.Context) error
}

func (r *runner) runConnectedCycles(ctx context.Context, taskID string, reporter task.StateReporter, engine *sync.Engine, wake wakeController) error {
	idleInterval := connectedIdleInterval(r.factory.syncInterval)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := reportState(ctx, reporter, task.StateSyncing); err != nil {
			return err
		}
		if err := engine.Run(ctx, taskID); err != nil {
			return err
		}
		if err := reportState(ctx, reporter, task.StateIdle); err != nil {
			return err
		}
		changed, woke, err := r.waitForConnectedChange(ctx, wake, idleInterval)
		if err != nil {
			return err
		}
		if changed {
			if wake != nil {
				_ = wake.SendWake(ctx)
			}
			addLog(ctx, reporter, "info", fmt.Sprintf("检测到同步目录变化，并已等待 %s 确认文件写入稳定，使用现有连接开始下一轮同步", filewatch.DescribeChangeWait()))
		}
		if woke {
			addLog(ctx, reporter, "info", "收到对端变化通知，使用现有连接开始下一轮同步")
		}
	}
}

func (r *runner) waitForConnectedChange(ctx context.Context, wake wakeController, idleInterval time.Duration) (changed bool, woke bool, err error) {
	if wake == nil {
		changed, err = filewatch.WaitForChangeOrPeriodic(ctx, r.localRoot(), r.task.IgnoreRules, idleInterval)
		return changed, false, err
	}
	waitContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type fileResult struct {
		changed bool
		err     error
	}
	fileResults := make(chan fileResult, 1)
	wakeResults := make(chan error, 1)
	go func() {
		changed, err := filewatch.WaitForChangeOrPeriodic(waitContext, r.localRoot(), r.task.IgnoreRules, idleInterval)
		fileResults <- fileResult{changed: changed, err: err}
	}()
	go func() {
		wakeResults <- wake.WaitWake(waitContext)
	}()
	select {
	case result := <-fileResults:
		cancel()
		return result.changed, false, result.err
	case err := <-wakeResults:
		cancel()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return false, false, ctx.Err()
			}
			return false, false, err
		}
		return false, true, nil
	case <-ctx.Done():
		cancel()
		return false, false, ctx.Err()
	}
}

func connectedIdleInterval(syncInterval time.Duration) time.Duration {
	if syncInterval <= 0 {
		return maxConnectedIdleInterval
	}
	if syncInterval < maxConnectedIdleInterval {
		return syncInterval
	}
	return maxConnectedIdleInterval
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

func sessionDigest(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(digest[:6])
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

func (s meteredSession) SendWake(ctx context.Context) error {
	wake, ok := s.base.(wakeController)
	if !ok {
		return nil
	}
	return wake.SendWake(ctx)
}

func (s meteredSession) WaitWake(ctx context.Context) error {
	wake, ok := s.base.(wakeController)
	if !ok {
		return errConnectionUnavailable
	}
	return wake.WaitWake(ctx)
}

func messageWireSize(message network.Message) uint64 {
	return uint64(1 + 1 + 8 + 4 + len(message.Payload))
}
