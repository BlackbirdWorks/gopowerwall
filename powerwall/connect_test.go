package powerwall_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/powerwall"
)

type failoverTestCase struct {
	name          string
	wantMode      powerwall.ConnectionMode
	opts          []powerwall.Option
	wantFailover  bool
	expectConnect bool
}

func TestPowerwallFailoverOptions(t *testing.T) {
	t.Parallel()

	testCases := []failoverTestCase{
		{
			name: "default failover is true",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
			},
			wantFailover:  true,
			wantMode:      powerwall.ModeLocal,
			expectConnect: false,
		},
		{
			name: "explicit WithFailover(false)",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(false),
			},
			wantFailover:  false,
			wantMode:      powerwall.ModeLocal,
			expectConnect: false,
		},
		{
			name: "explicit WithFailover(true)",
			opts: []powerwall.Option{
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(true),
			},
			wantFailover:  true,
			wantMode:      powerwall.ModeLocal,
			expectConnect: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			opts := append(
				[]powerwall.Option{
					powerwall.WithCacheFile(filepath.Join(cacheDir, ".powerwall")),
				},
				tc.opts...,
			)

			pw, err := powerwall.New(t.Context(), opts...)
			if !tc.expectConnect {
				var connectErr *powerwall.ConnectError
				require.ErrorAs(t, err, &connectErr)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, pw)
			assert.Equal(t, tc.wantFailover, pw.Failover())
			assert.Equal(t, tc.wantMode, pw.Mode())
		})
	}
}

type connectContextTestCase struct {
	name        string
	cancelEarly bool
	retry       bool
	failover    bool
}

func TestPowerwallConnectWithContextCancellation(t *testing.T) {
	t.Parallel()

	testCases := []connectContextTestCase{
		{
			name:        "strict mode with canceled context returns false immediately",
			cancelEarly: true,
			retry:       true,
			failover:    false,
		},
		{
			name:        "failover mode with canceled context returns false immediately",
			cancelEarly: true,
			retry:       true,
			failover:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cacheDir := t.TempDir()
			pw, err := powerwall.New(
				t.Context(),
				powerwall.WithHost("127.0.0.1:9"),
				powerwall.WithPassword("test"),
				powerwall.WithCloudMode(false),
				powerwall.WithFailover(tc.failover),
				powerwall.WithCacheFile(filepath.Join(cacheDir, ".powerwall")),
			)
			var connectErr *powerwall.ConnectError
			require.ErrorAs(t, err, &connectErr)
			require.NotNil(t, pw)

			ctx, cancel := context.WithCancel(t.Context())
			if tc.cancelEarly {
				cancel()
			} else {
				defer cancel()
			}

			start := time.Now()
			connected := pw.Connect(ctx, tc.retry)
			assert.False(t, connected)
			assert.Less(t, time.Since(start), 2*time.Second)
			assert.Equal(t, powerwall.ModeLocal, pw.Mode())
		})
	}
}
