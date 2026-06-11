package task

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/202121000995/OneSync/internal/progress"
)

var ErrTaskNotFound = errors.New("task not found")

// Runner performs one task synchronization cycle.
type Runner interface {
	Run(ctx context.Context, taskID string) error
}

// StateReporter lets long-running runners publish their current phase.
type StateReporter interface {
	SetState(ctx context.Context, state, lastError string) error
}

// ProgressReporter lets long-running runners publish file-level progress.
type ProgressReporter interface {
	SetProgress(ctx context.Context, snapshot progress.Snapshot) error
}

// TrafficReporter lets long-running runners publish network traffic counters.
type TrafficReporter interface {
	AddTraffic(ctx context.Context, receivedBytes, sentBytes uint64) error
}

// SizeReporter lets long-running runners publish folder size counters.
type SizeReporter interface {
	SetSizes(ctx context.Context, localBytes, standardBytes, localFiles, standardFiles uint64) error
}

// DeviceReporter lets long-running runners publish peer and connection details.
type DeviceReporter interface {
	SetDevice(ctx context.Context, details DeviceStats) error
}

// LogReporter lets long-running runners publish task events.
type LogReporter interface {
	AddLog(ctx context.Context, level, message string) error
}

// ReportingRunner performs a task and reports intermediate states.
type ReportingRunner interface {
	RunWithReporter(ctx context.Context, taskID string, reporter StateReporter) error
}

// RunnerFactory creates fresh runtime resources for each task start.
type RunnerFactory interface {
	Create(ctx context.Context, task Task) (Runner, error)
}

type runtimeTask struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// Manager owns persistent task state and transient task runtimes.
type Manager struct {
	mu       sync.RWMutex
	store    *store
	factory  RunnerFactory
	tasks    map[string]Task
	runtimes map[string]*runtimeTask
	now      func() time.Time
}

// NewManager loads and validates tasks from a versioned JSON store.
func NewManager(storePath string, factory RunnerFactory) (*Manager, error) {
	if factory == nil {
		return nil, errors.New("runner factory is required")
	}
	taskStore, err := newStore(storePath)
	if err != nil {
		return nil, err
	}
	tasks, err := taskStore.load()
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		store:    taskStore,
		factory:  factory,
		tasks:    tasks,
		runtimes: make(map[string]*runtimeTask),
		now:      time.Now,
	}
	if err := manager.recoverInterrupted(); err != nil {
		return nil, err
	}
	return manager, nil
}

// Create persists a new task in the created state.
func (m *Manager) Create(ctx context.Context, task Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTask(task); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tasks[task.ID]; exists {
		return fmt.Errorf("task %q already exists", task.ID)
	}
	if len(m.tasks) >= MaxTasks {
		return fmt.Errorf("task limit %d reached", MaxTasks)
	}
	now := m.now().UTC()
	task.State = StateCreated
	task.LastError = ""
	task.CreatedAt = now
	task.UpdatedAt = now
	m.tasks[task.ID] = task
	if err := m.store.save(m.tasks); err != nil {
		delete(m.tasks, task.ID)
		return err
	}
	return nil
}

// Start begins a task asynchronously.
func (m *Manager) Start(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	task, exists := m.tasks[taskID]
	if !exists {
		m.mu.Unlock()
		return ErrTaskNotFound
	}
	if _, running := m.runtimes[taskID]; running {
		m.mu.Unlock()
		return fmt.Errorf("task %q is already running", taskID)
	}
	if task.DeviceDisabled {
		m.mu.Unlock()
		return fmt.Errorf("task %q device is disabled", taskID)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runtime := &runtimeTask{cancel: cancel, done: make(chan struct{})}
	m.runtimes[taskID] = runtime
	previous := task
	task.State = StateConnecting
	task.LastError = ""
	task.Progress = nil
	task.Devices.Connected = 0
	task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "任务正在启动"})
	if len(task.Logs) > 200 {
		task.Logs = append([]LogEntry(nil), task.Logs[len(task.Logs)-200:]...)
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		delete(m.runtimes, taskID)
		cancel()
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	go m.run(runCtx, task, runtime)
	return nil
}

