package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/202121000995/OneSync/internal/webauth"
)

type adminConfig struct {
	Listen       string
	AuthPath     string
	TokenFile    string
	CertPathFile string
	DefaultCert  string
	DefaultKey   string
	RelayListen  string
}

type adminServer struct {
	config     adminConfig
	auth       *webauth.Store
	sessionMu  sync.Mutex
	sessions   map[string]time.Time
	httpServer *http.Server
}

type adminPageData struct {
	Configured    bool
	Authenticated bool
	Message       string
	Error         string
	RelayListen   string
	TokenFile     string
	Token         string
	CertPath      string
	KeyPath       string
	CertSubject   string
	CertIssuer    string
	CertNotBefore string
	CertNotAfter  string
	CertDNSNames  string
	CertIPs       string
	CertError     string
}

func startAdminServer(ctx context.Context, config adminConfig) error {
	if strings.TrimSpace(config.Listen) == "" {
		return nil
	}
	auth, err := webauth.NewStore(config.AuthPath)
	if err != nil {
		return err
	}
	server := &adminServer{
		config:   config,
		auth:     auth,
		sessions: make(map[string]time.Time),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.index)
	mux.HandleFunc("/setup", server.setup)
	mux.HandleFunc("/login", server.login)
	mux.HandleFunc("/logout", server.logout)
	mux.HandleFunc("/rotate-token", server.rotateToken)
	mux.HandleFunc("/set-cert", server.setCert)
	mux.HandleFunc("/paste-cert", server.pasteCert)
	server.httpServer = &http.Server{
		Addr:              config.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return fmt.Errorf("listen Relay admin panel: %w", err)
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.httpServer.Shutdown(shutdownContext)
	}()
	go func() {
		log.Printf("OneSync Relay admin panel: http://%s", listener.Addr())
		if err := server.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("Relay admin panel stopped: %v", err)
		}
	}()
	return nil
}

func (s *adminServer) index(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	s.render(writer, request, adminPageData{})
}

func (s *adminServer) setup(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Redirect(writer, request, "/", http.StatusSeeOther)
		return
	}
	if s.auth.Configured() {
		s.render(writer, request, adminPageData{Error: "管理账号已经设置。"})
		return
	}
	if err := request.ParseForm(); err != nil {
		s.render(writer, request, adminPageData{Error: "表单格式不正确。"})
		return
	}
	if err := s.auth.Setup(request.FormValue("username"), request.FormValue("password")); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	s.setSession(writer)
	http.Redirect(writer, request, "/", http.StatusSeeOther)
}

func (s *adminServer) login(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Redirect(writer, request, "/", http.StatusSeeOther)
		return
	}
	if err := request.ParseForm(); err != nil {
		s.render(writer, request, adminPageData{Error: "表单格式不正确。"})
		return
	}
	if !s.auth.Verify(request.FormValue("username"), request.FormValue("password")) {
		s.render(writer, request, adminPageData{Error: "账号或密码不正确。"})
		return
	}
	s.setSession(writer)
	http.Redirect(writer, request, "/", http.StatusSeeOther)
}

func (s *adminServer) logout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie("onesync_relay_admin"); err == nil {
		s.sessionMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionMu.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     "onesync_relay_admin",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(writer, request, "/", http.StatusSeeOther)
}

func (s *adminServer) rotateToken(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !s.authenticated(request) {
		http.Redirect(writer, request, "/", http.StatusSeeOther)
		return
	}
	if s.config.TokenFile == "" {
		s.render(writer, request, adminPageData{Error: "当前 Relay 没有使用令牌文件，不能通过面板轮换令牌。"})
		return
	}
	token, err := randomRelayToken()
	if err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	if err := writePrivateFile(s.config.TokenFile, token+"\n"); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	s.render(writer, request, adminPageData{Message: "Relay 访问令牌已轮换，新连接会立即使用新令牌。旧同步链接需要重新生成。"})
}

func (s *adminServer) setCert(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !s.authenticated(request) {
		http.Redirect(writer, request, "/", http.StatusSeeOther)
		return
	}
	if s.config.CertPathFile == "" {
		s.render(writer, request, adminPageData{Error: "当前 Relay 没有配置证书路径记录文件，不能通过面板切换证书。"})
		return
	}
	if err := request.ParseForm(); err != nil {
		s.render(writer, request, adminPageData{Error: "表单格式不正确。"})
		return
	}
	certPath := strings.TrimSpace(request.FormValue("cert_path"))
	keyPath := strings.TrimSpace(request.FormValue("key_path"))
	if _, err := loadCertificateInfo(certPath, keyPath); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.config.CertPathFile), 0o700); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	if err := os.WriteFile(s.config.CertPathFile, []byte(certPath+"\n"+keyPath+"\n"), 0o644); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	s.render(writer, request, adminPageData{Message: "证书路径已保存，新建 TLS 连接会读取新的证书路径。"})
}

