package task

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/202121000995/OneSync/internal/progress"
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
	ID          string             `json:"id"`
	Role        string             `json:"role"`
	SourcePath  string             `json:"source_path,omitempty"`
	TargetPath  string             `json:"target_path,omitempty"`
	PeerAddress string             `json:"peer_address,omitempty"`
	RelayURL    string             `json:"relay_url,omitempty"`
	State       string             `json:"state"`
	LastError   string             `json:"last_error,omitempty"`
	Progress    *progress.Snapshot `json:"progress,omitempty"`
	IgnoreRules []string           `json:"ignore_rules,omitempty"`
	Traffic     TrafficStats       `json:"traffic,omitempty"`
	Size        SizeStats          `json:"size,omitempty"`
	Devices     DeviceStats        `json:"devices,omitempty"`
	Logs        []LogEntry         `json:"logs,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// TrafficStats stores cumulative task traffic counters.
type TrafficStats struct {
	ReceivedBytes uint64 `json:"received_bytes,omitempty"`
	SentBytes     uint64 `json:"sent_bytes,omitempty"`
}

// SizeStats stores the latest folder size view for one task.
type SizeStats struct {
	LocalBytes    uint64 `json:"local_bytes,omitempty"`
	StandardBytes uint64 `json:"standard_bytes,omitempty"`
	LocalFiles    uint64 `json:"local_files,omitempty"`
	StandardFiles uint64 `json:"standard_files,omitempty"`
}

// DeviceStats stores the latest known peer and connection view for one task.
type DeviceStats struct {
	Connected     uint64    `json:"connected,omitempty"`
	Total         uint64    `json:"total,omitempty"`
	PeerID        string    `json:"peer_id,omitempty"`
	Endpoint      string    `json:"endpoint,omitempty"`
	RelayEndpoint string    `json:"relay_endpoint,omitempty"`
	Connection    string    `json:"connection,omitempty"`
	TLS           string    `json:"tls,omitempty"`
	ClientVersion string    `json:"client_version,omitempty"`
	LastSeen      time.Time `json:"last_seen,omitempty"`
}

// LogEntry records a task lifecycle event for the management page.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
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
	if task.Progress != nil {
		if err := progress.Validate(*task.Progress); err != nil {
			return err
		}
	}
	if err := validateIgnoreRules(task.IgnoreRules); err != nil {
		return err
	}
	if err := validateLogs(task.Logs); err != nil {
		return err
	}
	if err := validateDeviceStats(task.Devices); err != nil {
		return err
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

func validateIgnoreRules(rules []string) error {
	if len(rules) > 1000 {
		return errors.New("ignore rules exceed 1000 lines")
	}
	for _, rule := range rules {
		if len(rule) > 512 {
			return errors.New("ignore rule exceeds 512 characters")
		}
		if strings.ContainsRune(rule, '\x00') {
			return errors.New("ignore rule contains null byte")
		}
	}
	return nil
}

func validateLogs(logs []LogEntry) error {
	if len(logs) > 200 {
		return errors.New("task logs exceed 200 entries")
	}
	for _, entry := range logs {
		if entry.Level != "" && entry.Level != "info" && entry.Level != "error" && entry.Level != "warning" {
			return fmt.Errorf("invalid task log level %q", entry.Level)
		}
		if len(entry.Message) > 2048 {
			return errors.New("task log message exceeds 2048 characters")
		}
		if strings.ContainsRune(entry.Message, '\x00') {
			return errors.New("task log message contains null byte")
		}
	}
	return nil
}

func validateDeviceStats(devices DeviceStats) error {
	for _, value := range []string{devices.PeerID, devices.Endpoint, devices.RelayEndpoint, devices.Connection, devices.TLS, devices.ClientVersion} {
		if len(value) > 2048 {
			return errors.New("device detail exceeds 2048 characters")
		}
		if strings.ContainsRune(value, '\x00') {
			return errors.New("device detail contains null byte")
		}
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
