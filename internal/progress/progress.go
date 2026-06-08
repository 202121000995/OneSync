package progress

import (
	"errors"
	"strings"
)

const maxCurrentPathLength = 32768

// Snapshot describes coarse file-level progress for one synchronization cycle.
type Snapshot struct {
	TotalFiles     int    `json:"total_files"`
	CompletedFiles int    `json:"completed_files"`
	CurrentPath    string `json:"current_path,omitempty"`
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
	if strings.ContainsRune(snapshot.CurrentPath, '\x00') {
		return errors.New("progress current path contains null byte")
	}
	if len(snapshot.CurrentPath) > maxCurrentPathLength {
		return errors.New("progress current path is too long")
	}
	return nil
}
