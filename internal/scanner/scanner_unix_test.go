//go:build unix

package scanner

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

func TestScanIgnoresNamedPipes(t *testing.T) {
	root := t.TempDir()
	pipePath := filepath.Join(root, "scanner.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}

	snapshot, err := New(Options{}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if _, exists := snapshot.Files["scanner.pipe"]; exists {
		t.Fatal("Scan() included a named pipe")
	}
}
