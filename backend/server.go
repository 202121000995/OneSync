package backend

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	syncauth "github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/netutil"
	"github.com/202121000995/OneSync/internal/task"
	"github.com/202121000995/OneSync/internal/webauth"
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

type certificateFetcher interface {
	Fetch(ctx context.Context, endpoint string) (string, error)
}

type localEndpointSuggester struct{}

func (localEndpointSuggester) Suggestions(port int) ([]string, error) {
	return netutil.LocalEndpointSuggestions(port)
}

type tlsCertificateFetcher struct {
	timeout time.Duration
}

func (f tlsCertificateFetcher) Fetch(ctx context.Context, endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", errors.New("Relay endpoint is required")
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Relay endpoint: %w", err)
	}
	timeout := f.timeout
	if timeout == 0 {
		timeout = diagnostic.DefaultTimeout
	}
	checkContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config: &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS13,
		},
	}
	connection, err := dialer.DialContext(checkContext, "tcp", endpoint)
	if err != nil {
		return "", fmt.Errorf("read Relay TLS certificate: %w", err)
	}
	defer connection.Close()
	tlsConnection, ok := connection.(*tls.Conn)
	if !ok {
		return "", errors.New("Relay connection is not TLS")
	}
	state := tlsConnection.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", errors.New("Relay did not present a certificate")
	}
	certificate := state.PeerCertificates[0]
	if err := certificate.VerifyHostname(strings.Trim(host, "[]")); err != nil {
		return "", fmt.Errorf("Relay certificate does not match Relay address: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})), nil
}

// Options controls optional management server dependencies.
type Options struct {
	ConnectionTester     connectionTester
	EndpointSuggester    endpointSuggester
	CertificateFetcher   certificateFetcher
	WebAuth              *webauth.Store
	SyncPort             int
	DirectTLSConfigured  bool
	DirectTLSHosts       []string
	DirectTLSCertificate string
	Version              string
}

// Server provides the local management API and web page.
type Server struct {
	manager              taskManager
	links                *syncauth.LinkService
	credentials          *syncauth.CredentialStore
	connectionTester     connectionTester
	endpointSuggester    endpointSuggester
	certificateFetcher   certificateFetcher
	webAuth              *webauth.Store
	sessionMu            sync.Mutex
	sessions             map[string]time.Time
	syncPort             int
	directTLSConfigured  bool
	directTLSHosts       []string
	directTLSCertificate string
	version              string
	handler              http.Handler
}

// NewServer creates a local management server.
func NewServer(manager taskManager, links *syncauth.LinkService, credentials *syncauth.CredentialStore) (*Server, error) {
	return NewServerWithOptions(manager, links, credentials, Options{})
}

// NewServerWithOptions creates a local management server with optional dependencies.
func NewServerWithOptions(manager taskManager, links *syncauth.LinkService, credentials *syncauth.CredentialStore, options Options) (*Server, error) {
	if manager == nil || links == nil || credentials == nil {
		return nil, errors.New("manager, link service, and credential store are required")
	}
	server := &Server{
		manager:              manager,
		links:                links,
		credentials:          credentials,
		connectionTester:     options.ConnectionTester,
		endpointSuggester:    options.EndpointSuggester,
		certificateFetcher:   options.CertificateFetcher,
		webAuth:              options.WebAuth,
		sessions:             make(map[string]time.Time),
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
	if server.certificateFetcher == nil {
		server.certificateFetcher = tlsCertificateFetcher{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/status", server.authStatus)
	mux.HandleFunc("POST /api/auth/setup", server.authSetup)
	mux.HandleFunc("POST /api/auth/login", server.authLogin)
	mux.HandleFunc("POST /api/auth/logout", server.authLogout)
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
	return s.ListenAndServeOn(ctx, "127.0.0.1", port)
}

// ListenAndServeOn binds the management server to the requested IPv4 address.
func (s *Server) ListenAndServeOn(ctx context.Context, address string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("management port is invalid")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		address = "127.0.0.1"
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(address, strconv.Itoa(port)))
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

func (s *Server) authStatus(writer http.ResponseWriter, request *http.Request) {
	configured := s.webAuth != nil && s.webAuth.Configured()
	writeJSON(writer, http.StatusOK, map[string]any{
		"enabled":       s.webAuth != nil,
		"configured":    configured,
		"authenticated": configured && s.authenticated(request),
	})
}

func (s *Server) authSetup(writer http.ResponseWriter, request *http.Request) {
	if s.webAuth == nil {
		writeAPIError(writer, http.StatusBadRequest, errors.New("management auth is not enabled"))
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	if err := s.webAuth.Setup(input.Username, input.Password); err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	s.setSession(writer)
	writeJSON(writer, http.StatusCreated, map[string]string{"status": "configured"})
}

func (s *Server) authLogin(writer http.ResponseWriter, request *http.Request) {
	if s.webAuth == nil || !s.webAuth.Configured() {
		writeAPIError(writer, http.StatusBadRequest, errors.New("management account is not configured"))
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	if !s.webAuth.Verify(input.Username, input.Password) {
		writeAPIError(writer, http.StatusUnauthorized, errors.New("username or password is incorrect"))
		return
	}
	s.setSession(writer)
	writeJSON(writer, http.StatusOK, map[string]string{"status": "authenticated"})
}

func (s *Server) authLogout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie("onesync_session"); err == nil {
		s.sessionMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionMu.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     "onesync_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(writer, http.StatusOK, map[string]string{"status": "logged_out"})
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

func certificateBundle(certificates ...string) string {
	var builder strings.Builder
	for _, certificate := range certificates {
		certificate = strings.TrimSpace(certificate)
		if certificate == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(certificate)
		builder.WriteByte('\n')
	}
	return builder.String()
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
	if strings.TrimSpace(input.RelayEndpoint) != "" {
		relayCertificate, err := s.certificateFetcher.Fetch(request.Context(), input.RelayEndpoint)
		if err != nil {
			writeAPIError(writer, http.StatusBadRequest, err)
			return
		}
		caCertificate = certificateBundle(caCertificate, relayCertificate)
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
	if err := s.credentials.Save(input.TaskID, syncauth.Credential{
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
	peerID, err := syncauth.NewPeerID()
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
	if err := s.credentials.Save(input.TaskID, syncauth.Credential{
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
		if s.webAuth == nil && host != "127.0.0.1" && host != "localhost" {
			http.Error(writer, "local access only", http.StatusForbidden)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			origin := request.Header.Get("Origin")
			if origin != "" && !validOrigin(origin, request.Host, s.webAuth != nil) {
				http.Error(writer, "invalid request origin", http.StatusForbidden)
				return
			}
			if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
				http.Error(writer, "application/json required", http.StatusUnsupportedMediaType)
				return
			}
		}
		if strings.HasPrefix(request.URL.Path, "/api/") && !s.authAllowed(request) {
			writeAPIError(writer, http.StatusUnauthorized, errors.New("management login is required"))
			return
		}
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) authAllowed(request *http.Request) bool {
	if request.URL.Path == "/api/auth/status" {
		return true
	}
	if s.webAuth == nil {
		return true
	}
	configured := s.webAuth.Configured()
	if !configured && request.URL.Path == "/api/auth/setup" {
		return true
	}
	if configured && (request.URL.Path == "/api/auth/login" || request.URL.Path == "/api/auth/logout") {
		return true
	}
	return configured && s.authenticated(request)
}

func (s *Server) authenticated(request *http.Request) bool {
	cookie, err := request.Cookie("onesync_session")
	if err != nil || cookie.Value == "" {
		return false
	}
	now := time.Now().UTC()
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	expiresAt, exists := s.sessions[cookie.Value]
	if !exists || !now.Before(expiresAt) {
		delete(s.sessions, cookie.Value)
		return false
	}
	return true
}

func (s *Server) setSession(writer http.ResponseWriter) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	expiresAt := time.Now().UTC().Add(30 * 24 * time.Hour)
	s.sessionMu.Lock()
	s.sessions[token] = expiresAt
	s.sessionMu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name:     "onesync_session",
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func validOrigin(origin, requestHost string, remoteAuth bool) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil {
		return false
	}
	host := parsed.Hostname()
	if !remoteAuth {
		return host == "127.0.0.1" || host == "localhost"
	}
	requestHostName, _, err := net.SplitHostPort(requestHost)
	if err != nil {
		requestHostName = requestHost
	}
	return strings.EqualFold(host, requestHostName)
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
