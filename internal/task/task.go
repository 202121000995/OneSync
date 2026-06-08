package task

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	RoleSource = "source"
	RoleTarget = "target"

	StateCreated    = "created"
	StateConnecting = "connecting"
	StateSyncing    = "syncing"
	StateIdle       = "idle"
	StateFailed     = "failed"
	StateStopped    = "stopped"

	MaxTasks = 10_000
)

// Task contains persistent synchronization task state.
type Task struct {
	ID          string    `json:"id"`
	Role        string    `json:"role"`
	SourcePath  string    `json:"source_path,omitempty"`
	TargetPath  string    `json:"target_path,omitempty"`
	PeerAddress string    `json:"peer_address,omitempty"`
	RelayURL    string    `json:"relay_url,omitempty"`
	State       string    `json:"state"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func validateTask(task Task) error {
	if strings.TrimSpace(task.ID) == "" {
		return errors.New("task ID is required")
	}
	if len(task.ID) > 128 {
		return errors.New("task ID exceeds 128 characters")
	}
	if strings.ContainsAny(task.ID, `/\`+"\x00") {
		return errors.New("task ID contains an unsafe character")
	}
	if task.Role != RoleSource && task.Role != RoleTarget {
		return errors.New("task role must be source or target")
	}
	if len(task.PeerAddress) > 2048 || len(task.RelayURL) > 2048 {
		return errors.New("task endpoint exceeds 2048 characters")
	}
	if task.Role == RoleSource && strings.TrimSpace(task.SourcePath) == "" {
		return errors.New("source task requires source path")
	}
	if task.Role == RoleTarget && strings.TrimSpace(task.TargetPath) == "" {
		return errors.New("target task requires target path")
	}
	rootPath := task.SourcePath
	if task.Role == RoleTarget {
		rootPath = task.TargetPath
	}
	if strings.ContainsRune(rootPath, '\x00') || !filepath.IsAbs(rootPath) {
		return errors.New("task root must be an absolute path without null bytes")
	}
	if len(rootPath) > 32768 {
		return errors.New("task root exceeds 32768 characters")
	}
	if task.State != "" && !validState(task.State) {
		return fmt.Errorf("invalid task state %q", task.State)
	}
	return nil
}

func validatePersistedTask(task Task) error {
	if err := validateTask(task); err != nil {
		return err
	}
	if !validState(task.State) {
		return errors.New("persisted task state is required")
	}
	if task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.UpdatedAt.Before(task.CreatedAt) {
		return errors.New("persisted task timestamps are invalid")
	}
	return nil
}

func validState(state string) bool {
	switch state {
	case StateCreated, StateConnecting, StateSyncing, StateIdle, StateFailed, StateStopped:
		return true
	default:
		return false
	}
}
