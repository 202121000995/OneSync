package auth

import (
	"encoding/base64"
	"path/filepath"
	"testing"
)

func TestCredentialStoreRoundTrip(t *testing.T) {
	store, err := NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	tokenBytes := make([]byte, linkTokenBytes)
	if _, err := fixedRandom(tokenBytes); err != nil {
		t.Fatalf("fixedRandom() error = %v", err)
	}
	want := Credential{
		SessionID: "session",
		Endpoint:  "sync.example:443",
		Token:     base64.RawURLEncoding.EncodeToString(tokenBytes),
	}
	if err := store.Save("task", want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Load("task")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestCredentialStoreConsumesOneTimeTokenPersistently(t *testing.T) {
	store, err := NewCredentialStore(filepath.Join(t.TempDir(), "credentials"))
	if err != nil {
		t.Fatalf("NewCredentialStore() error = %v", err)
	}
	tokenBytes := make([]byte, linkTokenBytes)
	_, _ = fixedRandom(tokenBytes)
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	credential := Credential{
		SessionID: "session", Endpoint: "sync.example:443", Token: token, OneTime: true,
	}
	if err := store.Save("task", credential); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	peerID, err := NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	if _, err := store.Claim("task", token, peerID); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	reloaded, err := NewCredentialStore(store.directory)
	if err != nil {
		t.Fatalf("NewCredentialStore(reload) error = %v", err)
	}
	if _, err := reloaded.Claim("task", token, peerID); err != nil {
		t.Fatalf("Claim() rejected the bound peer after reload: %v", err)
	}
	otherPeerID, err := NewPeerID()
	if err != nil {
		t.Fatalf("NewPeerID() error = %v", err)
	}
	if _, err := reloaded.Claim("task", token, otherPeerID); err == nil {
		t.Fatal("Claim() accepted a different peer after reload")
	}
}