// Stop cancels a running task and waits for its state to be persisted.
func (m *Manager) Stop(ctx context.Context, taskID string) error {
	m.mu.RLock()
	_, exists := m.tasks[taskID]
	runtime := m.runtimes[taskID]
	m.mu.RUnlock()
	if !exists {
		return ErrTaskNotFound
	}
	if runtime == nil {
		return m.updateStopped(taskID)
	}

	runtime.cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runtime.done:
		return runtime.err
	}
}

// Delete removes a task after stopping any active runtime.
func (m *Manager) Delete(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.RLock()
	_, exists := m.tasks[taskID]
	runtime := m.runtimes[taskID]
	m.mu.RUnlock()
	if !exists {
		return ErrTaskNotFound
	}

	if runtime != nil {
		runtime.cancel()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-runtime.done:
			if runtime.err != nil {
				return runtime.err
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	previous, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	delete(m.tasks, taskID)
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// UpdateIgnoreRules persists user-editable ignore rules for one task.
func (m *Manager) UpdateIgnoreRules(ctx context.Context, taskID string, rules []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateIgnoreRules(rules); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.IgnoreRules = append([]string(nil), rules...)
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// UpdateTargetLink refreshes connection metadata for an existing target task.
func (m *Manager) UpdateTargetLink(ctx context.Context, taskID, targetPath, peerAddress, relayURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	if task.Role != RoleTarget {
		return fmt.Errorf("task %q is not a target task", taskID)
	}
	previous := task
	if strings.TrimSpace(targetPath) != "" {
		task.TargetPath = targetPath
	}
	task.PeerAddress = peerAddress
	task.RelayURL = relayURL
	task.State = StateStopped
	task.LastError = ""
	task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "已重新加入同步链接"})
	task.UpdatedAt = m.now().UTC()
	if err := validateTask(task); err != nil {
		return err
	}
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// RenameDevice stores a friendly device name for one task.
func (m *Manager) RenameDevice(ctx context.Context, taskID, alias string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(alias) > 128 || strings.ContainsRune(alias, '\x00') {
		return errors.New("device name is invalid")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.Devices.Alias = strings.TrimSpace(alias)
	task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "设备已重命名"})
	task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{Time: m.now().UTC(), Type: "renamed", Message: "设备已重命名", PeerID: task.Devices.PeerID, Connection: task.Devices.Connection, Endpoint: task.Devices.Endpoint, RelayEndpoint: task.Devices.RelayEndpoint})
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// SetDeviceDisabled toggles whether one task may connect to its peer.
func (m *Manager) SetDeviceDisabled(ctx context.Context, taskID string, disabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.DeviceDisabled = disabled
	if disabled {
		task.Devices.Connected = 0
		task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "warning", Message: "设备已禁用"})
		task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{Time: m.now().UTC(), Type: "disabled", Message: "设备已禁用", PeerID: task.Devices.PeerID, Connection: task.Devices.Connection, Endpoint: task.Devices.Endpoint, RelayEndpoint: task.Devices.RelayEndpoint})
	} else {
		task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "设备已启用"})
		task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{Time: m.now().UTC(), Type: "enabled", Message: "设备已启用", PeerID: task.Devices.PeerID, Connection: task.Devices.Connection, Endpoint: task.Devices.Endpoint, RelayEndpoint: task.Devices.RelayEndpoint})
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// SetDeviceTrusted toggles whether one task's peer is marked as trusted.
func (m *Manager) SetDeviceTrusted(ctx context.Context, taskID string, trusted bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.DeviceTrusted = trusted
	if trusted {
		task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "设备已标记为信任"})
		task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{Time: m.now().UTC(), Type: "trusted", Message: "设备已标记为信任", PeerID: task.Devices.PeerID, Connection: task.Devices.Connection, Endpoint: task.Devices.Endpoint, RelayEndpoint: task.Devices.RelayEndpoint})
	} else {
		task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "设备已取消信任"})
		task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{Time: m.now().UTC(), Type: "untrusted", Message: "设备已取消信任", PeerID: task.Devices.PeerID, Connection: task.Devices.Connection, Endpoint: task.Devices.Endpoint, RelayEndpoint: task.Devices.RelayEndpoint})
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// ClearDeviceBinding removes the currently remembered peer details for one task.
func (m *Manager) ClearDeviceBinding(ctx context.Context, taskID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	alias := task.Devices.Alias
	task.DeviceTrusted = false
	task.Devices = DeviceStats{Alias: alias, Total: 1, TLS: "TLS 1.3"}
	task.Logs = appendLogEntry(task.Logs, LogEntry{Time: m.now().UTC(), Level: "warning", Message: "已踢出并清除设备绑定"})
	task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{Time: m.now().UTC(), Type: "kicked", Message: "已踢出并清除设备绑定", PeerID: previous.Devices.PeerID, Connection: previous.Devices.Connection, Endpoint: previous.Devices.Endpoint, RelayEndpoint: previous.Devices.RelayEndpoint})
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

