package logger_test

import (
	"testing"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

//nolint:paralleltest // Tests modify global debug logger state sequentially.
func TestLoggerTable(t *testing.T) {
	tests := []struct {
		action    func()
		name      string
		wantDebug bool
	}{
		{
			name: "set debug true",
			action: func() {
				logger.SetDebug(true, false)
			},
			wantDebug: true,
		},
		{
			name: "set debug false",
			action: func() {
				logger.SetDebug(false)
			},
			wantDebug: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.action()
			if got := logger.IsDebug(); got != tt.wantDebug {
				t.Errorf("IsDebug() = %v, want %v", got, tt.wantDebug)
			}
			logger.LogDebug("test debug %s", "val")
			logger.LogError("test error %s", "val")
			logger.LogWarn("test warn %s", "val")
		})
	}
}
