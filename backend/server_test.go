package backend

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/certutil"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/task"
	"github.com/202121000995/OneSync/internal/webauth"
)

func TestServerServesManagementPage(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("OneSync")) {
		t.Fatal("management page does not contain OneSync")
	}
}

func TestServerRejectsNonLocalHostAndOrigin(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
	request.Host = "evil.example"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-local host status = %d, want 403", response.Code)
	}

	request = jsonRequest(http.MethodPost, "http://127.0.0.1/api/tasks", map[string]any{})
	request.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", response.Code)
	}
}

func TestTaskAPI(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	server := newServerWithManager(t, manager)
	create := jsonRequest(http.MethodPost, "http://127.0.0.1/api/tasks", map[string]any{
		"id": "photos", "role": "source", "source_path": "/photos",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/tasks", nil)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, list)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("photos")) {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}

	update := jsonRequest(http.MethodPatch, "http://127.0.0.1/api/tasks/photos", map[string]any{
		"ignore_rules": []string{"*.tmp", "cache/"},
	})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, update)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := manager.tasks["photos"].IgnoreRules; len(got) != 2 || got[0] != "*.tmp" || got[1] != "cache/" {
		t.Fatalf("IgnoreRules = %+v", got)
	}

	device := jsonRequest(http.MethodPatch, "http://127.0.0.1/api/tasks/photos/device", map[string]any{
		"alias": "办公室电脑", "trusted": true, "disabled": true,
	})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, device)
	if response.Code != http.StatusOK {
		t.Fatalf("device update status = %d, body = %s", response.Code, response.Body.String())
	}
	if got := manager.tasks["photos"]; got.Devices.Alias != "办公室电脑" || !got.DeviceTrusted || !got.DeviceDisabled {
		t.Fatalf("device state = %+v trusted=%t disabled=%t", got.Devices, got.DeviceTrusted, got.DeviceDisabled)
	}
}

func TestManagementAuthSetupAndLogin(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	authStore, err := webauth.NewStore(filepath.Join(t.TempDir(), "web-auth.json"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		WebAuth: authStore,
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/tasks", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	setup := jsonRequest(http.MethodPost, "http://127.0.0.1/api/auth/setup", map[string]any{
		"username": "admin", "password": "password123",
	})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, setup)
	if response.Code != http.StatusCreated {
		t.Fatalf("setup status = %d, body = %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]

	request = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/tasks", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, body = %s", response.Code, response.Body.String())
	}

	login := jsonRequest(http.MethodPost, "http://127.0.0.1/api/auth/login", map[string]any{
		"username": "admin", "password": "wrong-password",
	})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, login)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d", response.Code)
	}
}

func TestLinkIssueAndJoinStoresCredentialSeparately(t *testing.T) {
	manager := &fakeManager{tasks: map[string]task.Task{
		"source": {ID: "source", Role: task.RoleSource, SourcePath: "/private/source"},
	}}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	sourceCertificatePEM := testCertificatePEM(t)
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		DirectTLSConfigured:  true,
		DirectTLSCertificate: sourceCertificatePEM,
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}

	issue := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links", map[string]any{
		"task_id": "source", "endpoint": "192.168.1.10:7443",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, issue)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, body = %s", response.Code, response.Body.String())
	}
	var issued struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("/private/source")) {
		t.Fatal("link response exposed the local source path")
	}
	parsed, err := server.links.Parse(issued.Link)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.CACertificatePEM != sourceCertificatePEM {
		t.Fatal("link did not include source certificate")
	}

	join := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links/join", map[string]any{
		"task_id": "target", "target_path": "/private/target", "link": issued.Link,
	})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, join)
	if response.Code != http.StatusCreated {
		t.Fatalf("join status = %d, body = %s", response.Code, response.Body.String())
	}
	credential, err := credentials.Load("target")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if credential.Token == "" || credential.Endpoint != "192.168.1.10:7443" || credential.PeerID == "" {
		t.Fatalf("credential = %+v", credential)
	}
	if credential.CACertificatePEM != sourceCertificatePEM {
		t.Fatal("joined credential did not store source certificate")
	}
	if manager.tasks["target"].PeerAddress != "192.168.1.10:7443" {
		t.Fatalf("joined task = %+v", manager.tasks["target"])
	}
}

