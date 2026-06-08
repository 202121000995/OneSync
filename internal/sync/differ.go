package sync

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/202121000995/OneSync/internal/scanner"
)

// OperationType describes how the target should apply a source entry.
type OperationType string

const (
	OperationCreate OperationType = "create"
	OperationUpdate OperationType = "update"
)

// Operation is one source-authoritative change for the target.
type Operation struct {
	Type  OperationType
	Entry scanner.FileEntry
}

// Differ compares source and target snapshots.
type Differ interface {
	Compare(source scanner.Snapshot, target scanner.Snapshot) ([]Operation, error)
}

type snapshotDiffer struct{}

// NewDiffer returns a deterministic source-authoritative snapshot differ.
func NewDiffer() Differ {
	return snapshotDiffer{}
}

func (snapshotDiffer) Compare(source scanner.Snapshot, target scanner.Snapshot) ([]Operation, error) {
	if err := validateSnapshot("source", source); err != nil {
		return nil, err
	}
	if err := validateSnapshot("target", target); err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(source.Files))
	for filePath := range source.Files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)

	operations := make([]Operation, 0, len(paths))
	for _, filePath := range paths {
		sourceEntry := source.Files[filePath]
		targetEntry, exists := target.Files[filePath]
		if !exists {
			operations = append(operations, Operation{
				Type:  OperationCreate,
				Entry: sourceEntry,
			})
			continue
		}
		if entriesDiffer(sourceEntry, targetEntry) {
			operations = append(operations, Operation{
				Type:  OperationUpdate,
				Entry: sourceEntry,
			})
		}
	}

	return operations, nil
}

func entriesDiffer(source, target scanner.FileEntry) bool {
	if source.Hash != "" && target.Hash != "" {
		return source.Hash != target.Hash
	}
	return source.Size != target.Size ||
		source.ModTime != target.ModTime
}

func validateSnapshot(name string, snapshot scanner.Snapshot) error {
	for filePath, entry := range snapshot.Files {
		if err := validatePath(filePath); err != nil {
			return fmt.Errorf("%s snapshot path %q: %w", name, filePath, err)
		}
		if entry.Path != filePath {
			return fmt.Errorf(
				"%s snapshot entry path %q does not match map key %q",
				name,
				entry.Path,
				filePath,
			)
		}
	}
	return nil
}

func validatePath(filePath string) error {
	if filePath == "" {
		return errors.New("path is empty")
	}
	if strings.Contains(filePath, "\\") {
		return errors.New("path must use slash separators")
	}
	if strings.ContainsAny(filePath, ":\x00") {
		return errors.New("path contains a cross-platform unsafe character")
	}
	if strings.HasPrefix(filePath, "/") {
		return errors.New("path is absolute")
	}
	if cleaned := path.Clean(filePath); cleaned != filePath || cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") {
		return errors.New("path is not a normalized relative path")
	}
	return nil
}
