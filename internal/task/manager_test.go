package task

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/202121000995/OneSync/internal/progress"
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

func TestManagerAcceptsRunnerStateReports(t *testing.T) {
	runner := &reportingFakeRunner{reportedIdle: make(chan struct{})}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.reportedIdle
	task := waitForTaskState(t, manager, "task", StateIdle)
	if task.LastError != "" {
		t.Fatalf("reported task = %+v", task)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(ctx, "task"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestManagerAcceptsRunnerProgressReports(t *testing.T) {
	runner := &progressReportingRunner{reported: make(chan struct{})}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.reported
	task, err := manager.Get(context.Background(), "task")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if task.Progress == nil || task.Progress.TotalFiles != 3 || task.Progress.CompletedFiles != 2 || task.Progress.CurrentPath != "folder/file.txt" {
		t.Fatalf("progress = %+v", task.Progress)
	}
	if err := manager.Stop(context.Background(), "task"); err != nil {
		t.Fatalf("Stop() error = %v", err)
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

func TestManagerDeleteRemovesTaskAndPersists(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tasks.json")
	manager := newTestManager(t, storePath, &fakeFactory{})
	createSourceTask(t, manager, "task")

	if err := manager.Delete(context.Background(), "task"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := manager.Get(context.Background(), "task"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Get() error = %v, want ErrTaskNotFound", err)
	}

	reloaded := newTestManager(t, storePath, &fakeFactory{})
	tasks, err := reloaded.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("reloaded tasks = %+v, want empty", tasks)
	}
}

func TestManagerUpdatesIgnoreRulesAndRuntimeMetadata(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tasks.json")
	manager := newTestManager(t, storePath, &fakeFactory{})
	createSourceTask(t, manager, "task")

	if err := manager.UpdateIgnoreRules(context.Background(), "task", []string{"*.tmp", "cache/"}); err != nil {
		t.Fatalf("UpdateIgnoreRules() error = %v", err)
	}
	reporter := taskStateReporter{manager: manager, taskID: "task"}
	if err := reporter.AddTraffic(context.Background(), 123, 456); err != nil {
		t.Fatalf("AddTraffic() error = %v", err)
	}
	if err := reporter.AddLog(context.Background(), "info", "连接成功"); err != nil {
		t.Fatalf("AddLog() error = %v", err)
	}

	reloaded := newTestManager(t, storePath, &fakeFactory{})
	found, err := reloaded.Get(context.Background(), "task")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(found.IgnoreRules) != 2 || found.IgnoreRules[0] != "*.tmp" || found.IgnoreRules[1] != "cache/" {
		t.Fatalf("IgnoreRules = %+v", found.IgnoreRules)
	}
	if found.Traffic.ReceivedBytes != 123 || found.Traffic.SentBytes != 456 {
		t.Fatalf("Traffic = %+v", found.Traffic)
	}
	if len(found.Logs) != 1 || found.Logs[0].Message != "连接成功" {
		t.Fatalf("Logs = %+v", found.Logs)
	}
}

func TestManagerDeleteStopsRunningTask(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}), waitForCancel: true}
	manager := newTestManager(t, filepath.Join(t.TempDir(), "tasks.json"), &fakeFactory{runner: runner})
	createSourceTask(t, manager, "task")

	if err := manager.Start(context.Background(), "task"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	<-runner.started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Delete(ctx, "task"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := manager.Get(context.Background(), "task"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("Get() error = %v, want ErrTaskNotFound", err)
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

type reportingFakeRunner struct {
	reportedIdle chan struct{}
}

func (r *reportingFakeRunner) Run(context.Context, string) error {
	return errors.New("Run should not be called for reporting runner")
}

func (r *reportingFakeRunner) RunWithReporter(ctx context.Context, _ string, reporter StateReporter) error {
	if err := reporter.SetState(ctx, StateSyncing, ""); err != nil {
		return err
	}
	if err := reporter.SetState(ctx, StateIdle, ""); err != nil {
		return err
	}
	close(r.reportedIdle)
	<-ctx.Done()
	return ctx.Err()
}

type progressReportingRunner struct {
	reported chan struct{}
}

func (r *progressReportingRunner) Run(context.Context, string) error {
	return errors.New("Run should not be called for progress reporting runner")
}

func (r *progressReportingRunner) RunWithReporter(ctx context.Context, _ string, reporter StateReporter) error {
	progressReporter, ok := reporter.(ProgressReporter)
	if !ok {
		return errors.New("progress reporter is not available")
	}
	if err := progressReporter.SetProgress(ctx, progress.Snapshot{
		TotalFiles:     3,
		CompletedFiles: 2,
		CurrentPath:    "folder/file.txt",
	}); err != nil {
		return err
	}
	close(r.reported)
	<-ctx.Done()
	return ctx.Err()
}
