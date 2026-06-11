package backend

import (
	"archive/zip"
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
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	syncauth "github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/netutil"
	"github.com/202121000995/OneSync/internal/scanner"
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
	Rescan(ctx context.Context, taskID string) error
	Stop(ctx context.Context, taskID string) error
	Delete(ctx context.Context, taskID string) error
	UpdateIgnoreRules(ctx context.Context, taskID string, rules []string) error
	UpdateTargetLink(ctx context.Context, taskID, targetPath, peerAddress, relayURL string) error
	RenameDevice(ctx context.Context, taskID, alias string) error
	SetDeviceTrusted(ctx context.Context, taskID string, trusted bool) error
	SetDeviceDisabled(ctx context.Context, taskID string, disabled bool) error
	ClearDeviceBinding(ctx context.Context, taskID string) error
	ClearLogs(ctx context.Context, taskID string) error
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
	ManagementBind       string
	ManagementPort       int
	DataDir              string
	LogFile              string
	SyncInterval         time.Duration
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
	managementBind       string
	managementPort       int
	dataDir              string
	logFile              string
	syncInterval         time.Duration
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
		managementBind:       strings.TrimSpace(options.ManagementBind),
		managementPort:       options.ManagementPort,
		dataDir:              options.DataDir,
		logFile:              options.LogFile,
		syncInterval:         options.SyncInterval,
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
	if server.managementBind == "" {
		server.managementBind = "127.0.0.1"
	}
	if server.managementPort == 0 {
		server.managementPort = 8765
	}
	if server.syncInterval == 0 {
		server.syncInterval = 30 * time.Second
	}
	if server.syncPort < 1 || server.syncPort > 65535 {
		return nil, errors.New("sync port is invalid")
	}
	if server.managementPort < 1 || server.managementPort > 65535 {
		return nil, errors.New("management port is invalid")
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
	mux.HandleFunc("GET /api/diagnostics", server.allDiagnostics)
	mux.HandleFunc("GET /api/diagnostics.zip", server.diagnosticsPackage)
	mux.HandleFunc("GET /api/ignore/templates", server.ignoreTemplates)
	mux.HandleFunc("GET /api/endpoint-suggestions", server.endpointSuggestions)
	mux.HandleFunc("POST /api/tasks", server.createTask)
	mux.HandleFunc("POST /api/tasks/{id}/start", server.startTask)
	mux.HandleFunc("POST /api/tasks/{id}/rescan", server.rescanTask)
	mux.HandleFunc("POST /api/tasks/{id}/stop", server.stopTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", server.updateTask)
	mux.HandleFunc("PATCH /api/tasks/{id}/device", server.updateDevice)
	mux.HandleFunc("POST /api/tasks/{id}/device/kick", server.kickDevice)
	mux.HandleFunc("POST /api/tasks/{id}/ignore-preview", server.previewIgnored)
	mux.HandleFunc("GET /api/tasks/{id}/diagnostics", server.taskDiagnostics)
	mux.HandleFunc("POST /api/logs/clear", server.clearLogs)
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
		"management_bind":       s.managementBind,
		"management_port":       s.managementPort,
		"data_dir":              s.dataDir,
		"log_file":              s.logFile,
		"sync_interval":         s.syncInterval.String(),
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

func (s *Server) rescanTask(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	if err := s.manager.Rescan(request.Context(), taskID); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	if err := s.manager.Start(request.Context(), taskID); err != nil {
		if errors.Is(err, task.ErrTaskAlreadyRunning) {
			writeJSON(writer, http.StatusOK, map[string]string{"status": "rescanned", "task": "running"})
			return
		}
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]string{"status": "rescanned", "task": "starting"})
}

func (s *Server) stopTask(writer http.ResponseWriter, request *http.Request) {
	if err := s.manager.Stop(request.Context(), request.PathValue("id")); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) clearLogs(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	if err := s.manager.ClearLogs(request.Context(), input.TaskID); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "cleared"})
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

func (s *Server) updateDevice(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	var input struct {
		Alias    *string `json:"alias"`
		Trusted  *bool   `json:"trusted"`
		Disabled *bool   `json:"disabled"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	if input.Disabled != nil && *input.Disabled {
		_ = s.manager.Stop(request.Context(), taskID)
	}
	if input.Alias != nil {
		if err := s.manager.RenameDevice(request.Context(), taskID, *input.Alias); err != nil {
			writeAPIError(writer, statusForTaskError(err), err)
			return
		}
	}
	if input.Trusted != nil {
		if err := s.manager.SetDeviceTrusted(request.Context(), taskID, *input.Trusted); err != nil {
			writeAPIError(writer, statusForTaskError(err), err)
			return
		}
	}
	if input.Disabled != nil {
		if err := s.manager.SetDeviceDisabled(request.Context(), taskID, *input.Disabled); err != nil {
			writeAPIError(writer, statusForTaskError(err), err)
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) kickDevice(writer http.ResponseWriter, request *http.Request) {
	taskID := request.PathValue("id")
	_ = s.manager.Stop(request.Context(), taskID)
	if err := s.credentials.UnbindPeer(taskID); err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	if err := s.manager.ClearDeviceBinding(request.Context(), taskID); err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "kicked"})
}

func (s *Server) ignoreTemplates(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"templates": []map[string]any{
			{
				"id":   "common",
				"name": "常用临时文件",
				"rules": []string{
					"*.tmp",
					"*.temp",
					"*.bak",
					"~$*",
					".DS_Store",
					"Thumbs.db",
					".onesync-part/",
				},
			},
			{
				"id":   "dev",
				"name": "开发项目",
				"rules": []string{
					".git/",
					"node_modules/",
					"dist/",
					"build/",
					".cache/",
					"*.log",
				},
			},
			{
				"id":   "media",
				"name": "照片视频缓存",
				"rules": []string{
					"*.tmp",
					"*.partial",
					"@eaDir/",
					".thumbnails/",
					"cache/",
				},
			},
		},
	})
}

func (s *Server) previewIgnored(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		IgnoreRules []string `json:"ignore_rules"`
		SamplePath  string   `json:"sample_path"`
		SampleIsDir bool     `json:"sample_is_dir"`
		Limit       int      `json:"limit"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		return
	}
	current, err := s.manager.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	if input.IgnoreRules == nil {
		input.IgnoreRules = current.IgnoreRules
	}
	root := current.SourcePath
	if current.Role == task.RoleTarget {
		root = current.TargetPath
	}
	entries, total, truncated, err := scanner.PreviewIgnored(request.Context(), root, input.IgnoreRules, input.Limit)
	if err != nil {
		writeAPIError(writer, http.StatusBadRequest, err)
		return
	}
	sampleRule := ""
	if strings.TrimSpace(input.SamplePath) != "" {
		sampleRule = scanner.MatchIgnoreRule(input.IgnoreRules, input.SamplePath, input.SampleIsDir)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"entries":      entries,
		"total":        total,
		"truncated":    truncated,
		"sample_match": sampleRule,
	})
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

func (s *Server) taskDiagnostics(writer http.ResponseWriter, request *http.Request) {
	current, err := s.manager.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeAPIError(writer, statusForTaskError(err), err)
		return
	}
	writeText(writer, http.StatusOK, s.diagnosticText([]task.Task{current}))
}

