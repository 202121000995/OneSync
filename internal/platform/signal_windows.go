//go:build windows

package platform

import (
	"context"
	"os"
	"os/signal"
)

// NotifyShutdown returns a context canceled by terminal stop signals.
func NotifyShutdown(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt)
}