func (s *adminServer) pasteCert(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || !s.authenticated(request) {
		http.Redirect(writer, request, "/", http.StatusSeeOther)
		return
	}
	if s.config.CertPathFile == "" {
		s.render(writer, request, adminPageData{Error: "当前 Relay 没有配置证书路径记录文件，不能通过面板保存证书文本。"})
		return
	}
	if err := request.ParseForm(); err != nil {
		s.render(writer, request, adminPageData{Error: "表单格式不正确。"})
		return
	}
	certPEM := strings.TrimSpace(request.FormValue("cert_pem"))
	keyPEM := strings.TrimSpace(request.FormValue("key_pem"))
	if _, err := loadCertificateInfoFromPEM(certPEM, keyPEM); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	certPath, keyPath := s.managedCertPaths()
	if err := writePublicFile(certPath, certPEM+"\n"); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	if err := writePrivateFile(keyPath, keyPEM+"\n"); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.config.CertPathFile), 0o700); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	if err := os.WriteFile(s.config.CertPathFile, []byte(certPath+"\n"+keyPath+"\n"), 0o644); err != nil {
		s.render(writer, request, adminPageData{Error: err.Error()})
		return
	}
	s.render(writer, request, adminPageData{Message: "证书文本已保存并启用，新建 TLS 连接会读取新的证书。"})
}

