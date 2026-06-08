package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRejectsUnknownFieldsAndVersion(t *testing.T) {
	tests := []string{
		`{"version":1,"tasks":[],"unknown":true}`,
		`{"version":2,"tasks":[]}`,
		`{"version":1,"tasks":[]} {}`,
		`{"version":1,"tasks":[{"id":"task","role":"source","source_path":"/tmp","state":""}]}`,
	}
	for index, content := range tests {
		path := filepath.Join(t.TempDir(), "tasks.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		taskStore, err := newStore(path)
		if err != nil {
			t.Fatalf("newStore() error = %v", err)
		}
		if _, err := taskStore.load(); err == nil {
			t.Fatalf("case %d: load() accepted invalid store", index)
		}
	}
}

func TestStoreWritesVersionedSortedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	taskStore, err := newStore(path)
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	now := time.Now().UTC()
	if err := taskStore.save(map[string]Task{
		"z": {ID: "z", Role: RoleSource, SourcePath: "/source", State: StateCreated, CreatedAt: now, UpdatedAt: now},
		"a": {ID: "a", Role: RoleTarget, TargetPath: "/target", State: StateIdle, CreatedAt: now, UpdatedAt: now},
	}); err != nil {
		t.Fatalf("save() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"version": 1`) {
		t.Fatalf("store = %s, want version", content)
	}
	if strings.Index(content, `"id": "a"`) > strings.Index(content, `"id": "z"`) {
		t.Fatalf("store tasks are not sorted: %s", content)
	}
}

func TestStoreRejectsSymbolicLink(t *testing.T) {
	if testing.Short() {
		t.Skip("symbolic link test")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte(`{"version":1,"tasks":[]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	link := filepath.Join(root, "tasks.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Symlink() unavailable: %v", err)
	}
	taskStore, err := newStore(link)
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	if _, err := taskStore.load(); err == nil {
		t.Fatal("load() accepted a symbolic link store")
	}
}
