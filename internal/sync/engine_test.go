package sync

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/progress"
)

func TestEngineSynchronizesCreateAndUpdateAndPreservesTargetOnly(t *testing.T) {
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	writeEngineFile(t, filepath.Join(sourceRoot, "new.txt"), []byte("new"))
	writeEngineFile(t, filepath.Join(sourceRoot, "nested", "shared.txt"), []byte("source"))
	writeEngineFile(t, filepath.Join(targetRoot, "nested", "shared.txt"), []byte("target"))
	writeEngineFile(t, filepath.Join(targetRoot, "target-only.txt"), []byte("keep"))
	writeEngineFile(t, filepath.Join(targetRoot, ".onesync-part", "ignored.part"), []byte("partial"))

	runEnginePair(t, sourceRoot, targetRoot, "task-1")

	assertEngineFile(t, filepath.Join(targetRoot, "new.txt"), []byte("new"))
	assertEngineFile(t, filepath.Join(targetRoot, "nested", "shared.txt"), []byte("source"))
	assertEngineFile(t, filepath.Join(targetRoot, "target-only.txt"), []byte("keep"))
	if _, err := os.Stat(filepath.Join(targetRoot, ".onesync-part", "ignored.part")); err != nil {
		t.Fatalf("reserved transfer file changed: %v", err)
	}
}

func TestEngineHandlesNoChanges(t *testing.T) {
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	content := []byte("same")
	writeEngineFile(t, filepath.Join(sourceRoot, "same.txt"), content)
	writeEngineFile(t, filepath.Join(targetRoot, "same.txt"), content)

	runEnginePair(t, sourceRoot, targetRoot, "task-no-change")
	assertEngineFile(t, filepath.Join(targetRoot, "same.txt"), content)
}

