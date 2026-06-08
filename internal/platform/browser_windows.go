//go:build windows

package platform

import (
	"fmt"
	"os/exec"
)

// OpenBrowser opens a trusted local URL with the Windows default browser.
func OpenBrowser(url string) error {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		return fmt.Errorf("open management page: %w", err)
	}
	return nil
}
