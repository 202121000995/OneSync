//go:build !windows

package task

import "os"

func replaceStoreFile(source, target string) error {
	return os.Rename(source, target)
}

func syncStoreDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
