package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/progress"
	"github.com/202121000995/OneSync/internal/relay"
	"github.com/202121000995/OneSync/internal/task"
)

func TestRunnersSynchronizeDirectlyAndReconnectBoundPeer(t *testing.T) {
	serverTLS, clientTLS := clientTestTLS(t)
	endpoint := availableAddress(t)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceStore := credentialStore(t)
	targetStore := credentialStore(t)
	token := testToken()
	peerID, err := auth.NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	saveCredential(t, sourceStore, "source", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, OneTime: true,
	})
	saveCredential(t, targetStore, "target", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, PeerID: peerID,
	})
	sourceFactory := testFactory(t, sourceStore, serverTLS, clientTLS)
	targetFactory := testFactory(t, targetStore, nil, clientTLS)
	sourceTask := task.Task{ID: "source", Role: task.RoleSource, SourcePath: sourceRoot}
	targetTask := task.Task{ID: "target", Role: task.RoleTarget, TargetPath: targetRoot}

	writeTestFile(t, filepath.Join(sourceRoot, "folder", "file.txt"), "first")
	runRunnerPairUntilFile(t, sourceFactory, targetFactory, sourceTask, targetTask, filepath.Join(targetRoot, "folder", "file.txt"), "first")
	assertTestFile(t, filepath.Join(targetRoot, "folder", "file.txt"), "first")

	claimed, err := sourceStore.Load("source")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !claimed.Used || claimed.PeerID != peerID {
		t.Fatalf("source credential was not bound to target: %+v", claimed)
	}

	writeTestFile(t, filepath.Join(sourceRoot, "folder", "file.txt"), "second")
	runRunnerPairUntilFile(t, sourceFactory, targetFactory, sourceTask, targetTask, filepath.Join(targetRoot, "folder", "file.txt"), "second")
	assertTestFile(t, filepath.Join(targetRoot, "folder", "file.txt"), "second")
}

func TestTargetTrustsSourceCertificateFromCredential(t *testing.T) {
	serverTLS, clientTLS := clientTestTLS(t)
	endpoint := availableAddress(t)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceStore := credentialStore(t)
	targetStore := credentialStore(t)
	token := testToken()
	peerID, err := auth.NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	saveCredential(t, sourceStore, "source", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, OneTime: true,
	})
	saveCredential(t, targetStore, "target", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, PeerID: peerID,
		CACertificatePEM: certificatePEMFromTLS(t, serverTLS),
	})
	sourceFactory := testFactory(t, sourceStore, serverTLS, clientTLS)
	targetFactory := testFactory(t, targetStore, nil, &tls.Config{})
	sourceTask := task.Task{ID: "source", Role: task.RoleSource, SourcePath: sourceRoot}
	targetTask := task.Task{ID: "target", Role: task.RoleTarget, TargetPath: targetRoot}

	writeTestFile(t, filepath.Join(sourceRoot, "file.txt"), "trusted by link")
	runRunnerPairUntilFile(t, sourceFactory, targetFactory, sourceTask, targetTask, filepath.Join(targetRoot, "file.txt"), "trusted by link")
}

func TestRunnersKeepSynchronizingUntilCanceled(t *testing.T) {
	serverTLS, clientTLS := clientTestTLS(t)
	endpoint := availableAddress(t)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceStore := credentialStore(t)
	targetStore := credentialStore(t)
	token := testToken()
	peerID, err := auth.NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	saveCredential(t, sourceStore, "source", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, OneTime: true,
	})
	saveCredential(t, targetStore, "target", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, PeerID: peerID,
	})
	sourceFactory := testFactory(t, sourceStore, serverTLS, clientTLS)
	targetFactory := testFactory(t, targetStore, nil, clientTLS)
	sourceTask := task.Task{ID: "source", Role: task.RoleSource, SourcePath: sourceRoot}
	targetTask := task.Task{ID: "target", Role: task.RoleTarget, TargetPath: targetRoot}
	sourceRunner, targetRunner := createRunnerPair(t, sourceFactory, targetFactory, sourceTask, targetTask)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- sourceRunner.Run(ctx, sourceTask.ID) }()
	time.Sleep(20 * time.Millisecond)
	go func() { results <- targetRunner.Run(ctx, targetTask.ID) }()

	targetFile := filepath.Join(targetRoot, "file.txt")
	writeTestFile(t, filepath.Join(sourceRoot, "file.txt"), "first")
	waitForTestFile(t, targetFile, "first")
	writeTestFile(t, filepath.Join(sourceRoot, "file.txt"), "second")
	waitForTestFile(t, targetFile, "second")
	cancel()
	waitRunnerCancellation(t, results)
}

