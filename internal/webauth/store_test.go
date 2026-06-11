package webauth

import (
	"path/filepath"
	"testing"
)

func TestChangePassword(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := store.Setup("admin", "old-password"); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if err := store.ChangePassword("admin", "wrong-password", "new-password"); err == nil {
		t.Fatal("ChangePassword() accepted the wrong current password")
	}
	if err := store.ChangePassword("admin", "old-password", "new-password"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if store.Verify("admin", "old-password") {
		t.Fatal("Verify() accepted old password after password change")
	}
	if !store.Verify("admin", "new-password") {
		t.Fatal("Verify() rejected new password after password change")
	}
	if store.Username() != "admin" {
		t.Fatalf("Username() = %q, want admin", store.Username())
	}
}
