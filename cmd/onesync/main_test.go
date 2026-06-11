package main

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/certutil"
	"github.com/202121000995/OneSync/internal/config"
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

func TestDefaultDataDirFlagValueDoesNotBlockServiceStartup(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := defaultDataDirFlagValue(); got == "" {
		t.Fatal("defaultDataDirFlagValue returned an empty fallback")
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

func TestLoadOrCreateServerTLSGeneratesAutomaticCertificate(t *testing.T) {
	root := t.TempDir()
	paths, err := config.NewPaths(root)
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	config, err := loadOrCreateServerTLS("", "", paths, 7443)
	if err != nil {
		t.Fatalf("loadOrCreateServerTLS() error = %v", err)
	}
	if config == nil || len(config.Certificates) != 1 {
		t.Fatalf("config = %+v, want one certificate", config)
	}
	certPath := filepath.Join(root, "certs", "source.crt")
	keyPath := filepath.Join(root, "certs", "source.key")
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("source certificate was not written: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("source private key was not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key permissions = %o, want 0600", info.Mode().Perm())
	}
	certificate := parseServerCertificate(t, config)
	verifyHost(t, certificate, "localhost")
	verifyHost(t, certificate, "127.0.0.1")
}

func TestAutomaticCertificateReadyRequiresCurrentHosts(t *testing.T) {
	root := t.TempDir()
	certPath := filepath.Join(root, "source.crt")
	keyPath := filepath.Join(root, "source.key")
	now := time.Now().UTC()
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{"127.0.0.1"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: 48 * time.Hour,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if !automaticCertificateReady(certPath, keyPath, []string{"127.0.0.1"}, now) {
		t.Fatal("automaticCertificateReady() rejected matching certificate")
	}
	if automaticCertificateReady(certPath, keyPath, []string{"127.0.0.1", "192.0.2.36"}, now) {
		t.Fatal("automaticCertificateReady() accepted certificate missing current host")
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

func parseServerCertificate(t *testing.T, config *tls.Config) *x509.Certificate {
	t.Helper()
	certificate, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return certificate
}

func verifyHost(t *testing.T, certificate *x509.Certificate, host string) {
	t.Helper()
	if err := certificate.VerifyHostname(host); err != nil {
		t.Fatalf("certificate does not verify for %s: %v", host, err)
	}
}