func TestFactoryReportsMissingCredentialWithoutLeakingPath(t *testing.T) {
	_, clientTLS := clientTestTLS(t)
	credentialDir := filepath.Join(t.TempDir(), "credentials")
	store, err := auth.NewCredentialStore(credentialDir)
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	factory := testFactory(t, store, nil, clientTLS)

	_, err = factory.Create(context.Background(), task.Task{
		ID: "source", Role: task.RoleSource, SourcePath: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Create() error = nil, want missing credential error")
	}
	message := err.Error()
	if !strings.Contains(message, "task credential is missing") {
		t.Fatalf("Create() error = %q, want friendly missing credential message", message)
	}
	if strings.Contains(message, credentialDir) {
		t.Fatalf("Create() leaked credential path: %q", message)
	}
}

func TestRunnerReportsContinuousStates(t *testing.T) {
	serverTLS, clientTLS := clientTestTLS(t)
	endpoint := availableAddress(t)
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceStore := credentialStore(t)
	targetStore := credentialStore(t)
	token := testToken()
	peerID, err := auth.NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	saveCredential(t, sourceStore, "source", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, OneTime: true,
	})
	saveCredential(t, targetStore, "target", auth.Credential{
		SessionID: "session", Endpoint: endpoint, Token: token, PeerID: peerID,
	})
	sourceFactory := testFactory(t, sourceStore, serverTLS, clientTLS)
	targetFactory := testFactory(t, targetStore, nil, clientTLS)
	sourceTask := task.Task{ID: "source", Role: task.RoleSource, SourcePath: sourceRoot}
	targetTask := task.Task{ID: "target", Role: task.RoleTarget, TargetPath: targetRoot}
	sourceRunner, targetRunner := createRunnerPair(t, sourceFactory, targetFactory, sourceTask, targetTask)
	sourceReporter := newStateRecorder()
	targetReporter := newStateRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		results <- sourceRunner.(task.ReportingRunner).RunWithReporter(ctx, sourceTask.ID, sourceReporter)
	}()
	time.Sleep(20 * time.Millisecond)
	go func() {
		results <- targetRunner.(task.ReportingRunner).RunWithReporter(ctx, targetTask.ID, targetReporter)
	}()

	writeTestFile(t, filepath.Join(sourceRoot, "file.txt"), "content")
	waitForTestFile(t, filepath.Join(targetRoot, "file.txt"), "content")
	sourceReporter.waitFor(t, task.StateConnecting, task.StateSyncing, task.StateIdle)
	targetReporter.waitFor(t, task.StateConnecting, task.StateSyncing, task.StateIdle)
	sourceReporter.waitForProgress(t, progress.Snapshot{TotalFiles: 1, CompletedFiles: 1})
	targetReporter.waitForProgress(t, progress.Snapshot{TotalFiles: 1, CompletedFiles: 1})
	cancel()
	waitRunnerCancellation(t, results)
}

func TestRunnersSynchronizeThroughRelayAfterDirectFailure(t *testing.T) {
	serverTLS, clientTLS := clientTestTLS(t)
	broker, err := relay.NewBroker(relay.Config{
		WaitTimeout: time.Second,
		IdleTimeout: 30 * time.Second,
		MaxWaiting:  10,
		MaxActive:   10,
		MaxBytes:    16 << 20,
		AccessToken: "relay-access-token",
	})
	if err != nil {
		t.Fatalf("NewBroker() error = %v", err)
	}
	relayServer, err := relay.Listen("127.0.0.1:0", serverTLS, broker)
	if err != nil {
		t.Fatalf("relay.Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- relayServer.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = relayServer.Close()
		<-serveResult
	})

	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceStore := credentialStore(t)
	targetStore := credentialStore(t)
	token := testToken()
	peerID, err := auth.NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	credential := auth.Credential{
		SessionID:     "relay-session",
		Endpoint:      "127.0.0.1:1",
		RelayEndpoint: relayServer.Addr().String(),
		RelayToken:    "relay-access-token",
		Token:         token,
	}
	sourceCredential := credential
	sourceCredential.OneTime = true
	saveCredential(t, sourceStore, "source", sourceCredential)
	targetCredential := credential
	targetCredential.PeerID = peerID
	saveCredential(t, targetStore, "target", targetCredential)

	sourceFactory := testFactory(t, sourceStore, nil, clientTLS)
	targetFactory := testFactory(t, targetStore, nil, clientTLS)
	sourceTask := task.Task{ID: "source", Role: task.RoleSource, SourcePath: sourceRoot}
	targetTask := task.Task{ID: "target", Role: task.RoleTarget, TargetPath: targetRoot}
	writeTestFile(t, filepath.Join(sourceRoot, "relay.txt"), "through Relay")
	runRunnerPairUntilFile(t, sourceFactory, targetFactory, sourceTask, targetTask, filepath.Join(targetRoot, "relay.txt"), "through Relay")
	assertTestFile(t, filepath.Join(targetRoot, "relay.txt"), "through Relay")
}