func TestLinkIssueBundlesRelayCertificate(t *testing.T) {
	manager := &fakeManager{tasks: map[string]task.Task{
		"source": {ID: "source", Role: task.RoleSource, SourcePath: "/private/source"},
	}}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	sourceCertificatePEM := testCertificatePEM(t)
	relayCertificatePEM := testRelayCertificatePEM(t)
	fetcher := &fakeCertificateFetcher{certificate: relayCertificatePEM}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		DirectTLSConfigured:  true,
		DirectTLSCertificate: sourceCertificatePEM,
		CertificateFetcher:   fetcher,
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}

	issue := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links", map[string]any{
		"task_id": "source", "endpoint": "192.168.1.10:7443", "relay_endpoint": "relay.example:7443", "relay_token": "relay-secret",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, issue)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, body = %s", response.Code, response.Body.String())
	}
	var issued struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	parsed, err := server.links.Parse(issued.Link)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if fetcher.endpoint != "relay.example:7443" {
		t.Fatalf("fetcher endpoint = %q", fetcher.endpoint)
	}
	if parsed.RelayToken != "relay-secret" {
		t.Fatalf("RelayToken = %q, want relay-secret", parsed.RelayToken)
	}
	if !bytes.Contains([]byte(parsed.CACertificatePEM), []byte(sourceCertificatePEM)) ||
		!bytes.Contains([]byte(parsed.CACertificatePEM), []byte(relayCertificatePEM)) {
		t.Fatal("link did not include both source and Relay certificates")
	}
	credential, err := credentials.Load("source")
	if err != nil {
		t.Fatalf("Load(source) error = %v", err)
	}
	if credential.CACertificatePEM != parsed.CACertificatePEM {
		t.Fatal("source credential did not store bundled certificates")
	}
	if credential.RelayToken != "relay-secret" {
		t.Fatalf("source credential RelayToken = %q, want relay-secret", credential.RelayToken)
	}
}

func TestLinkIssueRejectsUnusableSourceWithoutTLSOrRelay(t *testing.T) {
	manager := &fakeManager{tasks: map[string]task.Task{
		"source": {ID: "source", Role: task.RoleSource, SourcePath: "/private/source"},
	}}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	server, err := NewServer(manager, auth.NewLinkService(), credentials)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	request := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links", map[string]any{
		"task_id": "source", "endpoint": "192.168.1.10:7443",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!bytes.Contains(response.Body.Bytes(), []byte("source direct connection is not ready; restart OneSync or enter a Relay endpoint")) {
		t.Fatalf("issue status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := credentials.Load("source"); err == nil {
		t.Fatal("rejected source link stored credentials")
	}
}

func TestDeleteTaskRemovesCredential(t *testing.T) {
	manager := &fakeManager{tasks: map[string]task.Task{
		"source": {ID: "source", Role: task.RoleSource, SourcePath: "/private/source"},
	}}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		DirectTLSConfigured:  true,
		DirectTLSCertificate: testCertificatePEM(t),
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}

	issue := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links", map[string]any{
		"task_id": "source", "endpoint": "192.168.1.10:7443",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, issue)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, err := credentials.Load("source"); err != nil {
		t.Fatalf("Load(source) error = %v", err)
	}

	request := jsonRequest(http.MethodDelete, "http://127.0.0.1/api/tasks/source", map[string]any{})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", response.Code, response.Body.String())
	}
	if _, exists := manager.tasks["source"]; exists {
		t.Fatal("deleted task still exists")
	}
	if _, err := credentials.Load("source"); err == nil {
		t.Fatal("deleted task credential still exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(source) error = %v, want os.ErrNotExist", err)
	}
}

