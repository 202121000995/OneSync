package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/202121000995/OneSync/internal/network"
	"github.com/202121000995/OneSync/internal/progress"
	"github.com/202121000995/OneSync/internal/scanner"
	"github.com/202121000995/OneSync/internal/transfer"
)

const (
	RoleSource    = "source"
	RoleTarget    = "target"
	MaxOperations = 1_000_000
)

type fileSender interface {
	SendFile(ctx context.Context, session network.Session, requestID uint64, sourcePath, relativePath string) error
}

type fileReceiver interface {
	ReceiveFile(ctx context.Context, session network.Session) error
}

// ProgressReporter receives file-level synchronization progress.
type ProgressReporter interface {
	SetProgress(ctx context.Context, snapshot progress.Snapshot) error
}

// SizeReporter receives task folder size snapshots.
type SizeReporter interface {
	SetSizes(ctx context.Context, localBytes, standardBytes, localFiles, standardFiles uint64) error
}

// Engine runs one source or target synchronization cycle.
type Engine struct {
	role             string
	root             string
	session          network.Session
	scanner          scanner.Scanner
	differ           Differ
	sender           fileSender
	receiver         fileReceiver
	progressReporter ProgressReporter
	cycles           cycleGroup
	taskMu           sync.Mutex
	taskID           string
}

// Config provides the role-specific dependencies for a synchronization engine.
type Config struct {
	Role     string
	Root     string
	Session  network.Session
	Scanner  scanner.Scanner
	Differ   Differ
	Sender   fileSender
	Receiver fileReceiver
	Progress ProgressReporter
}

// NewEngine validates and creates a synchronization engine.
func NewEngine(config Config) (*Engine, error) {
	if config.Role != RoleSource && config.Role != RoleTarget {
		return nil, errors.New("sync role must be source or target")
	}
	if strings.TrimSpace(config.Root) == "" {
		return nil, errors.New("sync root is required")
	}
	if config.Session == nil || config.Scanner == nil {
		return nil, errors.New("sync session and scanner are required")
	}
	if config.Role == RoleSource && (config.Differ == nil || config.Sender == nil) {
		return nil, errors.New("source differ and sender are required")
	}
	if config.Role == RoleTarget && config.Receiver == nil {
		return nil, errors.New("target receiver is required")
	}
	return &Engine{
		role:             config.Role,
		root:             config.Root,
		session:          config.Session,
		scanner:          config.Scanner,
		differ:           config.Differ,
		sender:           config.Sender,
		receiver:         config.Receiver,
		progressReporter: config.Progress,
	}, nil
}

// Run executes one cycle. Concurrent calls for the same task share one result.
func (e *Engine) Run(ctx context.Context, taskID string) error {
	if strings.TrimSpace(taskID) == "" {
		return errors.New("task ID is required")
	}
	e.taskMu.Lock()
	if e.taskID == "" {
		e.taskID = taskID
	}
	boundTaskID := e.taskID
	e.taskMu.Unlock()
	if taskID != boundTaskID {
		return fmt.Errorf("engine is bound to task %q", boundTaskID)
	}
	return e.cycles.Do(ctx, taskID, func() error {
		if e.role == RoleSource {
			return e.runSource(ctx)
		}
		return e.runTarget(ctx)
	})
}

