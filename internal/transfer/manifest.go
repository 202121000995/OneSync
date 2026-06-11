package transfer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

// Block describes one content-addressed file block.
type Block struct {
	Index  int
	Offset int64
	Size   int
	Hash   [hashSize]byte
}

// Manifest describes a file as fixed-size content blocks.
type Manifest struct {
	Size      int64
	BlockSize int
	FileHash  [hashSize]byte
	Blocks    []Block
}

// BuildManifest scans one regular file into a whole-file hash plus fixed-size block hashes.
// It is the compatibility-safe foundation for future missing-block requests.
func BuildManifest(ctx context.Context, path string, blockSize int) (Manifest, error) {
	if blockSize == 0 {
		blockSize = MaxChunkSize
	}
	if blockSize < 1 || blockSize > MaxChunkSize {
		return Manifest{}, fmt.Errorf("block size must be between 1 and %d", MaxChunkSize)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, errors.New("manifest path is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	manifest := Manifest{
		Size:      info.Size(),
		BlockSize: blockSize,
		Blocks:    make([]Block, 0, (info.Size()+int64(blockSize)-1)/int64(blockSize)),
	}
	whole := sha256.New()
	buffer := make([]byte, blockSize)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			data := buffer[:count]
			_, _ = whole.Write(data)
			manifest.Blocks = append(manifest.Blocks, Block{
				Index:  len(manifest.Blocks),
				Offset: offset,
				Size:   count,
				Hash:   sha256.Sum256(data),
			})
			offset += int64(count)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return Manifest{}, fmt.Errorf("read file: %w", readErr)
		}
	}
	copy(manifest.FileHash[:], whole.Sum(nil))
	return manifest, nil
}

// MissingBlocks compares a source manifest with the blocks already present on a target side.
func MissingBlocks(source Manifest, present map[int][hashSize]byte) []Block {
	missing := make([]Block, 0)
	for _, block := range source.Blocks {
		hash, ok := present[block.Index]
		if !ok || hash != block.Hash {
			missing = append(missing, block)
		}
	}
	return missing
}
