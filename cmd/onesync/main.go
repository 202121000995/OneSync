package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/202121000995/OneSync/backend"
	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/certutil"
	"github.com/202121000995/OneSync/internal/client"
	"github.com/202121000995/OneSync/internal/config"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/logger"
	"github.com/202121000995/OneSync/internal/netutil"
	"github.com/202121000995/OneSync/internal/platform"
	"github.com/202121000995/OneSync/internal/task"
	"github.com/202121000995/OneSync/internal/webauth"
)

var version = "dev"

func main() {
	defaultDataDir, err := config.DefaultDataDir()
	if err != nil {
		log.Fatal(err)
	}
	port := flag.Int("port", 8765, "local management port")
	webBind := flag.String("web-bind", "127.0.0.1", "management web bind address")
	webAuthEnabled := flag.Bool("web-auth", false, "require a management account for the web UI")
	syncPort := flag.Int("sync-port", backend.DefaultSyncPort, "default TLS synchronization port suggested by the management page")
	dataDir := flag.String("data-dir", defaultDataDir, "OneSync data directory")
	certificatePath := flag.String("cert", "", "optional custom TLS certificate file for source tasks")
	privateKeyPath := flag.String("key", "", "optional custom TLS private key file for source tasks")
	caPath := flag.String("ca", "", "optional trusted CA certificate file")
	logPath := flag.String("log-file", "", "optional log file path")
	syncInterval := flag.Duration("sync-interval", client.DefaultSyncInterval, "time between completed synchronization cycles")
	flag.Parse()

	logFile, err := configureLogging(*logPath)
	if err != nil {
		log.Fatal(err)
	}
	if logFile != nil {
		defer logFile.Close()
	}
	paths, err := config.NewPaths(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	serverTLS, err := loadOrCreateServerTLS(*certificatePath, *privateKeyPath, paths, *syncPort)
	if err != nil {
		log.Fatal(err)
	}
	clientTLS, err := loadClientTLS(*caPath)
	if err != nil {
		log.Fatal(err)
	}
	connectionTester, err := diagnostic.NewChecker(clientTLS, diagnostic.DefaultTimeout)
	if err != nil {
		log.Fatal(err)
	}
	credentials, err := auth.NewCredentialStore(paths.CredentialDir)
	if err != nil {
		log.Fatal(err)
	}
	runnerFactory, err := client.NewFactory(client.Config{
		Credentials:  credentials,
		ServerTLS:    serverTLS,
		ClientTLS:    clientTLS,
		SyncInterval: *syncInterval,
	})
	if err != nil {
		log.Fatal(err)
	}
	manager, err := task.NewManager(
		paths.TaskStore,
		runnerFactory,
	)
	if err != nil {
		log.Fatal(err)
	}
	webAuthStore, err := webAuthStore(paths, *webBind, *webAuthEnabled)
	if err != nil {
		log.Fatal(err)
	}
	server, err := backend.NewServerWithOptions(manager, auth.NewLinkService(), credentials, backend.Options{
		ConnectionTester:     connectionTester,
		WebAuth:              webAuthStore,
		SyncPort:             *syncPort,
		ManagementBind:       *webBind,
		ManagementPort:       *port,
		DataDir:              *dataDir,
		SyncInterval:         *syncInterval,
		DirectTLSConfigured:  serverTLS != nil,
		DirectTLSHosts:       serverCertificateHosts(serverTLS),
		DirectTLSCertificate: serverCertificatePEM(serverTLS),
		Version:              version,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := platform.NotifyShutdown(context.Background())
	defer stop()
	managementURL := fmt.Sprintf("http://127.0.0.1:%d", *port)
	log.Printf("OneSync management page: %s", managementURL)
	if err := platform.StartTray(ctx, managementURL, stop); err != nil {
		log.Printf("Start tray icon: %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := platform.OpenBrowser(managementURL); err != nil {
			log.Printf("Open management page manually: %v", err)
		}
	}()
	if err := server.ListenAndServeOn(ctx, *webBind, *port); err != nil {
		log.Fatal(err)
	}
}

func webAuthStore(paths config.Paths, webBind string, enabled bool) (*webauth.Store, error) {
	if enabled || webBind != "127.0.0.1" {
		return webauth.NewStore(paths.WebAuthStore)
	}
	return nil, nil
}

func configureLogging(logPath string) (*os.File, error) {
	return logger.OpenPrivateLog(logPath)
}

func loadServerTLS(certificatePath, privateKeyPath string) (*tls.Config, error) {
	if certificatePath == "" || privateKeyPath == "" {
		return nil, errors.New("-cert and -key must be provided together")
	}
	certificate, err := tls.LoadX509KeyPair(certificatePath, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func loadOrCreateServerTLS(certificatePath, privateKeyPath string, paths config.Paths, syncPort int) (*tls.Config, error) {
	if certificatePath != "" || privateKeyPath != "" {
		return loadServerTLS(certificatePath, privateKeyPath)
	}
	hosts, err := automaticCertificateHosts(syncPort)
	if err != nil {
		return nil, err
	}
	certificatePath = paths.AutomaticCertPath
	privateKeyPath = paths.AutomaticPrivateKeyPath
	if !automaticCertificateReady(certificatePath, privateKeyPath, hosts, time.Now()) {
		if err := certutil.Generate(certutil.Options{
			Hosts:    hosts,
			CertPath: certificatePath,
			KeyPath:  privateKeyPath,
			Validity: 10 * 365 * 24 * time.Hour,
		}); err != nil {
			return nil, fmt.Errorf("create automatic source TLS certificate: %w", err)
		}
		log.Printf("Generated automatic source TLS certificate for: %s", strings.Join(hosts, ", "))
	}
	return loadServerTLS(certificatePath, privateKeyPath)
}

func automaticCertificateHosts(syncPort int) ([]string, error) {
	suggestions, err := netutil.LocalEndpointSuggestions(syncPort)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
	}
	for _, suggestion := range suggestions {
		host, _, err := net.SplitHostPort(suggestion)
		if err != nil {
			continue
		}
		host = strings.Trim(host, "[]")
		if host != "" {
			seen[host] = struct{}{}
		}
	}
	hosts := make([]string, 0, len(seen))
	for host := range seen {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts, nil
}

func automaticCertificateReady(certificatePath, privateKeyPath string, hosts []string, now time.Time) bool {
	config, err := loadServerTLS(certificatePath, privateKeyPath)
	if err != nil || config == nil || len(config.Certificates) == 0 || len(config.Certificates[0].Certificate) == 0 {
		return false
	}
	certificate, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		return false
	}
	if !certificate.NotAfter.After(now.Add(24 * time.Hour)) {
		return false
	}
	for _, host := range hosts {
		if err := certificate.VerifyHostname(host); err != nil {
			return false
		}
	}
	return true
}

func loadClientTLS(caPath string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS13}
	if caPath == "" {
		return config, nil
	}
	data, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(data) {
		return nil, errors.New("CA certificate file contains no certificates")
	}
	config.RootCAs = roots
	return config, nil
}

func serverCertificateHosts(config *tls.Config) []string {
	if config == nil || len(config.Certificates) == 0 || len(config.Certificates[0].Certificate) == 0 {
		return nil
	}
	certificate, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		return nil
	}
	hosts := make([]string, 0, len(certificate.IPAddresses)+len(certificate.DNSNames))
	for _, ip := range certificate.IPAddresses {
		hosts = append(hosts, ip.String())
	}
	hosts = append(hosts, certificate.DNSNames...)
	return hosts
}

func serverCertificatePEM(config *tls.Config) string {
	if config == nil || len(config.Certificates) == 0 || len(config.Certificates[0].Certificate) == 0 {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: config.Certificates[0].Certificate[0]}))
}