func (e *Engine) runSource(ctx context.Context) error {
	const snapshotRequestID uint64 = 1
	if err := e.session.Send(ctx, network.Message{
		Type: network.MessageSnapshotRequest, RequestID: snapshotRequestID,
	}); err != nil {
		return fmt.Errorf("request target snapshot: %w", err)
	}
	response, err := e.session.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receive target snapshot: %w", err)
	}
	if response.Type != network.MessageSnapshotResponse || response.RequestID != snapshotRequestID {
		return errors.New("unexpected target snapshot response")
	}
	targetSnapshot, err := decodeSnapshot(response.Payload)
	if err != nil {
		return fmt.Errorf("decode target snapshot: %w", err)
	}

	sourceSnapshot, err := e.scanner.Scan(ctx, e.root)
	if err != nil {
		return fmt.Errorf("scan source: %w", err)
	}
	sourceFiles, sourceBytes := snapshotSize(sourceSnapshot)
	if err := e.reportSizes(ctx, sourceBytes, sourceBytes, sourceFiles, sourceFiles); err != nil {
		return err
	}
	operations, err := e.differ.Compare(sourceSnapshot, targetSnapshot)
	if err != nil {
		return fmt.Errorf("compare snapshots: %w", err)
	}
	if len(operations) > MaxOperations {
		return fmt.Errorf("sync plan contains %d operations, limit is %d", len(operations), MaxOperations)
	}
	if err := e.reportProgress(ctx, progress.Snapshot{TotalFiles: len(operations)}); err != nil {
		return err
	}

	const planRequestID uint64 = 2
	if err := sendPlan(ctx, e.session, planRequestID, planPayload{
		OperationCount: len(operations),
		StandardFiles:  sourceFiles,
		StandardBytes:  sourceBytes,
	}); err != nil {
		return err
	}
	if err := expectAck(ctx, e.session, planRequestID); err != nil {
		return fmt.Errorf("target rejected sync plan: %w", err)
	}

	for index, operation := range operations {
		if operation.Type != OperationCreate && operation.Type != OperationUpdate {
			return fmt.Errorf("unsupported operation type %q", operation.Type)
		}
		sourcePath := filepath.Join(e.root, filepath.FromSlash(operation.Entry.Path))
		requestID := uint64(index) + 3
		if err := e.reportProgress(ctx, progress.Snapshot{
			TotalFiles:     len(operations),
			CompletedFiles: index,
			CurrentPath:    operation.Entry.Path,
		}); err != nil {
			return err
		}
		if err := e.sender.SendFile(ctx, e.session, requestID, sourcePath, operation.Entry.Path); err != nil {
			return fmt.Errorf("transfer %q: %w", operation.Entry.Path, err)
		}
		if err := e.reportProgress(ctx, progress.Snapshot{
			TotalFiles:     len(operations),
			CompletedFiles: index + 1,
		}); err != nil {
			return err
		}
	}

	completeRequestID := uint64(len(operations)) + 3
	if err := e.session.Send(ctx, network.Message{
		Type: network.MessageSyncComplete, RequestID: completeRequestID,
	}); err != nil {
		return fmt.Errorf("send sync completion: %w", err)
	}
	if err := expectAck(ctx, e.session, completeRequestID); err != nil {
		return fmt.Errorf("target rejected sync completion: %w", err)
	}
	return nil
}

func (e *Engine) runTarget(ctx context.Context) error {
	request, err := e.session.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receive snapshot request: %w", err)
	}
	if request.Type != network.MessageSnapshotRequest {
		return errors.New("expected snapshot request")
	}
	targetSnapshot, err := e.scanner.Scan(ctx, e.root)
	if err != nil {
		return fmt.Errorf("scan target: %w", err)
	}
	targetFiles, targetBytes := snapshotSize(targetSnapshot)
	if err := e.reportSizes(ctx, targetBytes, 0, targetFiles, 0); err != nil {
		return err
	}
	payload, err := encodeSnapshot(targetSnapshot)
	if err != nil {
		return fmt.Errorf("encode target snapshot: %w", err)
	}
	if err := e.session.Send(ctx, network.Message{
		Type: network.MessageSnapshotResponse, RequestID: request.RequestID, Payload: payload,
	}); err != nil {
		return fmt.Errorf("send target snapshot: %w", err)
	}

	planMessage, err := e.session.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receive sync plan: %w", err)
	}
	if planMessage.Type != network.MessageSyncPlan {
		return errors.New("expected sync plan")
	}
	plan, err := decodePlan(planMessage.Payload)
	if err != nil {
		return err
	}
	if err := e.reportSizes(ctx, targetBytes, plan.StandardBytes, targetFiles, plan.StandardFiles); err != nil {
		return err
	}
	if err := e.reportProgress(ctx, progress.Snapshot{TotalFiles: plan.OperationCount}); err != nil {
		return err
	}
	if err := e.session.Send(ctx, network.Message{
		Type: network.MessageAck, RequestID: planMessage.RequestID,
	}); err != nil {
		return fmt.Errorf("acknowledge sync plan: %w", err)
	}

	for index := range plan.OperationCount {
		if err := e.receiver.ReceiveFile(ctx, e.session); err != nil {
			return fmt.Errorf("receive file: %w", err)
		}
		if err := e.reportProgress(ctx, progress.Snapshot{
			TotalFiles:     plan.OperationCount,
			CompletedFiles: index + 1,
		}); err != nil {
			return err
		}
	}

	complete, err := e.session.Receive(ctx)
	if err != nil {
		return fmt.Errorf("receive sync completion: %w", err)
	}
	if complete.Type != network.MessageSyncComplete {
		return errors.New("expected sync completion")
	}
	finalSnapshot, err := e.scanner.Scan(ctx, e.root)
	if err != nil {
		return fmt.Errorf("scan target after sync: %w", err)
	}
	finalFiles, finalBytes := snapshotSize(finalSnapshot)
	if err := e.reportSizes(ctx, finalBytes, plan.StandardBytes, finalFiles, plan.StandardFiles); err != nil {
		return err
	}
	return e.session.Send(ctx, network.Message{
		Type: network.MessageAck, RequestID: complete.RequestID,
	})
}

