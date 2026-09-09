// Package atomicfile writes files atomically via a temp file plus rename, so
// a crash, a concurrent reader, or a container restart can never observe a
// partially written file - important for credential caches such as
// pypowerwall's .pypowerwall.auth, which is read on every process start.
package atomicfile

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/google/renameio/v2"
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

// Write writes data to path atomically via renameio.WriteFile, so concurrent
// readers never observe a partially written file and a crash mid-write never
// corrupts the original.
func Write(path string, data []byte, perm os.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("atomic write %s: %w", path, err)
	}

	return nil
}
