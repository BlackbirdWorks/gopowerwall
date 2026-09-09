//go:build !windows

package atomicfile

import (
	"fmt"
	"os"

	"github.com/google/renameio/v2"
)

// Write writes data to path atomically via renameio.WriteFile, so concurrent
// readers never observe a partially written file and a crash mid-write never
// corrupts the original.
func Write(path string, data []byte, perm os.FileMode) error {
	if err := renameio.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("atomic write %s: %w", path, err)
	}

	return nil
}
