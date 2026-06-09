package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/certutil"
)

func TestLinkIssueParseAndOneTimeRedeem(t *testing.T) {
	service := NewLinkService()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.random = fixedRandom

	encoded, err := service.Issue("session-1", "sync.example:443", "relay.example:443")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	link, err := service.Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if link.ExpiresAt != now.Add(24*time.Hour) {
		t.Fatalf("ExpiresAt = %v, want %v", link.ExpiresAt, now.Add(24*time.Hour))
	}
	if link.IssuedAt != now {
		t.Fatalf("IssuedAt = %v, want %v", link.IssuedAt, now)
	}
	if _, err := service.Redeem(encoded); err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if _, err := service.Redeem(encoded); err == nil {
		t.Fatal("Redeem() accepted a used link")
	}
}

func TestLinkExpiresAfter24Hours(t *testing.T) {
	service := NewLinkService()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.random = fixedRandom
	encoded, err := service.Issue("session-1", "sync.example:443", "")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	service.now = func() time.Time { return now.Add(24 * time.Hour) }
	if _, err := service.Redeem(encoded); err == nil {
		t.Fatal("Redeem() accepted an expired link")
	}
}

func TestLinkCarriesSourceCertificate(t *testing.T) {
	service := NewLinkService()
	service.random = fixedRandom
	certificatePEM := testCertificatePEM(t)

	encoded, err := service.IssueWithCertificate("session-1", "192.168.1.10:7443", "", certificatePEM)
	if err != nil {
		t.Fatalf("IssueWithCertificate() error = %v", err)
	}
	link, err := service.Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if link.CACertificatePEM != certificatePEM {
		t.Fatal("parsed link did not preserve source certificate")
	}
}

func TestLinkRejectsInvalidCertificate(t *testing.T) {
	service := NewLinkService()
	service.random = fixedRandom
	if _, err := service.IssueWithCertificate("session-1", "192.168.1.10:7443", "", "not a certificate"); err == nil {
		t.Fatal("IssueWithCertificate() accepted invalid certificate")
	}
}

func TestLinkRejectsMalformedAndUnsafeMetadata(t *testing.T) {
	service := NewLinkService()
	if _, err := service.Issue("../session", "endpoint", ""); err == nil {
		t.Fatal("Issue() accepted an unsafe session ID")
	}
	if _, err := service.Parse("not-base64!"); err == nil {
		t.Fatal("Parse() accepted malformed input")
	}
}

func TestLinkRandomFailure(t *testing.T) {
	service := NewLinkService()
	service.random = func([]byte) (int, error) {
		return 0, errors.New("random unavailable")
	}
	if _, err := service.Issue("session", "endpoint", ""); err == nil {
		t.Fatal("Issue() error = nil, want random failure")
	}
}

func fixedRandom(data []byte) (int, error) {
	for index := range data {
		data[index] = byte(index + 1)
	}
	return len(data), nil
}

func testCertificatePEM(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	certPath := filepath.Join(root, "source.crt")
	keyPath := filepath.Join(root, "source.key")
	if err := certutil.Generate(certutil.Options{
		Hosts:    []string{"192.168.1.10"},
		CertPath: certPath,
		KeyPath:  keyPath,
		Validity: time.Hour,
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return string(data)
}
