//go:build !windows

package transfer

import "os"

func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
