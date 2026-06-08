package auth

import (
	"errors"
	"testing"
	"time"
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
