package task

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManagerCreatePersistsAndReloads(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tasks.json")
	manager := newTestManager(t, storePath, &fakeFactory{})
	task := Task{ID: "b-task", Role: RoleSource, SourcePath: t.TempDir()}
	if err := manager.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := manager.Create(context.Background(), Task{
		ID: "a-task", Role: RoleTarget, TargetPath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}

	reloaded := newTestManager(t, storePath, &fakeFactory{})
	tasks, err := reloaded.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(tasks) != 2 || tasks[0].ID != "a-task" || tasks[1].ID != "b-task" {
		t.Fatalf("List() = %+v, want tasks sorted by ID", tasks)
	}
	if tasks[1].State != StateCreated || tasks[1].CreatedAt.IsZero() {
		t.Fatalf("persisted task = %+v", tasks[1])
	}
}

func TestManagerRejectsDuplicateAndInvalidTasks(t *testing.T) {
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{})
	task := Task{ID: "task", Role: RoleSource, SourcePath: t.TempDir()}
	if err := manager.Create(context.Background(), task); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := manager.Create(context.Background(), task); err == nil {
		t.Fatal("Create() accepted duplicate task")
	}
	if err := manager.Create(context.Background(), Task{
		ID: "../unsafe", Role: RoleSource, SourcePath: t.TempDir(),
	}); err == nil {
		t.Fatal("Create() accepted unsafe task ID")
	}
}

func TestManagerSuccessfulRunBecomesIdle(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}), release: make(chan struct{})}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.started
	waitForTaskState(t, manager, "task", StateSyncing)
	close(runner.release)
	waitForTaskState(t, manager, "task", StateIdle)

	reloaded := newTestManager(t, manager.store.path, &fakeFactory{})
	task, err := reloaded.Get(context.Background(), "task")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.State != StateIdle || task.LastError != "" {
		t.Fatalf("reloaded task = %+v", task)
	}
}

func TestManagerFailedRunPersistsError(t *testing.T) {
	runner := &fakeRunner{runErr: errors.New("connection failed")}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	task := waitForTaskState(t, manager, "task", StateFailed)
	if task.LastError != "connection failed" {
		t.Fatalf("LastError = %q, want connection failed", task.LastError)
	}
}

func TestManagerRejectsNilRunner(t *testing.T) {
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), nilRunnerFactory{})
	createSourceTask(t, manager, "task")
	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	task := waitForTaskState(t, manager, "task", StateFailed)
	if task.LastError != "runner factory returned nil runner" {
		t.Fatalf("LastError = %q", task.LastError)
	}
}

func TestManagerStopCancelsRunningTask(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}), waitForCancel: true}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(ctx, "task"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	task, err := manager.Get(context.Background(), "task")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.State != StateStopped || task.LastError != "" {
		t.Fatalf("stopped task = %+v", task)
	}
}

func TestManagerStopCancelsConnectingFactory(t *testing.T) {
	factory := &fakeFactory{waitForCancel: true, started: make(chan struct{})}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), factory)
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-factory.started
	if err := manager.Stop(context.Background(), "task"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	waitForTaskState(t, manager, "task", StateStopped)
}

func TestManagerStopReturnsFinalPersistenceError(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}), waitForCancel: true}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")
	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.started

	manager.store.path = t.TempDir()
	if err := manager.Stop(context.Background(), "task"); err == nil {
		t.Fatal("Stop() error = nil, want persistence error")
	}
	task, err := manager.Get(context.Background(), "task")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.State != StateFailed {
		t.Fatalf("task state = %q, want failed", task.State)
	}
}

func TestManagerRecoversInterruptedTaskAsFailed(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tasks.json")
	taskStore, err := newStore(storePath)
	if err != nil {
		t.Fatalf("newStore() error = %v", err)
	}
	now := time.Now().UTC()
	if err := taskStore.save(map[string]Task{
		"task": {
			ID: "task", Role: RoleSource, SourcePath: t.TempDir(),
			State: StateSyncing, CreatedAt: now, UpdatedAt: now,
		},
	}); err != nil {
		t.Fatalf("save() error = %v", err)
	}

	manager := newTestManager(t, storePath, &fakeFactory{})
	task, err := manager.Get(context.Background(), "task")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.State != StateFailed || task.LastError != "previous run was interrupted" {
		t.Fatalf("recovered task = %+v", task)
	}
}

func TestManagerConcurrentReads(t *testing.T) {
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{})
	for index := 0; index < 20; index++ {
		createSourceTask(t, manager, "task-"+time.Unix(int64(index), 0).Format("150405"))
	}

	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := manager.List(context.Background()); err != nil {
				t.Errorf("List() error = %v", err)
			}
		}()
	}
	wait.Wait()
}

func newTestManager(t *testing.T, storePath string, factory RunnerFactory) *Manager {
	t.Helper()
	manager, err := NewManager(storePath, factory)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func createSourceTask(t *testing.T, manager *Manager, taskID string) {
	t.Helper()
	if err := manager.Create(context.Background(), Task{
		ID: taskID, Role: RoleSource, SourcePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func waitForTaskState(t *testing.T, manager *Manager, taskID, state string) Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, err := manager.Get(context.Background(), taskID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if task.State == state {
			return task
		}
		time.Sleep(time.Millisecond)
	}
	task, _ := manager.Get(context.Background(), taskID)
	t.Fatalf("task state = %q, want %q", task.State, state)
	return Task{}
}

type fakeFactory struct {
	runner        Runner
	createErr     error
	waitForCancel bool
	started       chan struct{}
}

type nilRunnerFactory struct{}

func (nilRunnerFactory) Create(context.Context, Task) (Runner, error) {
	return nil, nil
}

func (f *fakeFactory) Create(ctx context.Context, _ Task) (Runner, error) {
	if f.started != nil {
		close(f.started)
	}
	if f.waitForCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.runner == nil {
		return &fakeRunner{}, nil
	}
	return f.runner, nil
}

type fakeRunner struct {
	started       chan struct{}
	release       chan struct{}
	waitForCancel bool
	runErr        error
}

func (r *fakeRunner) Run(ctx context.Context, _ string) error {
	if r.started != nil {
		close(r.started)
	}
	if r.waitForCancel {
		<-ctx.Done()
		return ctx.Err()
	}
	if r.release != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.release:
		}
	}
	return r.runErr
}