func (e *Engine) reportProgress(ctx context.Context, snapshot progress.Snapshot) error {
	if e.progressReporter == nil {
		return nil
	}
	return e.progressReporter.SetProgress(ctx, snapshot)
}

func (e *Engine) reportSizes(ctx context.Context, localBytes, standardBytes, localFiles, standardFiles uint64) error {
	reporter, ok := e.progressReporter.(SizeReporter)
	if !ok {
		return nil
	}
	return reporter.SetSizes(ctx, localBytes, standardBytes, localFiles, standardFiles)
}

func snapshotSize(snapshot scanner.Snapshot) (uint64, uint64) {
	var files uint64
	var bytes uint64
	for _, entry := range snapshot.Files {
		files++
		if entry.Size > 0 {
			bytes += uint64(entry.Size)
		}
	}
	return files, bytes
}

type cycleCall struct {
	done    chan struct{}
	err     error
	waiters int
}

type cycleGroup struct {
	mu    sync.Mutex
	calls map[string]*cycleCall
}

func (g *cycleGroup) Do(ctx context.Context, key string, run func() error) error {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*cycleCall)
	}
	if call, exists := g.calls[key]; exists {
		call.waiters++
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-call.done:
			return call.err
		}
	}
	call := &cycleCall{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	call.err = run()
	close(call.done)

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	return call.err
}

// DefaultSourceEngine creates a source engine using production implementations.
func DefaultSourceEngine(root string, session network.Session, reporters ...ProgressReporter) (*Engine, error) {
	return DefaultSourceEngineWithOptions(root, session, scanner.Options{ComputeHash: true}, reporters...)
}

// DefaultSourceEngineWithOptions creates a source engine with custom scanner options.
func DefaultSourceEngineWithOptions(root string, session network.Session, options scanner.Options, reporters ...ProgressReporter) (*Engine, error) {
	options.ComputeHash = true
	return NewEngine(Config{
		Role:     RoleSource,
		Root:     root,
		Session:  session,
		Scanner:  scanner.New(options),
		Differ:   NewDiffer(),
		Sender:   transfer.Sender{},
		Progress: firstProgressReporter(reporters),
	})
}

// DefaultTargetEngine creates a target engine using production implementations.
func DefaultTargetEngine(root string, session network.Session, reporters ...ProgressReporter) (*Engine, error) {
	return DefaultTargetEngineWithOptions(root, session, scanner.Options{ComputeHash: true}, reporters...)
}

// DefaultTargetEngineWithOptions creates a target engine with custom scanner options.
func DefaultTargetEngineWithOptions(root string, session network.Session, options scanner.Options, reporters ...ProgressReporter) (*Engine, error) {
	options.ComputeHash = true
	return NewEngine(Config{
		Role:     RoleTarget,
		Root:     root,
		Session:  session,
		Scanner:  scanner.New(options),
		Receiver: transfer.Receiver{Root: root},
		Progress: firstProgressReporter(reporters),
	})
}

func firstProgressReporter(reporters []ProgressReporter) ProgressReporter {
	if len(reporters) == 0 {
		return nil
	}
	return reporters[0]
}
