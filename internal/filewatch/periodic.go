package filewatch

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/202121000995/OneSync/internal/scanner"
)

const (
	defaultSettleDuration = 600 * time.Millisecond
	defaultSettleCheck    = 200 * time.Millisecond
	defaultChangeCheck    = time.Second
)

// WaitPeriodic waits for the next polling trigger or context cancellation.
func WaitPeriodic(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return errors.New("poll interval must be positive")
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// WaitForChangeOrPeriodic returns when a lightweight filesystem snapshot changes,
// the interval elapses, or the context is canceled. It is a dependency-free
// bridge toward event-based watching for platforms where no watcher is installed.
func WaitForChangeOrPeriodic(ctx context.Context, root string, ignoreRules []string, interval time.Duration) (bool, error) {
	if interval <= 0 {
		return false, errors.New("poll interval must be positive")
	}
	if root == "" {
		return false, WaitPeriodic(ctx, interval)
	}
	checkEvery := interval / 3
	if checkEvery > 5*time.Second {
		checkEvery = 5 * time.Second
	}
	if checkEvery < time.Second {
		checkEvery = time.Second
	}
	return waitForChange(ctx, root, ignoreRules, checkEvery, interval)
}

// WaitForChange returns only when the watched folder content changes or the
// context is canceled. Unlike WaitForChangeOrPeriodic, it keeps one baseline
// for the whole wait so a deletion cannot be lost between short wait cycles.
func WaitForChange(ctx context.Context, root string, ignoreRules []string) error {
	monitor, err := NewMonitor(ctx, root, ignoreRules)
	if err != nil {
		return err
	}
	return monitor.Wait(ctx)
}

// Monitor keeps one filesystem baseline across multiple waits. This prevents a
// deletion or edit from being absorbed as the new baseline between sync cycles.
type Monitor struct {
	root       string
	watcher    scanner.Scanner
	signature  string
	ignoreNone bool
}

// NewMonitor creates a persistent folder-change monitor.
func NewMonitor(ctx context.Context, root string, ignoreRules []string) (*Monitor, error) {
	if root == "" {
		return &Monitor{ignoreNone: true}, nil
	}
	watcher := scanner.New(scanner.Options{ComputeHash: true, IgnoreRules: ignoreRules})
	baseline, err := watcher.Scan(ctx, root)
	if err != nil {
		return nil, err
	}
	return &Monitor{
		root:      root,
		watcher:   watcher,
		signature: signature(baseline),
	}, nil
}

// Wait returns after the monitored folder changes and updates the stored
// baseline to the stable post-change state.
func (m *Monitor) Wait(ctx context.Context) error {
	if m == nil || m.ignoreNone {
		<-ctx.Done()
		return ctx.Err()
	}
	return m.wait(ctx, defaultChangeCheck)
}

func waitForChange(ctx context.Context, root string, ignoreRules []string, checkEvery, deadlineAfter time.Duration) (bool, error) {
	watcher := scanner.New(scanner.Options{ComputeHash: true, IgnoreRules: ignoreRules})
	baseline, err := watcher.Scan(ctx, root)
	if err != nil {
		if deadlineAfter > 0 {
			return false, WaitPeriodic(ctx, deadlineAfter)
		}
		return false, err
	}
	baselineSignature := signature(baseline)
	var deadline <-chan time.Time
	var deadlineTimer *time.Timer
	if deadlineAfter > 0 {
		deadlineTimer = time.NewTimer(deadlineAfter)
		deadline = deadlineTimer.C
		defer deadlineTimer.Stop()
	}
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline:
			return false, nil
		case <-ticker.C:
			current, err := watcher.Scan(ctx, root)
			if err != nil {
				continue
			}
			currentSignature := signature(current)
			if currentSignature != baselineSignature {
				if err := waitUntilStable(ctx, watcher, root, currentSignature, defaultSettleDuration); err != nil {
					return false, err
				}
				return true, nil
			}
		}
	}
}

func waitUntilStable(ctx context.Context, watcher scanner.Scanner, root, currentSignature string, quietFor time.Duration) error {
	_, err := waitUntilStableSignature(ctx, watcher, root, currentSignature, quietFor)
	return err
}

func waitUntilStableSignature(ctx context.Context, watcher scanner.Scanner, root, currentSignature string, quietFor time.Duration) (string, error) {
	if quietFor <= 0 {
		return currentSignature, nil
	}
	quietTimer := time.NewTimer(quietFor)
	defer quietTimer.Stop()
	ticker := time.NewTicker(defaultSettleCheck)
	defer ticker.Stop()
	lastSignature := currentSignature
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-quietTimer.C:
			return lastSignature, nil
		case <-ticker.C:
			current, err := watcher.Scan(ctx, root)
			if err != nil {
				continue
			}
			nextSignature := signature(current)
			if nextSignature != lastSignature {
				lastSignature = nextSignature
				if !quietTimer.Stop() {
					select {
					case <-quietTimer.C:
					default:
					}
				}
				quietTimer.Reset(quietFor)
			}
		}
	}
}

func (m *Monitor) wait(ctx context.Context, checkEvery time.Duration) error {
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := m.watcher.Scan(ctx, m.root)
			if err != nil {
				continue
			}
			currentSignature := signature(current)
			if currentSignature == m.signature {
				continue
			}
			stableSignature, err := waitUntilStableSignature(ctx, m.watcher, m.root, currentSignature, defaultSettleDuration)
			if err != nil {
				return err
			}
			m.signature = stableSignature
			return nil
		}
	}
}

func signature(snapshot scanner.Snapshot) string {
	hash := uint64(1469598103934665603)
	mix := func(value uint64) {
		hash ^= value
		hash *= 1099511628211
	}
	paths := make([]string, 0, len(snapshot.Files))
	for path := range snapshot.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		file := snapshot.Files[path]
		for _, char := range []byte(path) {
			mix(uint64(char))
		}
		mix(uint64(file.Size))
		mix(uint64(file.ModTime))
		for _, char := range []byte(file.Hash) {
			mix(uint64(char))
		}
	}
	return strconv.FormatUint(hash, 16)
}

func DescribeChangeWait() string {
	return strings.TrimSpace(defaultSettleDuration.String())
}
