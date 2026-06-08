package sync

import (
	"encoding/binary"
	"testing"

	"github.com/202121000995/OneSync/internal/scanner"
)

func TestSnapshotRoundTrip(t *testing.T) {
	want := scanner.Snapshot{
		RootID:      "root",
		GeneratedAt: 123,
		Files: map[string]scanner.FileEntry{
			"file.txt": {Path: "file.txt", Size: 4, Hash: "hash"},
		},
	}
	payload, err := encodeSnapshot(want)
	if err != nil {
		t.Fatalf("encodeSnapshot() error = %v", err)
	}
	got, err := decodeSnapshot(payload)
	if err != nil {
		t.Fatalf("decodeSnapshot() error = %v", err)
	}
	if got.Files["file.txt"] != want.Files["file.txt"] {
		t.Fatalf("decodeSnapshot() entry = %+v, want %+v", got.Files["file.txt"], want.Files["file.txt"])
	}
}

func TestDecodeSnapshotRejectsUnsafePath(t *testing.T) {
	payload := []byte(`{"Files":{"../escape":{"Path":"../escape"}}}`)
	if _, err := decodeSnapshot(payload); err == nil {
		t.Fatal("decodeSnapshot() accepted unsafe path")
	}
}

func TestDecodeSnapshotRejectsUnknownFields(t *testing.T) {
	if _, err := decodeSnapshot([]byte(`{"Files":{},"Unknown":true}`)); err == nil {
		t.Fatal("decodeSnapshot() accepted an unknown field")
	}
}

func TestDecodePlanRejectsTooManyOperations(t *testing.T) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, MaxOperations+1)
	if _, err := decodePlan(payload); err == nil {
		t.Fatal("decodePlan() accepted too many operations")
	}
}
