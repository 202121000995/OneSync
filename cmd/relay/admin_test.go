package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/certutil"
)

func TestLoadCertificateInfoFromPEMAcceptsMatchingPair(t *testing.T) {
	certPEM, keyPEM := testRelayCertificatePair(t, "relay.example.com")

	certificate, err := loadCertificateInfoFromPEM(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("loadCertificateInfoFromPEM() error = %v", err)
	}
	if len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != "relay.example.com" {
		t.Fatalf("DNSNames = %v, want relay.example.com", certificate.DNSNames)
	}
}

func TestLoadCertificateInfoFromPEMRejectsMismatchedPair(t *testing.T) {
	certPEM, _ := testRelayCertificatePair(t, "relay.example.com")
	_, keyPEM := testRelayCertificatePair(t, "other.example.com")

	if _, err := loadCertificateInfoFromPEM(certPEM, keyPEM); err == nil {
		t.Fatal("loadCertificateInfoFromPEM() accepted a mismatched certificate and key")
	}
}

func TestAdminManagedCertPathsUseConfigDirectory(t *testing.T) {
	server := &adminServer{config: adminConfig{
		CertPathFile: filepath.Join("/etc/onesync", "relay.cert-paths"),
		DefaultCert:  "/custom/fullchain.pem",
		DefaultKey:   "/custom/privkey.pem",
	}}

	certPath, keyPath := server.managedCertPaths()
	if certPath != "/etc/onesync/relay.crt" || keyPath != "/etc/onesync/relay.key" {
		t.Fatalf("managedCertPaths() = %q, %q", certPath, keyPath)
	}
}

func TestPortHelpers(t *testing.T) {
	tests := map[string]string{
		":17443":          "17443",
		"0.0.0.0:8766":    "8766",
		"127.0.0.1:18766": "18766",
		"8766":            "8766",
	}
	for listen, want := range tests {
		if got := portFromListen(listen); got != want {
			t.Fatalf("portFromListen(%q) = %q, want %q", listen, got, want)
		}
	}
	for _, value := range []string{"1", "443", "65535"} {
		if err := validatePort(value); err != nil {
			t.Fatalf("validatePort(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"", "0", "65536", "abc"} {
		if err := validatePort(value); err == nil {
			t.Fatalf("validatePort(%q) accepted invalid port", value)
		}
	}
}

func testRelayCertificatePair(t *testing.T, host string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "relay.crt")
	keyPath := filepath.Join(dir, "relay.key")
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{host},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: 24 * time.Hour,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", certPath, err)
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", keyPath, err)
	}
	return string(certPEM), string(keyPEM)
}
