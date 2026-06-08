package certutil

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCreatesCertificateAndPrivateKey(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "certs", "onesync.crt")
	keyPath := filepath.Join(t.TempDir(), "certs", "onesync.key")
	now := time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)
	if err := Generate(Options{
		Hosts:    []string{"127.0.0.1,localhost"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: 24 * time.Hour,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	certificate := readCertificate(t, certPath)
	if len(certificate.IPAddresses) != 1 || certificate.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatalf("IPAddresses = %v", certificate.IPAddresses)
	}
	if len(certificate.DNSNames) != 1 || certificate.DNSNames[0] != "localhost" {
		t.Fatalf("DNSNames = %v", certificate.DNSNames)
	}
	if !certificate.NotAfter.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("NotAfter = %s", certificate.NotAfter)
	}
	if !certificate.IsCA {
		t.Fatal("certificate is not usable as a local trust anchor")
	}
	verifyGeneratedCertificate(t, certificate, "127.0.0.1", now)
	verifyGeneratedCertificate(t, certificate, "localhost", now)
	assertPermission(t, keyPath, 0o600)
	assertPermission(t, certPath, 0o644)
}

func TestGenerateRejectsInvalidInput(t *testing.T) {
	if err := Generate(Options{Hosts: []string{" "}, CertPath: "cert", KeyPath: "key"}); err == nil {
		t.Fatal("Generate() accepted an empty host")
	}
	if err := Generate(Options{Hosts: []string{"example.com/path"}, CertPath: "cert", KeyPath: "key"}); err == nil {
		t.Fatal("Generate() accepted an unsafe host")
	}
	if err := Generate(Options{Hosts: []string{"localhost"}, CertPath: "", KeyPath: "key"}); err == nil {
		t.Fatal("Generate() accepted an empty certificate path")
	}
}

func readCertificate(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("certificate PEM did not decode")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return certificate
}

func assertPermission(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permission = %o, want %o", path, got, want)
	}
}

func verifyGeneratedCertificate(t *testing.T, certificate *x509.Certificate, dnsName string, now time.Time) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	if _, err := certificate.Verify(x509.VerifyOptions{
		DNSName:     dnsName,
		Roots:       roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Fatalf("certificate did not verify for %s: %v", dnsName, err)
	}
}
