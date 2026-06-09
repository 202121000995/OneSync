package scanner

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestScanCollectsRegularFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "alpha.txt"), "alpha")
	writeFile(t, filepath.Join(root, "nested", "beta.txt"), "beta")

	snapshot, err := New(Options{}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if snapshot.RootID == "" {
		t.Fatal("Scan() returned an empty RootID")
	}
	if snapshot.GeneratedAt <= 0 {
		t.Fatalf("Scan() GeneratedAt = %d, want a positive value", snapshot.GeneratedAt)
	}
	if len(snapshot.Files) != 2 {
		t.Fatalf("Scan() found %d files, want 2", len(snapshot.Files))
	}

	alpha := snapshot.Files["alpha.txt"]
	if alpha.Path != "alpha.txt" || alpha.Size != 5 || alpha.Hash != "" {
		t.Fatalf("alpha entry = %+v", alpha)
	}

	beta := snapshot.Files["nested/beta.txt"]
	if beta.Path != "nested/beta.txt" || beta.Size != 4 {
		t.Fatalf("beta entry = %+v", beta)
	}
	if filepath.Separator == '\\' && beta.Path != "nested/beta.txt" {
		t.Fatalf("Scan() path = %q, want slash-separated path", beta.Path)
	}
}

func TestScanComputesMD5WhenEnabled(t *testing.T) {
	root := t.TempDir()
	const content = "hash me"
	writeFile(t, filepath.Join(root, "file.txt"), content)

	snapshot, err := New(Options{ComputeHash: true}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	sum := md5.Sum([]byte(content))
	want := hex.EncodeToString(sum[:])
	if got := snapshot.Files["file.txt"].Hash; got != want {
		t.Fatalf("Scan() hash = %q, want %q", got, want)
	}
}

func TestScanAppliesIgnoreRules(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.txt"), "keep")
	writeFile(t, filepath.Join(root, "skip.tmp"), "skip")
	writeFile(t, filepath.Join(root, "cache", "nested.txt"), "skip")
	writeFile(t, filepath.Join(root, "logs", "debug.txt"), "skip")

	snapshot, err := New(Options{IgnoreRules: []string{"*.tmp", "cache/", "logs/debug.txt"}}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if _, exists := snapshot.Files["keep.txt"]; !exists {
		t.Fatal("keep.txt was not scanned")
	}
	for _, ignored := range []string{"skip.tmp", "cache/nested.txt", "logs/debug.txt"} {
		if _, exists := snapshot.Files[ignored]; exists {
			t.Fatalf("%s was not ignored", ignored)
		}
	}
}

func TestScanRootIDIsStableAndDoesNotExposeRoot(t *testing.T) {
	root := t.TempDir()
	scanner := New(Options{})

	first, err := scanner.Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("first Scan() error = %v", err)
	}
	second, err := scanner.Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("second Scan() error = %v", err)
	}

	if first.RootID != second.RootID {
		t.Fatalf("RootID changed: %q != %q", first.RootID, second.RootID)
	}
	if first.RootID == root {
		t.Fatal("RootID exposes the absolute root path")
	}
}

func TestScanIgnoresSymbolicLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation commonly requires elevated Windows privileges")
	}

	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	writeFile(t, target, "target")
	if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	snapshot, err := New(Options{}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	if _, exists := snapshot.Files["link.txt"]; exists {
		t.Fatal("Scan() included a symbolic link")
	}
	if _, exists := snapshot.Files["target.txt"]; !exists {
		t.Fatal("Scan() omitted the regular link target")
	}
}

func TestScanIgnoresReservedTransferDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".onesync-part", "partial.bin"), "partial")
	writeFile(t, filepath.Join(root, "visible.txt"), "visible")

	snapshot, err := New(Options{}).Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if _, exists := snapshot.Files[".onesync-part/partial.bin"]; exists {
		t.Fatal("Scan() included a reserved transfer file")
	}
	if _, exists := snapshot.Files["visible.txt"]; !exists {
		t.Fatal("Scan() omitted a visible file")
	}
}

func TestScanRejectsSymbolicLinkRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation commonly requires elevated Windows privileges")
	}

	target := t.TempDir()
	root := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(target, root); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	_, err := New(Options{}).Scan(context.Background(), root)
	if err == nil {
		t.Fatal("Scan() error = nil, want symbolic link root error")
	}
}

func TestScanRejectsNonDirectoryRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	writeFile(t, file, "content")

	_, err := New(Options{}).Scan(context.Background(), file)
	if err == nil {
		t.Fatal("Scan() error = nil, want non-directory error")
	}
}

func TestScanRejectsEmptyRoot(t *testing.T) {
	_, err := New(Options{}).Scan(context.Background(), "")
	if err == nil {
		t.Fatal("Scan() error = nil, want empty root error")
	}
}

func TestScanReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(Options{}).Scan(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan() error = %v, want context.Canceled", err)
	}
}

func TestScanMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")

	_, err := New(Options{}).Scan(context.Background(), root)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Scan() error = %v, want os.ErrNotExist", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