func TestRelayWakeTriggersNextSyncCycle(t *testing.T) {
	serverTLS, clientTLS := clientTestTLS(t)
	broker, err := relay.NewBroker(relay.Config{
		WaitTimeout: time.Second,
		IdleTimeout: time.Second,
		MaxWaiting:  10,
		MaxActive:   10,
		MaxBytes:    16 << 20,
		AccessToken: "relay-access-token",
	})
	if err != nil {
		t.Fatalf("NewBroker() error = %v", err)
	}
	relayServer, err := relay.Listen("127.0.0.1:0", serverTLS, broker)
	if err != nil {
		t.Fatalf("relay.Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- relayServer.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = relayServer.Close()
		<-serveResult
	})

	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	sourceStore := credentialStore(t)
	targetStore := credentialStore(t)
	token := testToken()
	peerID, err := auth.NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	credential := auth.Credential{
		SessionID:     "relay-wake-session",
		Endpoint:      "127.0.0.1:1",
		RelayEndpoint: relayServer.Addr().String(),
		RelayToken:    "relay-access-token",
		Token:         token,
	}
	sourceCredential := credential
	sourceCredential.OneTime = true
	saveCredential(t, sourceStore, "source", sourceCredential)
	targetCredential := credential
	targetCredential.PeerID = peerID
	saveCredential(t, targetStore, "target", targetCredential)

	sourceFactory := testFactory(t, sourceStore, nil, clientTLS)
	targetFactory := testFactory(t, targetStore, nil, clientTLS)
	sourceTask := task.Task{ID: "source", Role: task.RoleSource, SourcePath: sourceRoot}
	targetTask := task.Task{ID: "target", Role: task.RoleTarget, TargetPath: targetRoot}
	sourceRunner, targetRunner := createRunnerPair(t, sourceFactory, targetFactory, sourceTask, targetTask)
	sourceReporter := newStateRecorder()
	targetReporter := newStateRecorder()
	runCtx, runCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer runCancel()
	results := make(chan error, 2)
	go func() {
		results <- sourceRunner.(task.ReportingRunner).RunWithReporter(runCtx, sourceTask.ID, sourceReporter)
	}()
	time.Sleep(20 * time.Millisecond)
	go func() {
		results <- targetRunner.(task.ReportingRunner).RunWithReporter(runCtx, targetTask.ID, targetReporter)
	}()

	targetFile := filepath.Join(targetRoot, "relay-wake.txt")
	writeTestFile(t, filepath.Join(sourceRoot, "relay-wake.txt"), "first")
	waitForTestFileOrRunnerError(t, targetFile, "first", results)
	writeTestFile(t, filepath.Join(sourceRoot, "relay-wake.txt"), "second")
	waitForTestFileOrRunnerError(t, targetFile, "second", results)
	if err := os.Remove(targetFile); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	waitForTestFileOrRunnerErrorWithLogs(t, targetFile, "second", results, sourceReporter, targetReporter)
	runCancel()
	waitRunnerCancellation(t, results)
}

func runRunnerPairUntilFile(
	t *testing.T,
	sourceFactory, targetFactory *Factory,
	sourceTask, targetTask task.Task,
	targetPath, targetContent string,
) {
	t.Helper()
	sourceRunner, targetRunner := createRunnerPair(t, sourceFactory, targetFactory, sourceTask, targetTask)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	results := make(chan error, 2)
	go func() { results <- sourceRunner.Run(ctx, sourceTask.ID) }()
	time.Sleep(20 * time.Millisecond)
	go func() { results <- targetRunner.Run(ctx, targetTask.ID) }()
	waitForTestFileOrRunnerError(t, targetPath, targetContent, results)
	time.Sleep(100 * time.Millisecond)
	cancel()
	waitRunnerCancellation(t, results)
}

