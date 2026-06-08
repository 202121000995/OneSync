package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/202121000995/OneSync/internal/platform"
	"github.com/202121000995/OneSync/internal/relay"
)

func main() {
	address := flag.String("listen", ":7443", "Relay TLS listen address")
	certificatePath := flag.String("cert", "", "TLS certificate file")
	privateKeyPath := flag.String("key", "", "TLS private key file")
	waitTimeout := flag.Duration("wait-timeout", relay.DefaultWaitTimeout, "peer pairing timeout")
	idleTimeout := flag.Duration("idle-timeout", relay.DefaultIdleTimeout, "stream idle timeout")
	maxWaiting := flag.Int("max-waiting", relay.DefaultMaxWaiting, "maximum waiting Relay sessions")
	maxActive := flag.Int("max-active", relay.DefaultMaxActive, "maximum active Relay sessions")
	maxBytes := flag.Int64("max-bytes", relay.DefaultMaxBytes, "maximum bytes per direction and session")
	logPath := flag.String("log-file", "", "optional log file path")
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
	certificate, err := tls.LoadX509KeyPair(*certificatePath, *privateKeyPath)
	if err != nil {
		log.Fatalf("load Relay TLS certificate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	broker, err := relay.NewBroker(relay.Config{
		WaitTimeout: *waitTimeout,
		IdleTimeout: *idleTimeout,
		MaxWaiting:  *maxWaiting,
		MaxActive:   *maxActive,
		MaxBytes:    *maxBytes,
		Logger:      logger,
	})
	if err != nil {
		log.Fatal(err)
	}
	server, err := relay.Listen(*address, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS13,
	}, broker)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := platform.NotifyShutdown(context.Background())
	defer stop()
	log.Printf("OneSync Relay listening on %s", server.Addr())
	if err := server.Serve(ctx); err != nil {
		log.Fatal(err)
	}
}

func configureLogging(logPath string) (io.Writer, func() error, error) {
	if logPath == "" {
		return os.Stdout, nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	log.SetOutput(file)
	return file, file.Close, nil
}
