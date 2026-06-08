package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
