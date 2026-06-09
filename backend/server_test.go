package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/task"
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
}

func TestLinkIssueAndJoinStoresCredentialSeparately(t *testing.T) {
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
	if manager.tasks["target"].PeerAddress != "192.168.1.10:7443" {
		t.Fatalf("joined task = %+v", manager.tasks["target"])
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
	server, err := NewServerWithOptions(manager, auth.NewLinkService(), credentials, Options{
		ConnectionTester: tester,
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
		!bytes.Contains(response.Body.Bytes(), []byte(`"direct_tls_configured":true`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"direct_tls_hosts":["192.168.1.10","source.local"]`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"direct_tls_endpoints":["192.168.1.10:9443","source.local:9443"]`)) {
		t.Fatalf("config response status = %d, body = %s", response.Code, response.Body.String())
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
	endpoint      string
	relayEndpoint string
}

func (t *fakeConnectionTester) Test(_ context.Context, endpoint, relayEndpoint string) diagnostic.Result {
	t.endpoint = endpoint
	t.relayEndpoint = relayEndpoint
	return diagnostic.Result{
		Direct: diagnostic.EndpointResult{Endpoint: endpoint, OK: true},
		Relay:  &diagnostic.EndpointResult{Endpoint: relayEndpoint, OK: true},
		Usable: true,
	}
}

type fakeEndpointSuggester struct {
	port        int
	suggestions []string
}

func (s *fakeEndpointSuggester) Suggestions(port int) ([]string, error) {
	s.port = port
	return s.suggestions, nil
}
