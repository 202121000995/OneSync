package filewatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestWaitForChangeOrPeriodicReturnsOnChange(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("one"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		changed, err := WaitForChangeOrPeriodic(ctx, root, nil, 3*time.Second)
		if err != nil {
			t.Errorf("WaitForChangeOrPeriodic() error = %v", err)
		}
		done <- changed
	}()
	time.Sleep(1200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("WriteFile() update error = %v", err)
	}
	if changed := <-done; !changed {
		t.Fatal("WaitForChangeOrPeriodic() did not report a change")
	}
}