func (s *Server) allDiagnostics(writer http.ResponseWriter, request *http.Request) {
	tasks, err := s.manager.List(request.Context())
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	writeText(writer, http.StatusOK, s.diagnosticText(tasks))
}

func (s *Server) diagnosticsPackage(writer http.ResponseWriter, request *http.Request) {
	tasks, err := s.manager.List(request.Context())
	if err != nil {
		writeAPIError(writer, http.StatusInternalServerError, err)
		return
	}
	writer.Header().Set("Content-Type", "application/zip")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(
		`attachment; filename="onesync-diagnostics-%s.zip"`,
		time.Now().UTC().Format("20060102-150405"),
	))
	archive := zip.NewWriter(writer)
	defer archive.Close()
	if err := addZipText(archive, "diagnostics.txt", s.diagnosticText(tasks)); err != nil {
		return
	}
	if s.logFile == "" {
		_ = addZipText(archive, "service-log.txt", "未配置服务日志文件。\n")
		return
	}
	logTail, err := readTail(s.logFile, 256*1024)
	if err != nil {
		_ = addZipText(archive, "service-log.txt", fmt.Sprintf("读取服务日志失败: %v\n日志路径: %s\n", err, s.logFile))
		return
	}
	_ = addZipText(archive, "service-log.txt", logTail)
}

