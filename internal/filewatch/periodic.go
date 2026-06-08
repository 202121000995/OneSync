package filewatch

import (
	"context"
	"errors"
	"time"
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
