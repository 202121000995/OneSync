package sync

import (
	"reflect"
	"testing"

	"github.com/202121000995/OneSync/internal/scanner"
)

func TestCompareCreatesSourceOnlyFiles(t *testing.T) {
	source := snapshot(
		entry("nested/new.txt", 10, 100, 0o644, "source-hash"),
	)

	operations, err := NewDiffer().Compare(source, scanner.Snapshot{})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}

	want := []Operation{{
		Type:  OperationCreate,
		Entry: source.Files["nested/new.txt"],
	}}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("Compare() = %+v, want %+v", operations, want)
	}
}

func TestCompareUpdatesDifferentFilesFromSource(t *testing.T) {
	source := snapshot(entry("same.txt", 10, 100, 0o644, "source-hash"))
	target := snapshot(entry("same.txt", 10, 100, 0o644, "target-hash"))

	operations, err := NewDiffer().Compare(source, target)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}

	want := []Operation{{
		Type:  OperationUpdate,
		Entry: source.Files["same.txt"],
	}}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("Compare() = %+v, want %+v", operations, want)
	}
}

func TestComparePreservesTargetOnlyFiles(t *testing.T) {
	target := snapshot(entry("target-only.txt", 10, 100, 0o644, "hash"))

	operations, err := NewDiffer().Compare(scanner.Snapshot{}, target)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("Compare() returned target-only operations: %+v", operations)
	}
}

func TestCompareIgnoresIdenticalFiles(t *testing.T) {
	source := snapshot(entry("same.txt", 10, 100, 0o644, "hash"))
	target := snapshot(entry("same.txt", 10, 200, 0o644, "hash"))

	operations, err := NewDiffer().Compare(source, target)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("Compare() returned identical-file operations: %+v", operations)
	}
}

func TestCompareFallsBackToMetadataWithoutBothHashes(t *testing.T) {
	tests := []struct {
		name   string
		source scanner.FileEntry
		target scanner.FileEntry
		update bool
	}{
		{
			name:   "identical metadata",
			source: entry("file.txt", 10, 100, 0o644, ""),
			target: entry("file.txt", 10, 100, 0o644, "target-hash"),
		},
		{
			name:   "different size",
			source: entry("file.txt", 11, 100, 0o644, ""),
			target: entry("file.txt", 10, 100, 0o644, ""),
			update: true,
		},
		{
			name:   "different modification time",
			source: entry("file.txt", 10, 101, 0o644, ""),
			target: entry("file.txt", 10, 100, 0o644, ""),
			update: true,
		},
		{
			name:   "different mode does not change content",
			source: entry("file.txt", 10, 100, 0o600, ""),
			target: entry("file.txt", 10, 100, 0o644, ""),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operations, err := NewDiffer().Compare(
				snapshot(test.source),
				snapshot(test.target),
			)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if got := len(operations) == 1; got != test.update {
				t.Fatalf("Compare() update = %v, want %v; operations = %+v", got, test.update, operations)
			}
		})
	}
}

func TestCompareSortsOperationsByPath(t *testing.T) {
	source := snapshot(
		entry("z.txt", 1, 1, 0o644, ""),
		entry("a.txt", 1, 1, 0o644, ""),
		entry("nested/b.txt", 1, 1, 0o644, ""),
	)

	operations, err := NewDiffer().Compare(source, scanner.Snapshot{})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(operations) != 3 {
		t.Fatalf("Compare() returned %d operations, want 3", len(operations))
	}

	got := []string{
		operations[0].Entry.Path,
		operations[1].Entry.Path,
		operations[2].Entry.Path,
	}
	want := []string{"a.txt", "nested/b.txt", "z.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Compare() paths = %v, want %v", got, want)
	}
}

func TestCompareRejectsInvalidSnapshotPaths(t *testing.T) {
	tests := []scanner.Snapshot{
		{Files: map[string]scanner.FileEntry{"": {Path: ""}}},
		{Files: map[string]scanner.FileEntry{"../escape.txt": {Path: "../escape.txt"}}},
		{Files: map[string]scanner.FileEntry{"/absolute.txt": {Path: "/absolute.txt"}}},
		{Files: map[string]scanner.FileEntry{"a\\b.txt": {Path: "a\\b.txt"}}},
		{Files: map[string]scanner.FileEntry{"C:/drive.txt": {Path: "C:/drive.txt"}}},
		{Files: map[string]scanner.FileEntry{"a.txt": {Path: "b.txt"}}},
	}

	for _, source := range tests {
		if _, err := NewDiffer().Compare(source, scanner.Snapshot{}); err == nil {
			t.Fatalf("Compare() accepted invalid snapshot: %+v", source)
		}
	}
}

func snapshot(entries ...scanner.FileEntry) scanner.Snapshot {
	files := make(map[string]scanner.FileEntry, len(entries))
	for _, entry := range entries {
		files[entry.Path] = entry
	}
	return scanner.Snapshot{Files: files}
}

func entry(filePath string, size, modTime int64, mode uint32, hash string) scanner.FileEntry {
	return scanner.FileEntry{
		Path:    filePath,
		Size:    size,
		ModTime: modTime,
		Mode:    mode,
		Hash:    hash,
	}
}