func TestEngineReportsFileProgress(t *testing.T) {
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	writeEngineFile(t, filepath.Join(sourceRoot, "a.txt"), []byte("a"))
	writeEngineFile(t, filepath.Join(sourceRoot, "b.txt"), []byte("b"))

	sourceConnection, targetConnection := net.Pipe()
	sourceSession, err := network.NewSession(sourceConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(source) error = %v", err)
	}
	targetSession, err := network.NewSession(targetConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(target) error = %v", err)
	}
	defer sourceSession.Close()
	defer targetSession.Close()

	recorder := &progressRecorder{}
	sourceEngine, err := DefaultSourceEngine(sourceRoot, sourceSession, recorder)
	if err != nil {
		t.Fatalf("DefaultSourceEngine() error = %v", err)
	}
	targetEngine, err := DefaultTargetEngine(targetRoot, targetSession)
	if err != nil {
		t.Fatalf("DefaultTargetEngine() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targetErrors := make(chan error, 1)
	go func() {
		targetErrors <- targetEngine.Run(ctx, "task-progress")
	}()
	if err := sourceEngine.Run(ctx, "task-progress"); err != nil {
		t.Fatalf("source Run() error = %v", err)
	}
	if err := <-targetErrors; err != nil {
		t.Fatalf("target Run() error = %v", err)
	}
	snapshots := recorder.snapshots()
	if len(snapshots) < 5 {
		t.Fatalf("progress snapshots = %+v", snapshots)
	}
	final := snapshots[len(snapshots)-1]
	if final.TotalFiles != 2 || final.CompletedFiles != 2 || final.CurrentPath != "" {
		t.Fatalf("final progress = %+v", final)
	}
	foundCurrent := false
	for _, snapshot := range snapshots {
		if snapshot.CurrentPath == "a.txt" || snapshot.CurrentPath == "b.txt" {
			foundCurrent = true
		}
	}
	if !foundCurrent {
		t.Fatalf("progress did not include current file: %+v", snapshots)
	}
}

func TestEngineReportsFolderSizes(t *testing.T) {
	sourceRoot := t.TempDir()
	targetRoot := t.TempDir()
	writeEngineFile(t, filepath.Join(sourceRoot, "a.txt"), []byte("aaaa"))
	writeEngineFile(t, filepath.Join(sourceRoot, "nested", "b.txt"), []byte("bbbbbb"))
	writeEngineFile(t, filepath.Join(targetRoot, "old.txt"), []byte("old"))

	sourceConnection, targetConnection := net.Pipe()
	sourceSession, err := network.NewSession(sourceConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(source) error = %v", err)
	}
	targetSession, err := network.NewSession(targetConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(target) error = %v", err)
	}
	defer sourceSession.Close()
	defer targetSession.Close()

	sourceRecorder := &progressRecorder{}
	targetRecorder := &progressRecorder{}
	sourceEngine, err := DefaultSourceEngine(sourceRoot, sourceSession, sourceRecorder)
	if err != nil {
		t.Fatalf("DefaultSourceEngine() error = %v", err)
	}
	targetEngine, err := DefaultTargetEngine(targetRoot, targetSession, targetRecorder)
	if err != nil {
		t.Fatalf("DefaultTargetEngine() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targetErrors := make(chan error, 1)
	go func() {
		targetErrors <- targetEngine.Run(ctx, "task-size")
	}()
	if err := sourceEngine.Run(ctx, "task-size"); err != nil {
		t.Fatalf("source Run() error = %v", err)
	}
	if err := <-targetErrors; err != nil {
		t.Fatalf("target Run() error = %v", err)
	}

	sourceSize := sourceRecorder.latestSize()
	if sourceSize.localBytes != 10 || sourceSize.standardBytes != 10 || sourceSize.localFiles != 2 || sourceSize.standardFiles != 2 {
		t.Fatalf("source size = %+v", sourceSize)
	}
	targetSize := targetRecorder.latestSize()
	if targetSize.localBytes != 13 || targetSize.standardBytes != 10 || targetSize.localFiles != 3 || targetSize.standardFiles != 2 {
		t.Fatalf("target size = %+v", targetSize)
	}
}

func TestCycleGroupMergesConcurrentTaskRuns(t *testing.T) {
	var group cycleGroup
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	run := func() error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return errors.New("shared result")
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- group.Do(context.Background(), "task", run)
	}()
	<-started

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- group.Do(context.Background(), "task", run)
	}()
	waitForCycleWaiters(t, &group, "task", 1)
	close(release)

	if err := <-firstResult; err == nil || err.Error() != "shared result" {
		t.Fatalf("first result = %v", err)
	}
	if err := <-secondResult; err == nil || err.Error() != "shared result" {
		t.Fatalf("second result = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("run called %d times, want 1", got)
	}
}

func waitForCycleWaiters(t *testing.T, group *cycleGroup, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		group.mu.Lock()
		call := group.calls[key]
		got := 0
		if call != nil {
			got = call.waiters
		}
		group.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("cycle waiters did not reach %d", want)
}

func TestCycleGroupWaiterCanCancel(t *testing.T) {
	var group cycleGroup
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = group.Do(context.Background(), "task", func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := group.Do(ctx, "task", func() error {
		t.Fatal("merged run function should not execute")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do() error = %v, want context.Canceled", err)
	}
	close(release)
}

func TestNewEngineValidatesRoleDependencies(t *testing.T) {
	if _, err := NewEngine(Config{}); err == nil {
		t.Fatal("NewEngine() accepted empty config")
	}
}

func TestEngineRejectsDifferentTaskID(t *testing.T) {
	sourceConnection, targetConnection := net.Pipe()
	sourceSession, err := network.NewSession(sourceConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	defer sourceSession.Close()
	defer targetConnection.Close()
	engine, err := DefaultSourceEngine(t.TempDir(), sourceSession)
	if err != nil {
		t.Fatalf("DefaultSourceEngine() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = engine.Run(ctx, "first-task")
	if err := engine.Run(context.Background(), "second-task"); err == nil {
		t.Fatal("Run() accepted a different task ID")
	}
}

func runEnginePair(t *testing.T, sourceRoot, targetRoot, taskID string) {
	t.Helper()
	sourceConnection, targetConnection := net.Pipe()
	sourceSession, err := network.NewSession(sourceConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(source) error = %v", err)
	}
	targetSession, err := network.NewSession(targetConnection, network.DefaultMaxPayload)
	if err != nil {
		t.Fatalf("NewSession(target) error = %v", err)
	}
	defer sourceSession.Close()
	defer targetSession.Close()

	sourceEngine, err := DefaultSourceEngine(sourceRoot, sourceSession)
	if err != nil {
		t.Fatalf("DefaultSourceEngine() error = %v", err)
	}
	targetEngine, err := DefaultTargetEngine(targetRoot, targetSession)
	if err != nil {
		t.Fatalf("DefaultTargetEngine() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targetErrors := make(chan error, 1)
	go func() {
		targetErrors <- targetEngine.Run(ctx, taskID)
	}()
	if err := sourceEngine.Run(ctx, taskID); err != nil {
		t.Fatalf("source Run() error = %v", err)
	}
	if err := <-targetErrors; err != nil {
		t.Fatalf("target Run() error = %v", err)
	}
}

func writeEngineFile(t *testing.T, filePath string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func assertEngineFile(t *testing.T, filePath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", filePath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadFile(%q) = %q, want %q", filePath, got, want)
	}
}

type progressRecorder struct {
	values []progress.Snapshot
	sizes  []sizeRecord
}

func (r *progressRecorder) SetProgress(_ context.Context, snapshot progress.Snapshot) error {
	r.values = append(r.values, snapshot)
	return nil
}

func (r *progressRecorder) snapshots() []progress.Snapshot {
	values := make([]progress.Snapshot, len(r.values))
	copy(values, r.values)
	return values
}

func (r *progressRecorder) SetSizes(_ context.Context, localBytes, standardBytes, localFiles, standardFiles uint64) error {
	r.sizes = append(r.sizes, sizeRecord{
		localBytes:    localBytes,
		standardBytes: standardBytes,
		localFiles:    localFiles,
		standardFiles: standardFiles,
	})
	return nil
}

func (r *progressRecorder) latestSize() sizeRecord {
	if len(r.sizes) == 0 {
		return sizeRecord{}
	}
	return r.sizes[len(r.sizes)-1]
}

type sizeRecord struct {
	localBytes    uint64
	standardBytes uint64
	localFiles    uint64
	standardFiles uint64
}
