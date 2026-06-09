package backend

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/netutil"
	"github.com/202121000995/OneSync/internal/task"
)

const (
	maxRequestBody  = 1 << 20
	DefaultSyncPort = 7443
)

//go:embed web/*
var webFiles embed.FS

type taskManager interface {
	Create(ctx context.Context, task task.Task) error
	Start(ctx context.Context, taskID string) error
	Stop(ctx context.Context, taskID string) error
	Delete(ctx context.Context, taskID string) error
	UpdateIgnoreRules(ctx context.Context, taskID string, rules []string) error
	Get(ctx context.Context, taskID string) (task.Task, error)
	List(ctx context.Context) ([]task.Task, error)
}

type connectionTester interface {
	TestWithCertificate(ctx context.Context, endpoint, relayEndpoint, caCertificatePEM string) diagnostic.Result
}

type endpointSuggester interface {
	Suggestions(port int) ([]string, error)
}

type localEndpointSuggester struct{}

func (localEndpointSuggester) Suggestions(port int) ([]string, error) {
	return netutil.LocalEndpointSuggestions(port)
}

// Options controls optional management server dependencies.
type Options struct {
	ConnectionTester     connectionTester
	EndpointSuggester    endpointSuggester
	SyncPort             int
	DirectTLSConfigured  bool
	DirectTLSHosts       []string
	DirectTLSCertificate string
	Version              string
}

// Server provides the local management API and web page.
type Server struct {
	manager              taskManager
	links                *auth.LinkService
	credentials          *auth.CredentialStore
	connectionTester     connectionTester
	endpointSuggester    endpointSuggester
	syncPort             int
	directTLSConfigured  bool
	directTLSHosts       []string
	directTLSCertificate string
	version              string
	handler              http.Handler
}

// NewServer creates a local management server.
func NewServer(manager taskManager, links *auth.LinkService, credentials *auth.CredentialStore) (*Server, error) {
	return NewServerWithOptions(manager, links, credentials, Options{})
}

// NewServerWithOptions creates a local management server with optional dependencies.
func NewServerWithOptions(manager taskManager, links *auth.LinkService, credentials *auth.CredentialStore, options Options) (*Server, error) {
	if manager == nil || links == nil || credentials == nil {
		return nil, errors.New("manager, link service, and credential store are required")
	}
	server := &Server{
		manager:              manager,
		links:                links,
		credentials:          credentials,
		connectionTester:     options.ConnectionTester,
		endpointSuggester:    options.EndpointSuggester,
		syncPort:             options.SyncPort,
		directTLSConfigured:  options.DirectTLSConfigured,
		directTLSHosts:       append([]string(nil), options.DirectTLSHosts...),
		directTLSCertificate: options.DirectTLSCertificate,
		version:              options.Version,
	}
	if server.version == "" {
		server.version = "dev"
	}
	if server.syncPort == 0 {
		server.syncPort = DefaultSyncPort
	}
	if server.syncPort < 1 || server.syncPort > 65535 {
		return nil, errors.New("sync port is invalid")
	}
	if server.endpointSuggester == nil {
		server.endpointSuggester = localEndpointSuggester{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks", server.listTasks)
	mux.HandleFunc("GET /api/config", server.config)
	mux.HandleFunc("GET /api/endpoint-suggestions", server.endpointSuggestions)
	mux.HandleFunc("POST /api/tasks", server.createTask)
	mux.HandleFunc("POST /api/tasks/{id}/start", server.startTask)
	mux.HandleFunc("POST /api/tasks/{id}/stop", server.stopTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", server.updateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", server.deleteTask)
	mux.HandleFunc("POST /api/links", server.issueLink)
	mux.HandleFunc("POST /api/links/join", server.joinLink)
	mux.HandleFunc("POST /api/links/test", server.testLink)

	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	mux.Handle("/", http.FileServer(http.FS(static)))
	server.handler = server.securityMiddleware(mux)
	return server, nil
}

// Handler returns the complete local HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ListenAndServe binds only to the IPv4 loopback interface.
func (s *Server) ListenAndServe(ctx context.Context, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("management port is invalid")
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	tasks, err := s.manager.List(request.Context())
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) config(writer http.ResponseWriter, _ *http.Request) {
	directTLSHosts := s.directTLSHosts
	if directTLSHosts == nil {
		directTLSHosts = []string{}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"sync_port":             s.syncPort,
		"direct_tls_configured": s.directTLSConfigured,
		"direct_tls_hosts":      directTLSHosts,
		"direct_tls_endpoints":  certificateEndpoints(directTLSHosts, s.syncPort),
		"version":               s.version,
	})
}

func certificateEndpoints(hosts []string, port int) []string {
	endpoints := make([]string, 0, len(hosts))
	seen := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		value := strings.TrimSpace(host)
		if value == "" || strings.HasPrefix(value, "*.") {
			continue
		}
		endpoint := net.JoinHostPort(value, strconv.Itoa(port))
		if _, exists := seen[endpoint]; exists {
			continue
		}
		seen[endpoint] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func (s *Server) endpointSuggestions(writer http.ResponseWriter, request *http.Request) {
	port := s.syncPort
	if rawPort := strings.TrimSpace(request.URL.Query().Get("port")); rawPort != "" {
		parsedPort, err := strconv.Atoi(rawPort)
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, errors.New("endpoint suggestion port is invalid"))
			return
		}
		port = parsedPort
	}
	suggestions, err := s.endpointSuggester.Suggestions(port)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"suggestions": suggestions})
}

