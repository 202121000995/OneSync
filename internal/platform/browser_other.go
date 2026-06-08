//go:build !windows

package platform

// OpenBrowser is intentionally disabled outside the Windows client.
func OpenBrowser(string) error {
	return nil
}
