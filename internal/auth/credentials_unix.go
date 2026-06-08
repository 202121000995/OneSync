//go:build !windows

package auth

import "os"

func replaceCredentialFile(source, target string) error {
	return os.Rename(source, target)
}
