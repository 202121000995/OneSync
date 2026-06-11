package main

import (
	"path/filepath"
	"testing"
)

func TestAccessKeyStoreCreateDisableDelete(t *testing.T) {
	store := newAccessKeyStore(filepath.Join(t.TempDir(), "relay.keys.json"))

	key, err := store.create("客户A")
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	if key.Name != "客户A" || key.Token == "" || !key.Enabled {
		t.Fatalf("created key = %+v", key)
	}
	if tokens := store.enabledTokens(); len(tokens) != 1 || tokens[0] != key.Token {
		t.Fatalf("enabledTokens() = %v, want created token", tokens)
	}

	if err := store.setEnabled(key.ID, false); err != nil {
		t.Fatalf("setEnabled(false) error = %v", err)
	}
	if tokens := store.enabledTokens(); len(tokens) != 0 {
		t.Fatalf("enabledTokens() after disable = %v, want none", tokens)
	}

	if err := store.delete(key.ID); err != nil {
		t.Fatalf("delete() error = %v", err)
	}
	keys, err := store.load()
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("keys after delete = %+v, want none", keys)
	}
}