// Get returns one persistent task snapshot.
func (m *Manager) Get(ctx context.Context, taskID string) (Task, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return Task{}, ErrTaskNotFound
	}
	return task, nil
}

// List returns tasks ordered by ID.
func (m *Manager) List(ctx context.Context) ([]Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	tasks := make([]Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	return tasks, nil
}

func (m *Manager) run(ctx context.Context, task Task, runtime *runtimeTask) {
	defer close(runtime.done)

	runner, err := m.factory.Create(ctx, task)
	if err == nil && runner == nil {
		err = errors.New("runner factory returned nil runner")
	}
	if err == nil {
		if reportingRunner, ok := runner.(ReportingRunner); ok {
			err = reportingRunner.RunWithReporter(ctx, task.ID, taskStateReporter{
				manager: m,
				taskID:  task.ID,
			})
		} else {
			err = m.setState(task.ID, StateSyncing, "")
			if err == nil {
				err = runner.Run(ctx, task.ID)
			}
		}
	}

	state := StateIdle
	lastError := ""
	if errors.Is(ctx.Err(), context.Canceled) {
		state = StateStopped
	} else if err != nil {
		state = StateFailed
		lastError = boundedError(err)
	}
	runtime.err = m.finishRun(task.ID, runtime, state, lastError)
}

type taskStateReporter struct {
	manager *Manager
	taskID  string
}

func (r taskStateReporter) SetState(ctx context.Context, state, lastError string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validState(state) {
		return fmt.Errorf("invalid task state %q", state)
	}
	return r.manager.setState(r.taskID, state, lastError)
}

func (r taskStateReporter) SetProgress(ctx context.Context, snapshot progress.Snapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := progress.Validate(snapshot); err != nil {
		return err
	}
	return r.manager.setProgress(r.taskID, snapshot)
}

func (r taskStateReporter) AddTraffic(ctx context.Context, receivedBytes, sentBytes uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.manager.addTraffic(r.taskID, receivedBytes, sentBytes)
}

func (r taskStateReporter) SetSizes(ctx context.Context, localBytes, standardBytes, localFiles, standardFiles uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.manager.setSizes(r.taskID, localBytes, standardBytes, localFiles, standardFiles)
}

func (r taskStateReporter) SetDevice(ctx context.Context, details DeviceStats) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.manager.setDevice(r.taskID, details)
}

func (r taskStateReporter) AddLog(ctx context.Context, level, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.manager.addLog(r.taskID, level, message)
}

func boundedError(err error) string {
	const limit = 4096
	message := err.Error()
	if len(message) > limit {
		return message[:limit]
	}
	return message
}

func appendLogEntry(logs []LogEntry, entry LogEntry) []LogEntry {
	logs = append(logs, entry)
	if len(logs) > 200 {
		return append([]LogEntry(nil), logs[len(logs)-200:]...)
	}
	return logs
}

func appendDeviceEvent(events []DeviceEvent, event DeviceEvent) []DeviceEvent {
	events = append(events, event)
	if len(events) > 100 {
		return append([]DeviceEvent(nil), events[len(events)-100:]...)
	}
	return events
}

