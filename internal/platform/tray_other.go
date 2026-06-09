//go:build !windows

package platform

import "context"

// StartTray is a no-op outside Windows.
func StartTray(_ context.Context, _ string, _ context.CancelFunc) error {
	return nil
}
