package scanner

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// FileEntry describes one regular file relative to a synchronization root.
type FileEntry struct {
	Path    string
	Size    int64
	ModTime int64
	Mode    uint32
	Hash    string
}

// Snapshot contains the regular files found under a synchronization root.
type Snapshot struct {
	RootID      string
	GeneratedAt int64
	Files       map[string]FileEntry
}

// Scanner creates filesystem snapshots.
type Scanner interface {
	Scan(ctx context.Context, root string) (Snapshot, error)
}

// Options controls optional work performed during a scan.
type Options struct {
	ComputeHash bool
	IgnoreRules []string
}

type filesystemScanner struct {
	computeHash bool
	ignoreRules []ignoreRule
}

const reservedTransferDirectory = ".onesync-part"

// New returns a filesystem scanner configured with options.
func New(options Options) Scanner {
	return &filesystemScanner{
		computeHash: options.ComputeHash,
		ignoreRules: parseIgnoreRules(options.IgnoreRules),
	}
}

func (s *filesystemScanner) Scan(ctx context.Context, root string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(root) == "" {
		return Snapshot{}, errors.New("scan root is empty")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve scan root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)

	info, err := os.Lstat(absoluteRoot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat scan root: %w", err)
	}
	if !info.IsDir() {
		return Snapshot{}, fmt.Errorf("scan root %q is not a directory", root)
	}

	snapshot := Snapshot{
		RootID:      rootID(absoluteRoot),
		GeneratedAt: time.Now().UnixNano(),
		Files:       make(map[string]FileEntry),
	}

	err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("walk %q: %w", path, walkErr)
		}
		if path == absoluteRoot || entry.IsDir() {
			if path != absoluteRoot && entry.IsDir() {
				relativePath, err := filepath.Rel(absoluteRoot, path)
				if err != nil {
					return fmt.Errorf("make %q relative to scan root: %w", path, err)
				}
				relativePath = filepath.ToSlash(relativePath)
				if s.ignored(relativePath, true) {
					return filepath.SkipDir
				}
			}
			if path != absoluteRoot && entry.IsDir() && entry.Name() == reservedTransferDirectory {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		fileInfo, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("inspect %q: %w", path, err)
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}

		relativePath, err := filepath.Rel(absoluteRoot, path)
		if err != nil {
			return fmt.Errorf("make %q relative to scan root: %w", path, err)
		}
		relativePath = filepath.ToSlash(relativePath)
		if relativePath == "." || relativePath == "" || strings.HasPrefix(relativePath, "../") {
			return fmt.Errorf("invalid relative path %q", relativePath)
		}
		if s.ignored(relativePath, false) {
			return nil
		}

		fileHash := ""
		if s.computeHash {
			fileHash, err = hashFile(ctx, path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return fmt.Errorf("hash %q: %w", path, err)
			}
		}

		snapshot.Files[relativePath] = FileEntry{
			Path:    relativePath,
			Size:    fileInfo.Size(),
			ModTime: fileInfo.ModTime().UnixNano(),
			Mode:    uint32(fileInfo.Mode()),
			Hash:    fileHash,
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}

	return snapshot, nil
}

type ignoreRule struct {
	pattern string
	dirOnly bool
}

func parseIgnoreRules(rules []string) []ignoreRule {
	parsed := make([]ignoreRule, 0, len(rules))
	for _, raw := range rules {
		rule := strings.TrimSpace(raw)
		if rule == "" || strings.HasPrefix(rule, "#") {
			continue
		}
		rule = filepath.ToSlash(rule)
		rule = strings.TrimPrefix(rule, "/")
		dirOnly := strings.HasSuffix(rule, "/")
		rule = strings.TrimSuffix(rule, "/")
		if rule == "" {
			continue
		}
		parsed = append(parsed, ignoreRule{pattern: rule, dirOnly: dirOnly})
	}
	return parsed
}

func (s *filesystemScanner) ignored(relativePath string, isDir bool) bool {
	for _, rule := range s.ignoreRules {
		if rule.dirOnly && !isDir && !strings.HasPrefix(relativePath, rule.pattern+"/") {
			continue
		}
		if relativePath == rule.pattern || strings.HasPrefix(relativePath, rule.pattern+"/") {
			return true
		}
		if !strings.Contains(rule.pattern, "/") {
			matched, err := path.Match(rule.pattern, path.Base(relativePath))
			if err == nil && matched {
				return true
			}
		}
		matched, err := path.Match(rule.pattern, relativePath)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func rootID(absoluteRoot string) string {
	sum := sha256.Sum256([]byte(absoluteRoot))
	return hex.EncodeToString(sum[:])
}

func hashFile(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		count, readErr := file.Read(buffer)
		if count > 0 {
			if _, err := hash.Write(buffer[:count]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