func (s *adminServer) render(writer http.ResponseWriter, request *http.Request, data adminPageData) {
	data.Configured = s.auth.Configured()
	data.Authenticated = s.authenticated(request)
	data.RelayListen = s.config.RelayListen
	data.TokenFile = s.config.TokenFile
	if data.Authenticated {
		data.Token = readOptionalTrimmed(s.config.TokenFile)
		certPath, keyPath := s.currentCertPaths()
		data.CertPath = certPath
		data.KeyPath = keyPath
		if info, err := loadCertificateInfo(certPath, keyPath); err == nil {
			data.CertSubject = info.Subject.String()
			data.CertIssuer = info.Issuer.String()
			data.CertNotBefore = info.NotBefore.Format("2006-01-02 15:04:05 MST")
			data.CertNotAfter = info.NotAfter.Format("2006-01-02 15:04:05 MST")
			data.CertDNSNames = strings.Join(info.DNSNames, ", ")
			ips := make([]string, 0, len(info.IPAddresses))
			for _, ip := range info.IPAddresses {
				ips = append(ips, ip.String())
			}
			data.CertIPs = strings.Join(ips, ", ")
		} else {
			data.CertError = err.Error()
		}
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := adminTemplate.Execute(writer, data); err != nil {
		log.Printf("render Relay admin panel: %v", err)
	}
}

func (s *adminServer) authenticated(request *http.Request) bool {
	if !s.auth.Configured() {
		return false
	}
	cookie, err := request.Cookie("onesync_relay_admin")
	if err != nil || cookie.Value == "" {
		return false
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	expires, ok := s.sessions[cookie.Value]
	if !ok || time.Now().After(expires) {
		delete(s.sessions, cookie.Value)
		return false
	}
	s.sessions[cookie.Value] = time.Now().Add(12 * time.Hour)
	return true
}

func (s *adminServer) setSession(writer http.ResponseWriter) {
	token, err := randomRelayToken()
	if err != nil {
		return
	}
	s.sessionMu.Lock()
	s.sessions[token] = time.Now().Add(12 * time.Hour)
	s.sessionMu.Unlock()
	http.SetCookie(writer, &http.Cookie{
		Name:     "onesync_relay_admin",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *adminServer) currentCertPaths() (string, string) {
	if s.config.CertPathFile != "" {
		data, err := os.ReadFile(s.config.CertPathFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 && strings.TrimSpace(lines[0]) != "" && strings.TrimSpace(lines[1]) != "" {
				return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
			}
		}
	}
	return s.config.DefaultCert, s.config.DefaultKey
}

func (s *adminServer) managedCertPaths() (string, string) {
	if s.config.CertPathFile != "" {
		dir := filepath.Dir(s.config.CertPathFile)
		return filepath.Join(dir, "relay.crt"), filepath.Join(dir, "relay.key")
	}
	return s.config.DefaultCert, s.config.DefaultKey
}

func randomRelayToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func writePrivateFile(path, content string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func writePublicFile(path, content string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func readOptionalTrimmed(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func loadCertificateInfo(certPath, keyPath string) (*x509.Certificate, error) {
	certPath = strings.TrimSpace(certPath)
	keyPath = strings.TrimSpace(keyPath)
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("证书路径和私钥路径不能为空")
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("读取证书或私钥失败: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("证书文件没有包含证书")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("解析证书失败: %w", err)
	}
	return certificate, nil
}

func loadCertificateInfoFromPEM(certPEM, keyPEM string) (*x509.Certificate, error) {
	certPEM = strings.TrimSpace(certPEM)
	keyPEM = strings.TrimSpace(keyPEM)
	if certPEM == "" || keyPEM == "" {
		return nil, fmt.Errorf("证书 PEM 和私钥 KEY 不能为空")
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("证书 PEM 或私钥 KEY 无效，或二者不匹配: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("证书 PEM 没有包含证书")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("解析证书失败: %w", err)
	}
	return certificate, nil
}

var adminTemplate = template.Must(template.New("relay-admin").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OneSync Relay 管理</title>
<style>
body{margin:0;background:#f4f7fb;color:#172033;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif}
.wrap{max-width:980px;margin:38px auto;padding:0 18px}
.card{background:white;border:1px solid #dbe4f0;border-radius:18px;box-shadow:0 8px 30px rgba(15,23,42,.06);padding:24px;margin-bottom:18px}
h1{font-size:30px;margin:0 0 18px}h2{font-size:20px;margin:0 0 14px}
label{display:block;font-weight:700;margin:12px 0 6px}
input,textarea{width:100%;box-sizing:border-box;border:1px solid #cfd9e8;border-radius:10px;padding:10px 12px;font-size:15px}
textarea{min-height:220px;font-family:ui-monospace,SFMono-Regular,Consolas,"Liberation Mono",monospace;resize:vertical}
button{border:0;border-radius:10px;background:#2563eb;color:white;font-weight:800;padding:10px 18px;margin-top:14px;cursor:pointer}
button.secondary{background:#eef4ff;color:#1d4ed8}
.row{display:grid;grid-template-columns:1fr 1fr;gap:16px}
.msg{padding:12px 14px;border-radius:12px;background:#ecfdf5;color:#047857;margin-bottom:16px}
.err{padding:12px 14px;border-radius:12px;background:#fef2f2;color:#b91c1c;margin-bottom:16px}
.kv{display:grid;grid-template-columns:160px 1fr;gap:8px 14px;font-size:14px}
code,pre{background:#0f172a;color:#dbeafe;border-radius:10px;padding:10px;white-space:pre-wrap;word-break:break-all}
.hint{color:#64748b;font-size:14px;line-height:1.7}
</style>
</head>
<body><div class="wrap">
<h1>OneSync Relay 管理</h1>
{{if .Message}}<div class="msg">{{.Message}}</div>{{end}}
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
{{if not .Configured}}
<div class="card"><h2>首次设置管理账号</h2>
<form method="post" action="/setup">
<label>账号</label><input name="username" value="admin" maxlength="64" required>
<label>密码</label><input name="password" type="password" minlength="8" required>
<button type="submit">设置并登录</button>
</form></div>
{{else if not .Authenticated}}
<div class="card"><h2>登录</h2>
<form method="post" action="/login">
<label>账号</label><input name="username" value="admin" required>
<label>密码</label><input name="password" type="password" required>
<button type="submit">登录</button>
</form></div>
{{else}}
<div class="card"><form method="post" action="/logout"><button class="secondary" type="submit">退出登录</button></form></div>
<div class="card"><h2>Relay 状态</h2>
<div class="kv">
<b>Relay 监听</b><span>{{.RelayListen}}</span>
<b>令牌文件</b><span>{{.TokenFile}}</span>
<b>证书路径</b><span>{{.CertPath}}</span>
<b>私钥路径</b><span>{{.KeyPath}}</span>
</div></div>
<div class="card"><h2>Relay 访问令牌</h2>
<p class="hint">轮换后，新连接会立即使用新令牌；已经生成的旧同步链接会失效，需要源端重新生成链接。</p>
<pre>{{.Token}}</pre>
<form method="post" action="/rotate-token"><button type="submit">轮换 Relay 令牌</button></form>
</div>
<div class="card"><h2>证书信息</h2>
{{if .CertError}}<div class="err">{{.CertError}}</div>{{else}}
<div class="kv">
<b>主题</b><span>{{.CertSubject}}</span>
<b>颁发者</b><span>{{.CertIssuer}}</span>
<b>生效时间</b><span>{{.CertNotBefore}}</span>
<b>过期时间</b><span>{{.CertNotAfter}}</span>
<b>域名</b><span>{{.CertDNSNames}}</span>
<b>IP</b><span>{{.CertIPs}}</span>
</div>{{end}}
</div>
<div class="card"><h2>设置证书路径</h2>
<p class="hint">适合宝塔 / 1Panel：先在面板里申请和续期证书，然后把 fullchain 和 privkey 路径填到这里。新建 TLS 连接会读取新路径。</p>
<form method="post" action="/set-cert">
<label>证书文件 fullchain.pem</label><input name="cert_path" value="{{.CertPath}}" required>
<label>私钥文件 privkey.pem</label><input name="key_path" value="{{.KeyPath}}" required>
<button type="submit">保存证书路径</button>
</form></div>
<div class="card"><h2>粘贴证书文本</h2>
<p class="hint">适合从宝塔 / 1Panel / 证书服务商复制出来的证书内容。左边粘贴私钥 KEY，右边粘贴证书 PEM。保存前会校验证书和私钥是否匹配。</p>
<form method="post" action="/paste-cert">
<div class="row">
<div><label>私钥 KEY</label><textarea name="key_pem" placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----" required></textarea></div>
<div><label>证书 PEM 格式</label><textarea name="cert_pem" placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----" required></textarea></div>
</div>
<button type="submit">保存并启用证书</button>
</form></div>
{{end}}
</div></body></html>`))
