// Package atomicfile writes files atomically via a temp file plus rename, so
// a crash, a concurrent reader, or a container restart can never observe a
// partially written file - important for credential caches such as
// pypowerwall's .pypowerwall.auth, which is read on every process start.
package atomicfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const jsonIndent = "  "

// WriteJSON marshals v as indented JSON and writes it to path atomically,
// creating or replacing the file with the given permissions.
func WriteJSON(path string, v any, perm os.FileMode) error {
	b, err := json.MarshalIndent(v, "", jsonIndent)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}

	return Write(path, b, perm)
}

// Write writes data to path atomically via a temp file created in the same
// directory followed by a rename, so concurrent readers never observe a
// partially written file and a crash mid-write never corrupts the original.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".atomicfile-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if writeErr := writeAndClose(tmp, data, perm); writeErr != nil {
		return writeErr
	}

	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		return fmt.Errorf("rename temp file to %s: %w", path, renameErr)
	}

	return nil
}

func writeAndClose(tmp *os.File, data []byte, perm os.FileMode) error {
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	return nil
}
