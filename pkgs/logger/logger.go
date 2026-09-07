package logger

import (
	"fmt"
	"os"
	"sync"
)

// Global state for logger matching pypowerwall's module-level debug toggle.
//
//nolint:gochecknoglobals // Global logging flags required for module-wide parity with pypowerwall.
var (
	mu        sync.RWMutex
	debugMode bool
	colorLogs = true
)

// SetDebug enables or disables verbose debug logging.
func SetDebug(toggle bool, color ...bool) {
	mu.Lock()
	defer mu.Unlock()
	debugMode = toggle
	if len(color) > 0 {
		colorLogs = color[0]
	} else {
		colorLogs = true
	}
}

// IsDebug returns whether debug logging is enabled.
func IsDebug() bool {
	mu.RLock()
	defer mu.RUnlock()

	return debugMode
}

// LogDebug writes a debug message if debugMode is enabled.
//
//nolint:goprintffuncname // Named for parity with pypowerwall.
func LogDebug(format string, v ...any) {
	if !IsDebug() {
		return
	}
	mu.RLock()
	colored := colorLogs
	mu.RUnlock()

	msg := fmt.Sprintf(format, v...)
	if colored {
		fmt.Fprintf(os.Stdout, "\x1b[31;1mDEBUG: %s\x1b[0m\n", msg)
	} else {
		fmt.Fprintf(os.Stdout, "DEBUG: %s\n", msg)
	}
}

// LogError writes an error message.
//
//nolint:goprintffuncname // Named for parity with pypowerwall.
func LogError(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	fmt.Fprintf(os.Stderr, "ERROR: %s\n", msg)
}

// LogWarn writes a warning message.
//
//nolint:goprintffuncname // Named for parity with pypowerwall.
func LogWarn(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	fmt.Fprintf(os.Stdout, "WARNING: %s\n", msg)
}
