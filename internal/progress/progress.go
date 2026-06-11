package progress

import (
	"errors"
	"strings"
)

const maxCurrentPathLength = 32768

const (
	StageConnecting = "connecting"
	StageScanning   = "scanning"
	StagePlanning   = "planning"
	StageTransfer   = "transfer"
	StageComplete   = "complete"
	StageWaiting    = "waiting"
)

// Snapshot describes coarse file-level progress for one synchronization cycle.
type Snapshot struct {
	TotalFiles        int    `json:"total_files"`
	CompletedFiles    int    `json:"completed_files"`
	Stage             string `json:"stage,omitempty"`
	CurrentPath       string `json:"current_path,omitempty"`
	CurrentBytes      uint64 `json:"current_bytes,omitempty"`
	CurrentTotalBytes uint64 `json:"current_total_bytes,omitempty"`
}

// Validate checks progress values before they are persisted or reported.
func Validate(snapshot Snapshot) error {
	if snapshot.TotalFiles < 0 {
		return errors.New("progress total files cannot be negative")
	}
	if snapshot.CompletedFiles < 0 {
		return errors.New("progress completed files cannot be negative")
	}
	if snapshot.CompletedFiles > snapshot.TotalFiles {
		return errors.New("progress completed files exceeds total files")
	}
	if !validStage(snapshot.Stage) {
		return errors.New("progress stage is invalid")
	}
	if strings.ContainsRune(snapshot.CurrentPath, '\x00') {
		return errors.New("progress current path contains null byte")
	}
	if len(snapshot.CurrentPath) > maxCurrentPathLength {
		return errors.New("progress current path is too long")
	}
	if snapshot.CurrentBytes > snapshot.CurrentTotalBytes && snapshot.CurrentTotalBytes > 0 {
		return errors.New("progress current bytes exceeds current total bytes")
	}
	return nil
}

func validStage(stage string) bool {
	switch stage {
	case "", StageConnecting, StageScanning, StagePlanning, StageTransfer, StageComplete, StageWaiting:
		return true
	default:
		return false
	}
}
