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
	watcher := scanner.New(scanner.Options{ComputeHash: true, IgnoreRules: ignoreRules})
	baseline, err := watcher.Scan(ctx, root)
	if err != nil {
		return false, WaitPeriodic(ctx, interval)
	}
	baselineSignature := signature(baseline)
	deadline := time.NewTimer(interval)
	defer deadline.Stop()
	ticker := time.NewTicker(checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
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
	if quietFor <= 0 {
		return nil
	}
	quietTimer := time.NewTimer(quietFor)
	defer quietTimer.Stop()
	ticker := time.NewTicker(defaultSettleCheck)
	defer ticker.Stop()
	lastSignature := currentSignature
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-quietTimer.C:
			return nil
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
