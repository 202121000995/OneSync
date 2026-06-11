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

func TestLoadAccessTokenFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.token")
	if err := os.WriteFile(path, []byte(" relay-secret \n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	token, err := loadAccessToken("", path)
	if err != nil {
		t.Fatalf("loadAccessToken() error = %v", err)
	}
	if token != "relay-secret" {
		t.Fatalf("token = %q, want relay-secret", token)
	}
	if _, err := loadAccessToken("one", path); err == nil {
		t.Fatal("loadAccessToken() accepted both value and file")
	}
}

func TestDefaultAccessKeysFile(t *testing.T) {
	tests := []struct {
		name                string
		certificatePathFile string
		accessTokenFile     string
		want                string
	}{
		{
			name:                "uses cert path directory first",
			certificatePathFile: filepath.Join("etc", "onesync", "relay.cert-paths"),
			accessTokenFile:     filepath.Join("other", "relay.token"),
			want:                filepath.Join("etc", "onesync", "relay.keys.json"),
		},
		{
			name:            "falls back to token directory",
			accessTokenFile: filepath.Join("etc", "onesync", "relay.token"),
			want:            filepath.Join("etc", "onesync", "relay.keys.json"),
		},
		{
			name: "empty when no persistent paths",
			want: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := defaultAccessKeysFile(test.certificatePathFile, test.accessTokenFile)
			if got != test.want {
				t.Fatalf("defaultAccessKeysFile() = %q, want %q", got, test.want)
			}
		})
	}
}