func createRunnerPair(
	t *testing.T,
	sourceFactory, targetFactory *Factory,
	sourceTask, targetTask task.Task,
) (task.Runner, task.Runner) {
	t.Helper()
	sourceRunner, err := sourceFactory.Create(context.Background(), sourceTask)
	if err != nil {
		t.Fatalf("create source runner: %v", err)
	}
	targetRunner, err := targetFactory.Create(context.Background(), targetTask)
	if err != nil {
		t.Fatalf("create target runner: %v", err)
	}
	return sourceRunner, targetRunner
}

func waitRunnerCancellation(t *testing.T, results chan error) {
	t.Helper()
	for range 2 {
		if err := <-results; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("runner error = %v", err)
		}
	}
}

func clientTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "OneSync Test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair() error = %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certificatePEM)
	return &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS13,
		}, &tls.Config{
			RootCAs:    roots,
			MinVersion: tls.VersionTLS13,
		}
}

func certificatePEMFromTLS(t *testing.T, config *tls.Config) string {
	t.Helper()
	if config == nil || len(config.Certificates) == 0 || len(config.Certificates[0].Certificate) == 0 {
		t.Fatal("TLS config has no certificate")
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: config.Certificates[0].Certificate[0]}))
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func credentialStore(t *testing.T) *auth.CredentialStore {
	t.Helper()
	store, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	return store
}

func testFactory(t *testing.T, store *auth.CredentialStore, serverTLS, clientTLS *tls.Config) *Factory {
	t.Helper()
	factory, err := NewFactory(Config{
		Credentials:  store,
		ServerTLS:    serverTLS,
		ClientTLS:    clientTLS,
		SyncInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	return factory
}

func saveCredential(t *testing.T, store *auth.CredentialStore, taskID string, credential auth.Credential) {
	t.Helper()
	if err := store.Save(taskID, credential); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func testToken() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func assertTestFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
}

func waitForTestFile(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertTestFile(t, path, want)
}

func waitForTestFileOrRunnerError(t *testing.T, path, want string, results <-chan error) {
	t.Helper()
	waitForTestFileOrRunnerErrorWithLogs(t, path, want, results)
}

func waitForTestFileOrRunnerErrorWithLogs(t *testing.T, path, want string, results <-chan error, recorders ...*stateRecorder) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-results:
			t.Fatalf("runner exited before file synchronized: %v", err)
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err == nil && string(data) == want {
				return
			}
		case <-deadline.C:
			for index, recorder := range recorders {
				t.Logf("recorder %d logs:\n%s", index, recorder.drainLogs())
			}
			assertTestFile(t, path, want)
		}
	}
}

type stateRecorder struct {
	updates  chan string
	progress chan progress.Snapshot
	logs     chan string
}

func newStateRecorder() *stateRecorder {
	return &stateRecorder{
		updates:  make(chan string, 100),
		progress: make(chan progress.Snapshot, 100),
		logs:     make(chan string, 500),
	}
}

func (r *stateRecorder) SetState(ctx context.Context, state, _ string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.updates <- state:
		return nil
	}
}

func (r *stateRecorder) SetProgress(ctx context.Context, snapshot progress.Snapshot) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.progress <- snapshot:
		return nil
	}
}

func (r *stateRecorder) AddLog(ctx context.Context, level, message string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.logs <- level + ": " + message:
		return nil
	default:
		return nil
	}
}

func (r *stateRecorder) drainLogs() string {
	var builder strings.Builder
	for {
		select {
		case line := <-r.logs:
			builder.WriteString(line)
			builder.WriteByte('\n')
		default:
			return builder.String()
		}
	}
}

func (r *stateRecorder) waitFor(t *testing.T, states ...string) {
	t.Helper()
	next := 0
	deadline := time.After(5 * time.Second)
	for next < len(states) {
		select {
		case state := <-r.updates:
			if state == states[next] {
				next++
			}
		case <-deadline:
			t.Fatalf("state %q was not reported", states[next])
		}
	}
}

func (r *stateRecorder) waitForProgress(t *testing.T, want progress.Snapshot) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case got := <-r.progress:
			if got.TotalFiles == want.TotalFiles && got.CompletedFiles == want.CompletedFiles && got.CurrentPath == want.CurrentPath {
				return
			}
		case <-deadline:
			t.Fatalf("progress %+v was not reported", want)
		}
	}
}
