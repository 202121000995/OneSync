package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/202121000995/OneSync/internal/logger"
	"github.com/202121000995/OneSync/internal/platform"
	"github.com/202121000995/OneSync/internal/relay"
)

func main() {
	address := flag.String("listen", ":7443", "Relay TLS listen address")
	certificatePath := flag.String("cert", "", "TLS certificate file")
	privateKeyPath := flag.String("key", "", "TLS private key file")
	certificatePathFile := flag.String("cert-path-file", "", "optional file whose first two lines are certificate and private key paths")
	waitTimeout := flag.Duration("wait-timeout", relay.DefaultWaitTimeout, "peer pairing timeout")
	idleTimeout := flag.Duration("idle-timeout", relay.DefaultIdleTimeout, "stream idle timeout")
	maxWaiting := flag.Int("max-waiting", relay.DefaultMaxWaiting, "maximum waiting Relay sessions")
	maxActive := flag.Int("max-active", relay.DefaultMaxActive, "maximum active Relay sessions")
	maxBytes := flag.Int64("max-bytes", relay.DefaultMaxBytes, "maximum bytes per direction and session")
	accessToken := flag.String("access-token", "", "optional Relay access token")
	accessTokenFile := flag.String("access-token-file", "", "optional file containing the Relay access token")
	logPath := flag.String("log-file", "", "optional log file path")
	adminListen := flag.String("admin-listen", "", "optional Relay admin web listen address")
	adminAuthPath := flag.String("admin-auth-file", "", "optional Relay admin account file")
	flag.Parse()

	logWriter, closeLog, err := configureLogging(*logPath)
	if err != nil {
		log.Fatal(err)
	}
	if closeLog != nil {
		defer closeLog()
	}
	if *certificatePath == "" || *privateKeyPath == "" {
		log.Fatal("-cert and -key are required")
	}
	relayAccessToken, err := loadAccessToken(*accessToken, *accessTokenFile)
	if err != nil {
		log.Fatal(err)
	}
	certificateProvider := newCertificateProvider(*certificatePath, *privateKeyPath, *certificatePathFile)
	certificate, err := certificateProvider.GetCertificate(nil)
	if err != nil {
		log.Fatalf("load Relay TLS certificate: %v", err)
	}
	_ = certificate
	broker, err := relay.NewBroker(relay.Config{
		WaitTimeout:         *waitTimeout,
		IdleTimeout:         *idleTimeout,
		MaxWaiting:          *maxWaiting,
		MaxActive:           *maxActive,
		MaxBytes:            *maxBytes,
		AccessToken:         relayAccessToken,
		AccessTokenProvider: accessTokenProvider(*accessTokenFile),
		Logger:              logger.NewText(logWriter),
	})
	if err != nil {
		log.Fatal(err)
	}
	server, err := relay.Listen(*address, &tls.Config{
		GetCertificate: certificateProvider.GetCertificate,
		MinVersion:     tls.VersionTLS13,
	}, broker)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := platform.NotifyShutdown(context.Background())
	defer stop()
	if *adminListen != "" {
		authPath := strings.TrimSpace(*adminAuthPath)
		if authPath == "" {
			authPath = "relay-admin-auth.json"
		}
		if err := startAdminServer(ctx, adminConfig{
			Listen:       *adminListen,
			AuthPath:     authPath,
			TokenFile:    *accessTokenFile,
			CertPathFile: *certificatePathFile,
			DefaultCert:  *certificatePath,
			DefaultKey:   *privateKeyPath,
			RelayListen:  *address,
		}); err != nil {
			log.Fatal(err)
		}
	}
	if relayAccessToken != "" {
		log.Printf("OneSync Relay listening on %s with access token enabled", server.Addr())
	} else {
		log.Printf("OneSync Relay listening on %s without access token", server.Addr())
	}
	if err := server.Serve(ctx); err != nil {
		log.Fatal(err)
	}
}

func loadAccessToken(value, path string) (string, error) {
	value = strings.TrimSpace(value)
	path = strings.TrimSpace(path)
	if value != "" && path != "" {
		return "", errors.New("use either -access-token or -access-token-file, not both")
	}
	if path == "" {
		return value, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("Relay access token file is empty")
	}
	return token, nil
}

func configureLogging(logPath string) (io.Writer, func() error, error) {
	if logPath == "" {
		return os.Stdout, nil, nil
	}
	file, err := logger.OpenPrivateLog(logPath)
	if err != nil {
		return nil, nil, err
	}
	return file, file.Close, nil
}

type certificateProvider struct {
	defaultCert string
	defaultKey  string
	pathFile    string
	mu          sync.Mutex
	cachedCert  string
	cachedKey   string
	cachedMTime time.Time
	cachedPair  *tls.Certificate
	cachedErr   error
}

func newCertificateProvider(certPath, keyPath, pathFile string) *certificateProvider {
	return &certificateProvider{
		defaultCert: strings.TrimSpace(certPath),
		defaultKey:  strings.TrimSpace(keyPath),
		pathFile:    strings.TrimSpace(pathFile),
	}
}

func (p *certificateProvider) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certPath, keyPath := p.currentPaths()
	if certPath == "" || keyPath == "" {
		return nil, errors.New("Relay TLS certificate and private key paths are required")
	}
	mtime, err := latestModTime(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cachedPair != nil && p.cachedCert == certPath && p.cachedKey == keyPath && p.cachedMTime.Equal(mtime) {
		return p.cachedPair, p.cachedErr
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		p.cachedPair = nil
		p.cachedErr = fmt.Errorf("load Relay TLS certificate: %w", err)
		return nil, p.cachedErr
	}
	p.cachedCert = certPath
	p.cachedKey = keyPath
	p.cachedMTime = mtime
	p.cachedPair = &pair
	p.cachedErr = nil
	return &pair, nil
}

func (p *certificateProvider) currentPaths() (string, string) {
	if p.pathFile != "" {
		data, err := os.ReadFile(p.pathFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 && strings.TrimSpace(lines[0]) != "" && strings.TrimSpace(lines[1]) != "" {
				return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
			}
		}
	}
	return p.defaultCert, p.defaultKey
}

func latestModTime(paths ...string) (time.Time, error) {
	var latest time.Time
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest, nil
}

func accessTokenProvider(path string) func() string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return func() string {
		token, err := loadAccessToken("", path)
		if err != nil {
			return ""
		}
		return token
	}
}
