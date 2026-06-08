package filewatch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitPeriodicWaitsForInterval(t *testing.T) {
	start := time.Now()
	if err := WaitPeriodic(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatalf("WaitPeriodic() error = %v", err)
	}
	if time.Since(start) < 10*time.Millisecond {
		t.Fatal("WaitPeriodic() returned before interval elapsed")
	}
}

func TestWaitPeriodicCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitPeriodic(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitPeriodic() error = %v, want context.Canceled", err)
	}
}

func TestWaitPeriodicRejectsInvalidInterval(t *testing.T) {
	if err := WaitPeriodic(context.Background(), 0); err == nil {
		t.Fatal("WaitPeriodic() accepted a zero interval")
	}
}
