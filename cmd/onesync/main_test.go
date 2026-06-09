package main

import (
	"crypto/tls"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/certutil"
)

func TestConfigureLoggingWritesPrivateFile(t *testing.T) {
	originalWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(originalWriter) })
	logPath := filepath.Join(t.TempDir(), "nested", "onesync.log")
	file, err := configureLogging(logPath)
	if err != nil {
		t.Fatalf("configureLogging() error = %v", err)
	}
	defer file.Close()
	log.Print("service started")
	if err := file.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "service started") {
		t.Fatalf("log file = %q", data)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestServerCertificateHosts(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "source.crt")
	keyPath := filepath.Join(root, "source.key")
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{"192.168.1.10,source.local"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: 24 * time.Hour,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	config, err := loadServerTLS(certPath, keyPath)
	if err != nil {
		t.Fatalf("loadServerTLS() error = %v", err)
	}
	hosts := serverCertificateHosts(config)
	if strings.Join(hosts, ",") != "192.168.1.10,source.local" {
		t.Fatalf("serverCertificateHosts() = %v", hosts)
	}
}

func TestServerCertificateHostsHandlesMissingCertificate(t *testing.T) {
	if hosts := serverCertificateHosts(nil); hosts != nil {
		t.Fatalf("serverCertificateHosts(nil) = %v", hosts)
	}
	if hosts := serverCertificateHosts(&tls.Config{}); hosts != nil {
		t.Fatalf("serverCertificateHosts(empty) = %v", hosts)
	}
}
