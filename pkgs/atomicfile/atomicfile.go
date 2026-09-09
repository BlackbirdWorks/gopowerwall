// Package atomicfile writes files atomically via a temp file plus rename, so
// a crash, a concurrent reader, or a container restart can never observe a
// partially written file - important for credential caches such as
// pypowerwall's .pypowerwall.auth, which is read on every process start.
package atomicfile

import (
	"encoding/json"
	"fmt"
	"os"
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