func (s *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ID         string `json:"id"`
		Role       string `json:"role"`
		SourcePath string `json:"source_path"`
		TargetPath string `json:"target_path"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	created := task.Task{
		ID: input.ID, Role: input.Role, SourcePath: input.SourcePath, TargetPath: input.TargetPath,
	}
	if err := s.manager.Create(request.Context(), created); err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"task": created.ID})
}

func (s *Server) startTask(writer http.ResponseWriter, request *http.Request) {
	if err := s.manager.Start(request.Context(), request.PathValue("id")); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]string{"status": "starting"})
}

func (s *Server) stopTask(writer http.ResponseWriter, request *http.Request) {
	if err := s.manager.Stop(request.Context(), request.PathValue("id")); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) updateTask(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		IgnoreRules []string `json:"ignore_rules"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	if err := s.manager.UpdateIgnoreRules(request.Context(), request.PathValue("id"), input.IgnoreRules); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) deleteTask(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if err := s.manager.Delete(request.Context(), taskID); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	if err := s.credentials.Delete(taskID); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) issueLink(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		TaskID        string `json:"task_id"`
		Endpoint      string `json:"endpoint"`
		RelayEndpoint string `json:"relay_endpoint"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	existing, err := s.manager.Get(request.Context(), input.TaskID)
	if err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	if existing.Role != task.RoleSource {
		writeAPIError(writer, http.StatusBadRequest, errors.New("only source tasks can issue links"))
		return
	}
	if !s.directTLSConfigured && strings.TrimSpace(input.RelayEndpoint) == "" {
		writeAPIError(writer, http.StatusBadRequest, errors.New("source direct connection is not ready; restart OneSync or enter a Relay endpoint"))
		return
	}
	caCertificate := ""
	if s.directTLSConfigured {
		caCertificate = s.directTLSCertificate
	}
	encoded, err := s.links.IssueWithCertificate(input.TaskID, input.Endpoint, input.RelayEndpoint, caCertificate)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	link, err := s.links.Parse(encoded)
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	if err := s.credentials.Save(input.TaskID, auth.Credential{
		SessionID: link.SessionID, Endpoint: link.Endpoint,
		RelayEndpoint: link.RelayEndpoint, CACertificatePEM: link.CACertificatePEM,
		Token: link.Token, OneTime: true,
	}); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"link": encoded, "expires_at": link.ExpiresAt,
	})
}

func (s *Server) joinLink(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		TaskID     string `json:"task_id"`
		TargetPath string `json:"target_path"`
		Link       string `json:"link"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	link, err := s.links.Parse(input.Link)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	if !time.Now().UTC().Before(link.ExpiresAt) {
		writeAPIError(writer, http.StatusBadRequest, errors.New("synchronization link has expired"))
		return
	}
	if input.TaskID == "" {
		input.TaskID = link.SessionID + "-target"
	}
	peerID, err := auth.NewPeerID()
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	if _, err := s.manager.Get(request.Context(), input.TaskID); err == nil {
		writeAPIError(writer, http.StatusBadRequest, errors.New("task ID already exists"))
		return
	} else if !errors.Is(err, task.ErrTaskNotFound) {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	joined := task.Task{
		ID: input.TaskID, Role: task.RoleTarget, TargetPath: input.TargetPath,
		PeerAddress: link.Endpoint, RelayURL: link.RelayEndpoint,
	}
	if err := s.credentials.Save(input.TaskID, auth.Credential{
		SessionID: link.SessionID, Endpoint: link.Endpoint,
		RelayEndpoint: link.RelayEndpoint, CACertificatePEM: link.CACertificatePEM,
		Token: link.Token, PeerID: peerID,
	}); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	if err := s.manager.Create(request.Context(), joined); err != nil {
		_ = s.credentials.Delete(input.TaskID)
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"task": input.TaskID})
}

func (s *Server) testLink(writer http.ResponseWriter, request *http.Request) {
	if s.connectionTester == nil {
		writeAPIError(writer, http.StatusServiceUnavailable, errors.New("connection tester is not configured"))
		return
	}
	var input struct {
		Link string `json:"link"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	link, err := s.links.Parse(input.Link)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	if !time.Now().UTC().Before(link.ExpiresAt) {
		writeAPIError(writer, http.StatusBadRequest, errors.New("synchronization link has expired"))
		return
	}
	result := s.connectionTester.TestWithCertificate(request.Context(), link.Endpoint, link.RelayEndpoint, link.CACertificatePEM)
	writeJSON(writer, http.StatusOK, result)
}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, _, err := net.SplitHostPort(request.Host)
		if err != nil {
			host = request.Host
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(writer, "local access only", http.StatusForbidden)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			origin := request.Header.Get("Origin")
			if origin != "" && !validLocalOrigin(origin) {
				http.Error(writer, "invalid request origin", http.StatusForbidden)
				return
			}
			if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
				http.Error(writer, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
		}
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func validLocalOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "127.0.0.1" || host == "localhost"
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBody)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(writer, http.StatusBadRequest, errors.New("invalid JSON request"))
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(writer, http.StatusBadRequest, errors.New("request must contain one JSON value"))
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeAPIError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

func statusForTaskError(err error) int {
	if errors.Is(err, task.ErrTaskNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}
