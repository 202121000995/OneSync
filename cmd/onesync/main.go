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
	"os/signal"
	"path/filepath"
	"time"

	"github.com/202121000995/OneSync/backend"
	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/client"
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
	flag.Parse()

	serverTLS, err := loadServerTLS(*certificatePath, *privateKeyPath)
	if err != nil {
		log.Fatal(err)
	}
	clientTLS, err := loadClientTLS(*caPath)
	if err != nil {
		log.Fatal(err)
	}
	credentials, err := auth.NewCredentialStore(filepath.Join(*dataDir, "credentials"))
	if err != nil {
		log.Fatal(err)
	}
	runnerFactory, err := client.NewFactory(client.Config{
		Credentials: credentials,
		ServerTLS:   serverTLS,
		ClientTLS:   clientTLS,
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
	server, err := backend.NewServer(manager, auth.NewLinkService(), credentials)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