func addZipText(archive *zip.Writer, name, content string) error {
	file, err := archive.Create(name)
	if err != nil {
		return err
	}
	_, err = file.Write([]byte(content))
	return err
}

func readTail(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	offset := int64(0)
	if info.Size() > limit {
		offset = info.Size() - limit
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}
	prefix := fmt.Sprintf("日志路径: %s\n日志截取: 最近 %d 字节\n\n", path, len(data))
	if offset > 0 {
		prefix = fmt.Sprintf("日志路径: %s\n日志截取: 最后 %d 字节，前面内容已省略\n\n", path, len(data))
	}
	return prefix + string(data), nil
}

func (s *Server) issueLink(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		TaskID        string `json:"task_id"`
		Endpoint      string `json:"endpoint"`
		RelayEndpoint string `json:"relay_endpoint"`
		RelayToken    string `json:"relay_token"`
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
	encoded, err := s.links.IssueWithRelayCertificate(input.TaskID, input.Endpoint, input.RelayEndpoint, input.RelayToken, caCertificate)
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
		RelayEndpoint: link.RelayEndpoint, RelayToken: link.RelayToken, CACertificatePEM: link.CACertificatePEM,
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
	existing, err := s.manager.Get(request.Context(), input.TaskID)
	if err == nil {
		if existing.Role != task.RoleTarget {
			writeAPIError(writer, http.StatusBadRequest, errors.New("task ID already exists and is not a target task"))
			return
		}
		if strings.TrimSpace(input.TargetPath) == "" {
			input.TargetPath = existing.TargetPath
		}
		if input.TargetPath == "" {
			writeAPIError(writer, http.StatusBadRequest, errors.New("target path is required"))
			return
		}
		if err := s.credentials.Save(input.TaskID, syncauth.Credential{
			SessionID: link.SessionID, Endpoint: link.Endpoint,
			RelayEndpoint: link.RelayEndpoint, RelayToken: link.RelayToken, CACertificatePEM: link.CACertificatePEM,
			Token: link.Token, PeerID: peerID,
		}); err != nil {
			writeAPIError(writer, http.StatusInternalServerError, err)
			return
		}
		if err := s.manager.UpdateTargetLink(request.Context(), input.TaskID, input.TargetPath, link.Endpoint, link.RelayEndpoint); err != nil {
			writeAPIError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]string{"task": input.TaskID, "status": "rejoined"})
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
		RelayEndpoint: link.RelayEndpoint, RelayToken: link.RelayToken, CACertificatePEM: link.CACertificatePEM,
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

func writeText(writer http.ResponseWriter, status int, value string) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(value))
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

