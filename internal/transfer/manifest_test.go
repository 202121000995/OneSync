package transfer

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildManifestSplitsFileIntoHashedBlocks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.bin")
	content := []byte("abcdefghijkl")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	manifest, err := BuildManifest(context.Background(), path, 5)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	if manifest.Size != int64(len(content)) || manifest.BlockSize != 5 || len(manifest.Blocks) != 3 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.FileHash != sha256.Sum256(content) {
		t.Fatal("file hash mismatch")
	}
	if manifest.Blocks[0].Offset != 0 || manifest.Blocks[1].Offset != 5 || manifest.Blocks[2].Offset != 10 {
		t.Fatalf("block offsets = %+v", manifest.Blocks)
	}
	if manifest.Blocks[2].Size != 2 {
		t.Fatalf("last block size = %d, want 2", manifest.Blocks[2].Size)
	}
}

func TestMissingBlocksReturnsOnlyAbsentOrDifferentBlocks(t *testing.T) {
	source := Manifest{
		Blocks: []Block{
			{Index: 0, Hash: sha256.Sum256([]byte("a"))},
			{Index: 1, Hash: sha256.Sum256([]byte("b"))},
			{Index: 2, Hash: sha256.Sum256([]byte("c"))},
		},
	}
	present := map[int][hashSize]byte{
		0: source.Blocks[0].Hash,
		1: sha256.Sum256([]byte("different")),
	}

	missing := MissingBlocks(source, present)
	if len(missing) != 2 || missing[0].Index != 1 || missing[1].Index != 2 {
		t.Fatalf("missing = %+v", missing)
	}
}
