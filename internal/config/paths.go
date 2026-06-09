package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths contains the local filesystem locations used by one OneSync instance.
type Paths struct {
	DataDir                 string
	TaskStore               string
	CredentialDir           string
	WebAuthStore            string
	LogFile                 string
	AutomaticCertPath       string
	AutomaticPrivateKeyPath string
}

// DefaultDataDir returns the platform default OneSync data directory.
func DefaultDataDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "OneSync"), nil
}

// NewPaths validates a data directory and returns the standard local paths.
func NewPaths(dataDir string) (Paths, error) {
	if strings.TrimSpace(dataDir) == "" {
		return Paths{}, fmt.Errorf("data directory is required")
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve data directory: %w", err)
	}
	if strings.ContainsRune(absolute, '\x00') {
		return Paths{}, fmt.Errorf("data directory contains null byte")
	}
	return Paths{
		DataDir:                 absolute,
		TaskStore:               filepath.Join(absolute, "tasks.json"),
		CredentialDir:           filepath.Join(absolute, "credentials"),
		WebAuthStore:            filepath.Join(absolute, "web-auth.json"),
		LogFile:                 filepath.Join(absolute, "logs", "onesync.log"),
		AutomaticCertPath:       filepath.Join(absolute, "certs", "source.crt"),
		AutomaticPrivateKeyPath: filepath.Join(absolute, "certs", "source.key"),
	}, nil
}
