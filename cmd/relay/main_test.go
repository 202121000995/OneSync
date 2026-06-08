package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelayConfigureLoggingWritesPrivateFile(t *testing.T) {
	originalWriter := log.Writer()
	t.Cleanup(func() { log.SetOutput(originalWriter) })
	logPath := filepath.Join(t.TempDir(), "nested", "relay.log")
	writer, closeLog, err := configureLogging(logPath)
	if err != nil {
		t.Fatalf("configureLogging() error = %v", err)
	}
	defer closeLog()
	if _, err := writer.Write([]byte("relay started\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "relay started") {
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