func (s *Server) diagnosticText(tasks []task.Task) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "OneSync 诊断日志\n")
	fmt.Fprintf(&builder, "生成时间: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&builder, "版本: %s\n", s.version)
	fmt.Fprintf(&builder, "管理页监听: %s:%d\n", s.managementBind, s.managementPort)
	fmt.Fprintf(&builder, "数据目录: %s\n", emptyText(s.dataDir))
	fmt.Fprintf(&builder, "服务日志: %s\n", emptyText(s.logFile))
	fmt.Fprintf(&builder, "同步端口: %d\n", s.syncPort)
	fmt.Fprintf(&builder, "同步间隔: %s\n", s.syncInterval)
	fmt.Fprintf(&builder, "源端直连 TLS: %s\n", yesNo(s.directTLSConfigured))
	fmt.Fprintf(&builder, "任务数量: %d\n\n", len(tasks))
	for _, item := range tasks {
		fmt.Fprintf(&builder, "任务: %s\n", item.ID)
		fmt.Fprintf(&builder, "  类型: %s\n", roleName(item.Role))
		fmt.Fprintf(&builder, "  状态: %s\n", item.State)
		fmt.Fprintf(&builder, "  错误分类: %s\n", errorCategory(item.LastError))
		if item.LastError != "" {
			fmt.Fprintf(&builder, "  最近错误: %s\n", item.LastError)
		}
		if item.Role == task.RoleSource {
			fmt.Fprintf(&builder, "  本地目录: %s\n", item.SourcePath)
		} else {
			fmt.Fprintf(&builder, "  本地目录: %s\n", item.TargetPath)
		}
		fmt.Fprintf(&builder, "  源端地址: %s\n", emptyText(item.PeerAddress))
		fmt.Fprintf(&builder, "  Relay 地址: %s\n", emptyText(item.RelayURL))
		fmt.Fprintf(&builder, "  本地大小: %d 字节 / %d 文件\n", item.Size.LocalBytes, item.Size.LocalFiles)
		fmt.Fprintf(&builder, "  全局大小: %d 字节 / %d 文件\n", item.Size.StandardBytes, item.Size.StandardFiles)
		fmt.Fprintf(&builder, "  累计接收: %d 字节\n", item.Traffic.ReceivedBytes)
		fmt.Fprintf(&builder, "  累计发送: %d 字节\n", item.Traffic.SentBytes)
		fmt.Fprintf(&builder, "  设备名称: %s\n", emptyText(item.Devices.Alias))
		fmt.Fprintf(&builder, "  设备信任: %s\n", yesNo(item.DeviceTrusted))
		fmt.Fprintf(&builder, "  设备禁用: %s\n", yesNo(item.DeviceDisabled))
		fmt.Fprintf(&builder, "  同步设备: %d / %d\n", item.Devices.Connected, item.Devices.Total)
		fmt.Fprintf(&builder, "  连接方式: %s\n", emptyText(item.Devices.Connection))
		fmt.Fprintf(&builder, "  设备详情源端地址: %s\n", emptyText(item.Devices.Endpoint))
		fmt.Fprintf(&builder, "  设备详情 Relay: %s\n", emptyText(item.Devices.RelayEndpoint))
		fmt.Fprintf(&builder, "  加密: %s\n", emptyText(item.Devices.TLS))
		fmt.Fprintf(&builder, "  最近连接: %s\n", timeText(item.Devices.LastSeen))
		fmt.Fprintf(&builder, "  设备历史:\n")
		if len(item.DeviceHistory) == 0 {
			fmt.Fprintf(&builder, "    - 暂无设备历史\n")
		}
		for _, event := range item.DeviceHistory {
			fmt.Fprintf(&builder, "    - [%s] %s %s %s %s\n", event.Type, timeText(event.Time), emptyText(event.Message), emptyText(event.Connection), emptyText(event.PeerID))
		}
		fmt.Fprintf(&builder, "  忽略规则: %d 条\n", len(item.IgnoreRules))
		for _, rule := range item.IgnoreRules {
			fmt.Fprintf(&builder, "    - %s\n", rule)
		}
		fmt.Fprintf(&builder, "  任务日志:\n")
		if len(item.Logs) == 0 {
			fmt.Fprintf(&builder, "    - 暂无日志\n")
		}
		for _, entry := range item.Logs {
			fmt.Fprintf(&builder, "    - [%s] %s %s\n", entry.Level, timeText(entry.Time), entry.Message)
		}
		builder.WriteByte('\n')
	}
	return builder.String()
}

func roleName(role string) string {
	if role == task.RoleSource {
		return "发送"
	}
	if role == task.RoleTarget {
		return "接收"
	}
	return role
}

func yesNo(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

func emptyText(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func timeText(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func errorCategory(message string) string {
	text := strings.ToLower(message)
	switch {
	case text == "":
		return "-"
	case strings.Contains(text, "credential"):
		return "同步链接/凭据"
	case strings.Contains(text, "certificate") || strings.Contains(text, "tls") || strings.Contains(text, "x509"):
		return "TLS 证书"
	case strings.Contains(text, "relay"):
		return "Relay 连接"
	case strings.Contains(text, "connect") || strings.Contains(text, "connection") || strings.Contains(text, "timeout"):
		return "网络连接"
	case strings.Contains(text, "scan") || strings.Contains(text, "stat") || strings.Contains(text, "permission") || strings.Contains(text, "path"):
		return "本地文件/权限"
	case strings.Contains(text, "disk") || strings.Contains(text, "space"):
		return "磁盘空间"
	case strings.Contains(text, "authentication"):
		return "同步认证"
	default:
		return "其他"
	}
}
