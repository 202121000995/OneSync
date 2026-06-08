package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/202121000995/OneSync/backend"
	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/client"
	"github.com/202121000995/OneSync/internal/diagnostic"
	"github.com/202121000995/OneSync/internal/platform"
	"github.com/202121000995/OneSync/internal/task"
)

func main() {
	defaultDataDir, err := dataDirectory()
	if err != nil {
		log.Fatal(err)
	}
	port := flag.Int("port", 8765, "local management port")
	dataDir := flag.String("data-dir", defaultDataDir, "OneSync data directory")
	certificatePath := flag.String("cert", "", "TLS certificate file for source tasks")
	privateKeyPath := flag.String("key", "", "TLS private key file for source tasks")
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
	serverTLS, err := loadServerTLS(*certificatePath, *privateKeyPath)
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
	credentials, err := auth.NewCredentialStore(filepath.Join(*dataDir, "credentials"))
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
		filepath.Join(*dataDir, "tasks.json"),
		runnerFactory,
	)
	if err != nil {
		log.Fatal(err)
	}
	server, err := backend.NewServerWithOptions(manager, auth.NewLinkService(), credentials, backend.Options{
		ConnectionTester: connectionTester,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := platform.NotifyShutdown(context.Background())
	defer stop()
	managementURL := fmt.Sprintf("http://127.0.0.1:%d", *port)
	log.Printf("OneSync management page: %s", managementURL)
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := platform.OpenBrowser(managementURL); err != nil {
			log.Printf("Open management page manually: %v", err)
		}
	}()
	if err := server.ListenAndServe(ctx, *port); err != nil {
		log.Fatal(err)
	}
}

func configureLogging(logPath string) (*os.File, error) {
	if logPath == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	log.SetOutput(file)
	return file, nil
}

func loadServerTLS(certificatePath, privateKeyPath string) (*tls.Config, error) {
	if certificatePath == "" && privateKeyPath == "" {
		return nil, nil
	}
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

func dataDirectory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "OneSync"), nil
}
