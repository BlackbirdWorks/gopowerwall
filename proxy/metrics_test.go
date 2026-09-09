package proxy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/proxy"
)

func TestConnectionHealth(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		threshold     int
		failures      int
		recordSuccess bool
		wantDegraded  bool
	}

	for _, tc := range []testCase{
		{name: "no failures stays healthy", threshold: 3, failures: 0, wantDegraded: false},
		{name: "failures below threshold stays healthy", threshold: 3, failures: 2, wantDegraded: false},
		{name: "failures at threshold degrades", threshold: 3, failures: 3, wantDegraded: true},
		{name: "zero threshold never degrades", threshold: 0, failures: 10, wantDegraded: false},
		{
			name:          "success after degradation recovers",
			threshold:     2,
			failures:      2,
			recordSuccess: true,
			wantDegraded:  false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			health := proxy.NewConnectionHealth()
			for range tc.failures {
				health.RecordFailure(tc.threshold)
			}
			if tc.recordSuccess {
				health.RecordSuccess()
			}

			assert.Equal(t, tc.wantDegraded, health.IsDegraded)
			assert.Equal(t, tc.failures, health.TotalFailures)
		})
	}
}

func TestConnectionHealthReset(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		threshold int
		failures  int
	}

	for _, tc := range []testCase{
		{name: "reset restores initial healthy state", threshold: 1, failures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			health := proxy.NewConnectionHealth()
			for range tc.failures {
				health.RecordFailure(tc.threshold)
			}
			require.True(t, health.IsDegraded)

			health.Reset()
			assert.False(t, health.IsDegraded)
			assert.Equal(t, 0, health.ConsecutiveFailures)
			assert.Equal(t, 0, health.TotalFailures)
			assert.Equal(t, 0, health.TotalSuccesses)
		})
	}
}

func TestConnectionHealthSnapshot(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		successes int
		failures  int
		threshold int
	}

	for _, tc := range []testCase{
		{name: "snapshot reflects failure and success counts", successes: 1, failures: 1, threshold: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			health := proxy.NewConnectionHealth()
			for range tc.successes {
				health.RecordSuccess()
			}
			for range tc.failures {
				health.RecordFailure(tc.threshold)
			}

			snap := health.Snapshot()
			assert.Equal(t, 1, snap["consecutive_failures"])
			assert.Equal(t, 1, snap["total_failures"])
			assert.Equal(t, 1, snap["total_successes"])
			assert.Equal(t, false, snap["is_degraded"])
			assert.Contains(t, snap, "last_success_time")
			assert.Contains(t, snap, "last_success_age_seconds")
		})
	}
}

func TestEndpointStatsTracker(t *testing.T) {
	t.Parallel()

	type recordCall struct {
		endpoint string
		success  bool
	}

	type testCase struct {
		name        string
		records     []recordCall
		wantTotal   int
		wantSuccess int
		wantFailed  int
	}

	for _, tc := range []testCase{
		{
			name: "aggregates multiple calls per endpoint",
			records: []recordCall{
				{"/aggregates", true},
				{"/aggregates", true},
				{"/aggregates", false},
				{"/soe", true},
			},
			wantTotal:   3,
			wantSuccess: 2,
			wantFailed:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracker := proxy.NewEndpointStatsTracker()
			for _, rec := range tc.records {
				tracker.Record(rec.endpoint, rec.success)
			}

			snap := tracker.Snapshot()
			require.Contains(t, snap, "/aggregates")
			agg, ok := snap["/aggregates"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.wantTotal, agg["total_calls"])
			assert.Equal(t, tc.wantSuccess, agg["successful_calls"])
			assert.Equal(t, tc.wantFailed, agg["failed_calls"])
			assert.InDelta(t, 66.66, agg["success_rate_percent"].(float64), 0.1)
			assert.Contains(t, agg, "last_success_age_seconds")
			assert.Contains(t, agg, "last_failure_age_seconds")

			require.Contains(t, snap, "/soe")
		})
	}
}

func TestEndpointStatsTrackerReset(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		endpoints []string
	}

	for _, tc := range []testCase{
		{name: "resets all recorded endpoint stats", endpoints: []string{"/a", "/b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tracker := proxy.NewEndpointStatsTracker()
			for _, ep := range tc.endpoints {
				tracker.Record(ep, true)
			}

			cleared := tracker.Reset()
			assert.Equal(t, len(tc.endpoints), cleared)
			assert.Empty(t, tracker.Snapshot())
		})
	}
}
