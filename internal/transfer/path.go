package transfer

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func validateRelativePath(filePath string) error {
	if filePath == "" {
		return errors.New("path is empty")
	}
	if strings.ContainsAny(filePath, "\\:\x00") || strings.HasPrefix(filePath, "/") {
		return errors.New("path contains unsafe characters")
	}
	cleaned := path.Clean(filePath)
	if cleaned != filePath || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("path is not a normalized relative path")
	}
	return nil
}

func targetPath(root, relativePath string) (string, error) {
	if err := validateRelativePath(relativePath); err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absoluteRoot, filepath.FromSlash(relativePath))
	relative, err := filepath.Rel(absoluteRoot, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("target path escapes root")
	}
	return target, nil
}

func prepareTargetParent(root, relativePath string) error {
	directory := path.Dir(relativePath)
	if directory == "." {
		return nil
	}
	current := root
	for _, component := range strings.Split(directory, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("target parent contains a symbolic link or non-directory")
		}
	}
	return nil
}
