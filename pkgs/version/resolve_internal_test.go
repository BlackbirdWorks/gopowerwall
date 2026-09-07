package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolve table-tests the pure decision helper behind Get, including the
// ldflags-override branch, without touching the BuildVersion package global.
func TestResolve(t *testing.T) {
	t.Parallel()

	type testCase struct {
		info  *debug.BuildInfo
		name  string
		build string
		want  string
		ok    bool
	}

	for _, tc := range []testCase{
		{
			name:  "ldflags injected build version wins",
			build: "1.2.3",
			ok:    false,
			want:  "1.2.3",
		},
		{
			name:  "ldflags injected build version wins even with build info present",
			build: "1.2.3",
			info:  &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}},
			ok:    true,
			want:  "1.2.3",
		},
		{
			name:  "falls back to module build info version",
			build: "dev",
			info:  &debug.BuildInfo{Main: debug.Module{Version: "v0.5.0"}},
			ok:    true,
			want:  "v0.5.0",
		},
		{
			name:  "devel placeholder falls back to dev",
			build: "dev",
			info:  &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			ok:    true,
			want:  "dev",
		},
		{
			name:  "empty build info version falls back to dev",
			build: "dev",
			info:  &debug.BuildInfo{Main: debug.Module{Version: ""}},
			ok:    true,
			want:  "dev",
		},
		{
			name:  "no build info available falls back to dev",
			build: "dev",
			info:  nil,
			ok:    false,
			want:  "dev",
		},
		{
			name:  "ok false with non-nil info still falls back to dev",
			build: "dev",
			info:  &debug.BuildInfo{Main: debug.Module{Version: "v1.0.0"}},
			ok:    false,
			want:  "dev",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, resolve(tc.build, tc.info, tc.ok))
		})
	}
}
