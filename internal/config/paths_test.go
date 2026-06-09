package config

import (
	"path/filepath"
	"testing"
)

func TestNewPathsBuildsStandardLocations(t *testing.T) {
	root := t.TempDir()
	paths, err := NewPaths(root)
	if err != nil {
		t.Fatalf("NewPaths() error = %v", err)
	}
	if paths.DataDir != filepath.Clean(root) {
		t.Fatalf("DataDir = %q, want %q", paths.DataDir, filepath.Clean(root))
	}
	if paths.TaskStore != filepath.Join(root, "tasks.json") {
		t.Fatalf("TaskStore = %q", paths.TaskStore)
	}
	if paths.CredentialDir != filepath.Join(root, "credentials") {
		t.Fatalf("CredentialDir = %q", paths.CredentialDir)
	}
	if paths.WebAuthStore != filepath.Join(root, "web-auth.json") {
		t.Fatalf("WebAuthStore = %q", paths.WebAuthStore)
	}
	if paths.AutomaticCertPath != filepath.Join(root, "certs", "source.crt") {
		t.Fatalf("AutomaticCertPath = %q", paths.AutomaticCertPath)
	}
	if paths.AutomaticPrivateKeyPath != filepath.Join(root, "certs", "source.key") {
		t.Fatalf("AutomaticPrivateKeyPath = %q", paths.AutomaticPrivateKeyPath)
	}
}