func (m *Manager) setState(taskID, state, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[taskID]
	previous := task
	task.State = state
	task.LastError = lastError
	switch state {
	case StateConnecting:
		task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "正在连接同步设备"})
	case StateSyncing:
		task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "发起同步"})
	case StateFailed:
		task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "error", Message: lastError})
	}
	if len(task.Logs) > 200 {
		task.Logs = append([]LogEntry(nil), task.Logs[len(task.Logs)-200:]...)
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) setProgress(taskID string, snapshot progress.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[taskID]
	previous := task
	task.Progress = &snapshot
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) setSizes(taskID string, localBytes, standardBytes, localFiles, standardFiles uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.Size = SizeStats{
		LocalBytes:    localBytes,
		StandardBytes: standardBytes,
		LocalFiles:    localFiles,
		StandardFiles: standardFiles,
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) setDevice(taskID string, details DeviceStats) error {
	if details.Total == 0 {
		details.Total = 1
	}
	if details.Connected > details.Total {
		details.Total = details.Connected
	}
	if details.TLS == "" {
		details.TLS = "TLS 1.3"
	}
	if err := validateDeviceStats(details); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	if details.Alias == "" {
		details.Alias = task.Devices.Alias
	}
	details.LastSeen = m.now().UTC()
	task.Devices = details
	task.DeviceHistory = appendDeviceEvent(task.DeviceHistory, DeviceEvent{
		Time:          details.LastSeen,
		Type:          "connected",
		Message:       "设备连接成功",
		PeerID:        details.PeerID,
		Connection:    details.Connection,
		Endpoint:      details.Endpoint,
		RelayEndpoint: details.RelayEndpoint,
	})
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) addTraffic(taskID string, receivedBytes, sentBytes uint64) error {
	if receivedBytes == 0 && sentBytes == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.Traffic.ReceivedBytes += receivedBytes
	task.Traffic.SentBytes += sentBytes
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) addLog(taskID, level, message string) error {
	if level == "" {
		level = "info"
	}
	entry := LogEntry{
		Time:    m.now().UTC(),
		Level:   level,
		Message: boundedError(errors.New(message)),
	}
	if err := validateLogs([]LogEntry{entry}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, exists := m.tasks[taskID]
	if !exists {
		return ErrTaskNotFound
	}
	previous := task
	task.Logs = append(task.Logs, entry)
	if len(task.Logs) > 200 {
		task.Logs = append([]LogEntry(nil), task.Logs[len(task.Logs)-200:]...)
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) finishRun(taskID string, runtime *runtimeTask, state, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[taskID]
	task.State = state
	task.LastError = lastError
	if lastError != "" {
		task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "error", Message: lastError})
	} else if state == StateIdle {
		task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "同步完成，等待下一轮"})
	} else if state == StateStopped {
		task.Devices.Connected = 0
		task.Logs = append(task.Logs, LogEntry{Time: m.now().UTC(), Level: "info", Message: "任务已停止"})
	}
	if len(task.Logs) > 200 {
		task.Logs = append([]LogEntry(nil), task.Logs[len(task.Logs)-200:]...)
	}
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	delete(m.runtimes, taskID)
	if err := m.store.save(m.tasks); err != nil {
		task.State = StateFailed
		task.LastError = "persist final task state: " + err.Error()
		task.UpdatedAt = m.now().UTC()
		m.tasks[taskID] = task
		return err
	}
	return nil
}

func (m *Manager) updateStopped(taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task := m.tasks[taskID]
	previous := task
	task.State = StateStopped
	task.LastError = ""
	task.Devices.Connected = 0
	task.UpdatedAt = m.now().UTC()
	m.tasks[taskID] = task
	if err := m.store.save(m.tasks); err != nil {
		m.tasks[taskID] = previous
		return err
	}
	return nil
}

func (m *Manager) recoverInterrupted() error {
	changed := false
	now := m.now().UTC()
	for id, task := range m.tasks {
		if task.State == StateConnecting || task.State == StateSyncing {
			task.State = StateFailed
			task.LastError = "previous run was interrupted"
			task.UpdatedAt = now
			m.tasks[id] = task
			changed = true
		}
	}
	if changed {
		return m.store.save(m.tasks)
	}
	return nil
}
