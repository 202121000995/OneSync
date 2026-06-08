package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const storeVersion = 1
const maxStoreSize = 64 << 20

type storeFile struct {
	Version int    `json:"version"`
	Tasks   []Task `json:"tasks"`
}

type store struct {
	path string
}

func newStore(path string) (*store, error) {
	if path == "" {
		return nil, errors.New("task store path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve task store path: %w", err)
	}
	return &store{path: absolutePath}, nil
}

func (s *store) load() (map[string]Task, error) {
	info, err := os.Lstat(s.path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("task store path is a symbolic link or non-regular file")
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect task store: %w", err)
	}
	if err == nil && info.Size() > maxStoreSize {
		return nil, fmt.Errorf("task store size %d exceeds limit %d", info.Size(), maxStoreSize)
	}

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return make(map[string]Task), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read task store: %w", err)
	}

	var file storeFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode task store: %w", err)
	}
	if err := ensureStoreJSONEnd(decoder); err != nil {
		return nil, fmt.Errorf("decode task store: %w", err)
	}
	if file.Version != storeVersion {
		return nil, fmt.Errorf("unsupported task store version %d", file.Version)
	}
	if len(file.Tasks) > MaxTasks {
		return nil, fmt.Errorf("task store contains %d tasks, limit is %d", len(file.Tasks), MaxTasks)
	}

	tasks := make(map[string]Task, len(file.Tasks))
	for _, task := range file.Tasks {
		if err := validatePersistedTask(task); err != nil {
			return nil, fmt.Errorf("validate persisted task %q: %w", task.ID, err)
		}
		if _, exists := tasks[task.ID]; exists {
			return nil, fmt.Errorf("duplicate persisted task ID %q", task.ID)
		}
		tasks[task.ID] = task
	}
	return tasks, nil
}

func (s *store) save(tasks map[string]Task) error {
	if len(tasks) > MaxTasks {
		return fmt.Errorf("task count %d exceeds limit %d", len(tasks), MaxTasks)
	}
	ordered := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		ordered = append(ordered, task)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].ID < ordered[j].ID
	})
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Tasks: ordered}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task store: %w", err)
	}
	data = append(data, '\n')

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create task store directory: %w", err)
	}
	tempFile, err := os.CreateTemp(directory, ".onesync-tasks-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary task store: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if err := tempFile.Chmod(0o600); err != nil {
		tempFile.Close()
		return fmt.Errorf("secure temporary task store: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return fmt.Errorf("write temporary task store: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return fmt.Errorf("sync temporary task store: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary task store: %w", err)
	}
	if err := replaceStoreFile(tempPath, s.path); err != nil {
		return fmt.Errorf("replace task store: %w", err)
	}
	if err := syncStoreDirectory(directory); err != nil {
		return fmt.Errorf("sync task store directory: %w", err)
	}
	return nil
}

func ensureStoreJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("task store contains multiple JSON values")
}