func TestLinkTestUsesConfiguredConnectionTester(t *testing.T) {
	manager := &fakeManager{tasks: map[string]task.Task{
		"source": {ID: "source", Role: task.RoleSource, SourcePath: "/private/source"},
	}}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	tester := &fakeConnectionTester{}
	sourceCertificatePEM := testCertificatePEM(t)
	relayCertificatePEM := testRelayCertificatePEM(t)
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		ConnectionTester:     tester,
		DirectTLSConfigured:  true,
		DirectTLSCertificate: sourceCertificatePEM,
		CertificateFetcher:   &fakeCertificateFetcher{certificate: relayCertificatePEM},
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}
	issue := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links", map[string]any{
		"task_id": "source", "endpoint": "192.168.1.10:7443", "relay_endpoint": "relay.example:7443",
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, issue)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, body = %s", response.Code, response.Body.String())
	}
	var issued struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	check := jsonRequest(http.MethodPost, "http://127.0.0.1/api/links/test", map[string]any{
		"link": issued.Link,
	})
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, check)
	if response.Code != http.StatusOK {
		t.Fatalf("test status = %d, body = %s", response.Code, response.Body.String())
	}
	if tester.endpoint != "192.168.1.10:7443" || tester.relayEndpoint != "relay.example:7443" {
		t.Fatalf("tester called with endpoint=%q relay=%q", tester.endpoint, tester.relayEndpoint)
	}
	if !bytes.Contains([]byte(tester.caCertificatePEM), []byte(sourceCertificatePEM)) ||
		!bytes.Contains([]byte(tester.caCertificatePEM), []byte(relayCertificatePEM)) {
		t.Fatal("tester did not receive bundled link certificates")
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"usable":true`)) {
		t.Fatalf("test response = %s", response.Body.String())
	}
}

func TestEndpointSuggestionsAPI(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	suggester := &fakeEndpointSuggester{suggestions: []string{"10.0.0.7:7443", "192.168.1.10:7443"}}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		EndpointSuggester: suggester,
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/endpoint-suggestions?port=7443", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if suggester.port != 7443 {
		t.Fatalf("suggestion port = %d, want 7443", suggester.port)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("192.168.1.10:7443")) {
		t.Fatalf("response = %s", response.Body.String())
	}
}

func TestEndpointSuggestionsAPIDefaultsToConfiguredPort(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	suggester := &fakeEndpointSuggester{}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		EndpointSuggester: suggester,
		SyncPort:          9443,
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/endpoint-suggestions", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if suggester.port != 9443 {
		t.Fatalf("suggestion port = %d, want 9443", suggester.port)
	}
}

func TestConfigAPIReportsSyncPort(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		SyncPort:            9443,
		ManagementBind:      "0.0.0.0",
		ManagementPort:      8766,
		DataDir:             "/tmp/onesync-test",
		LogFile:             "/tmp/onesync-test/logs/onesync.log",
		SyncInterval:        15 * time.Second,
		DirectTLSConfigured: true,
		DirectTLSHosts:      []string{"192.168.1.10", "source.local"},
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/config", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"sync_port":9443`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"management_bind":"0.0.0.0"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"management_port":8766`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"data_dir":"/tmp/onesync-test"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"log_file":"/tmp/onesync-test/logs/onesync.log"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"sync_interval":"15s"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"direct_tls_configured":true`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"direct_tls_hosts":["192.168.1.10","source.local"]`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"direct_tls_endpoints":["192.168.1.10:9443","source.local:9443"]`)) {
		t.Fatalf("config response status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestDiagnosticsPackageIncludesTextAndLogTail(t *testing.T) {
	manager := &fakeManager{tasks: map[string]task.Task{
		"source": {ID: "source", Role: task.RoleSource, SourcePath: "/private/source"},
	}}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "onesync.log")
	if err := os.WriteFile(logPath, []byte("service started\nsync completed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		LogFile: logPath,
	})
	if err != nil {
		t.Fatalf("NewServerWithOptions() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/diagnostics.zip", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	entries := make(map[string]string)
	for _, file := range reader.File {
		opened, err := file.Open()
		if err != nil {
			t.Fatalf("Open(%s) error = %v", file.Name, err)
		}
		data, err := io.ReadAll(opened)
		_ = opened.Close()
		if err != nil {
			t.Fatalf("ReadAll(%s) error = %v", file.Name, err)
		}
		entries[file.Name] = string(data)
	}
	if !strings.Contains(entries["diagnostics.txt"], "任务: source") {
		t.Fatalf("diagnostics.txt = %q", entries["diagnostics.txt"])
	}
	if !strings.Contains(entries["service-log.txt"], "sync completed") {
		t.Fatalf("service-log.txt = %q", entries["service-log.txt"])
	}
}

func TestCertificateEndpoints(t *testing.T) {
	got := certificateEndpoints([]string{
		"192.168.1.10",
		"source.local",
		"::1",
		"*.example.com",
		"192.168.1.10",
		" ",
	}, 9443)
	want := []string{"192.168.1.10:9443", "source.local:9443", "[::1]:9443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("certificateEndpoints() = %v, want %v", got, want)
	}
}

func TestServerRejectsInvalidSyncPort(t *testing.T) {
	manager := &fakeManager{tasks: make(map[string]task.Task)}
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	if _, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{SyncPort: 70000}); err == nil {
		t.Fatal("NewServerWithOptions() accepted invalid sync port")
	}
}

func TestEndpointSuggestionsAPIRejectsInvalidPort(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/endpoint-suggestions?port=bad", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newServerWithManager(t, &fakeManager{tasks: make(map[string]task.Task)})
}

func newServerWithManager(t *testing.T, manager taskManager) *Server {
	t.Helper()
	credentials, err := auth.NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	server, err := NewServer(manager, auth.NewLinkService(), credentials)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func jsonRequest(method, target string, value any) *http.Request {
	data, _ := json.Marshal(value)
	request := httptest.NewRequest(method, target, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://127.0.0.1")
	return request
}

type fakeManager struct {
	tasks map[string]task.Task
}

func (m *fakeManager) Create(_ context.Context, created task.Task) error {
	created.State = task.StateCreated
	m.tasks[created.ID] = created
	return nil
}

func (m *fakeManager) Start(context.Context, string) error { return nil }
func (m *fakeManager) Stop(context.Context, string) error  { return nil }
func (m *fakeManager) Delete(_ context.Context, id string) error {
	if _, ok := m.tasks[id]; !ok {
		return task.ErrTaskNotFound
	}
	delete(m.tasks, id)
	return nil
}
func (m *fakeManager) UpdateIgnoreRules(_ context.Context, id string, rules []string) error {
	found, ok := m.tasks[id]
	if !ok {
		return task.ErrTaskNotFound
	}
	found.IgnoreRules = append([]string(nil), rules...)
	m.tasks[id] = found
	return nil
}
func (m *fakeManager) RenameDevice(_ context.Context, id, alias string) error {
	found, ok := m.tasks[id]
	if !ok {
		return task.ErrTaskNotFound
	}
	found.Devices.Alias = alias
	m.tasks[id] = found
	return nil
}
func (m *fakeManager) SetDeviceTrusted(_ context.Context, id string, trusted bool) error {
	found, ok := m.tasks[id]
	if !ok {
		return task.ErrTaskNotFound
	}
	found.DeviceTrusted = trusted
	m.tasks[id] = found
	return nil
}
func (m *fakeManager) SetDeviceDisabled(_ context.Context, id string, disabled bool) error {
	found, ok := m.tasks[id]
	if !ok {
		return task.ErrTaskNotFound
	}
	found.DeviceDisabled = disabled
	m.tasks[id] = found
	return nil
}
func (m *fakeManager) ClearDeviceBinding(_ context.Context, id string) error {
	found, ok := m.tasks[id]
	if !ok {
		return task.ErrTaskNotFound
	}
	found.Devices.PeerID = ""
	found.Devices.Connected = 0
	m.tasks[id] = found
	return nil
}

func (m *fakeManager) Get(_ context.Context, id string) (task.Task, error) {
	found, ok := m.tasks[id]
	if !ok {
		return task.Task{}, task.ErrTaskNotFound
	}
	return found, nil
}

func (m *fakeManager) List(context.Context) ([]task.Task, error) {
	tasks := make([]task.Task, 0, len(m.tasks))
	for _, found := range m.tasks {
		tasks = append(tasks, found)
	}
	return tasks, nil
}

type fakeConnectionTester struct {
	endpoint         string
	relayEndpoint    string
	caCertificatePEM string
}

func (t *fakeConnectionTester) TestWithCertificate(_ context.Context, endpoint, relayEndpoint, caCertificatePEM string) diagnostic.Result {
	t.endpoint = endpoint
	t.relayEndpoint = relayEndpoint
	t.caCertificatePEM = caCertificatePEM
	return diagnostic.Result{
		Direct: diagnostic.EndpointResult{Endpoint: endpoint, OK: true},
		Relay:  &diagnostic.EndpointResult{Endpoint: relayEndpoint, OK: true},
		Usable: true,
	}
}

type fakeCertificateFetcher struct {
	endpoint    string
	certificate string
	err         error
}

func (f *fakeCertificateFetcher) Fetch(_ context.Context, endpoint string) (string, error) {
	f.endpoint = endpoint
	if f.err != nil {
		return "", f.err
	}
	return f.certificate, nil
}

func testCertificatePEM(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	certPath := filepath.Join(root, "source.crt")
	keyPath := filepath.Join(root, "source.key")
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{"192.168.1.10"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: time.Hour,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return string(data)
}

func testRelayCertificatePEM(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	certPath := filepath.Join(root, "relay.crt")
	keyPath := filepath.Join(root, "relay.key")
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{"relay.example"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: time.Hour,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return string(data)
}

type fakeEndpointSuggester struct {
	port        int
	suggestions []string
}

func (s *fakeEndpointSuggester) Suggestions(port int) ([]string, error) {
	s.port = port
	return s.suggestions, nil
}
