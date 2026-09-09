//go:build windows

package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Write writes data to path atomically on Windows via a temporary file and replace.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".atomicfile-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	_ = os.Remove(path)
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file to %s: %w", path, err)
	}

	return nil
}
