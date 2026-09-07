package calc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
)

func TestComputeLLVoltage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		v1n       float64
		v2n       float64
		v3n       float64
		want      float64
		tolerance float64
	}

	for _, tc := range []testCase{
		{
			name:      "no significant voltages sums raw inputs",
			v1n:       10.0,
			v2n:       20.0,
			v3n:       30.0,
			want:      60.0,
			tolerance: 0.01,
		},
		{
			name:      "single phase active",
			v1n:       120.0,
			v2n:       0.0,
			v3n:       0.0,
			want:      120.0,
			tolerance: 0.01,
		},
		{
			name:      "split phase active (two phases)",
			v1n:       120.0,
			v2n:       120.0,
			v3n:       0.0,
			want:      240.0,
			tolerance: 0.01,
		},
		{
			name:      "three phase active",
			v1n:       230.0,
			v2n:       230.0,
			v3n:       230.0,
			want:      398.37,
			tolerance: 0.1,
		},
		{
			name:      "negative single phase active",
			v1n:       -120.0,
			v2n:       0.0,
			v3n:       0.0,
			want:      -120.0,
			tolerance: 0.01,
		},
		{
			name:      "boundary voltage exactly at threshold is not significant",
			v1n:       100.0,
			v2n:       0.0,
			v3n:       0.0,
			want:      100.0,
			tolerance: 0.01,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.InDelta(t, tc.want, calc.ComputeLLVoltage(tc.v1n, tc.v2n, tc.v3n), tc.tolerance)
		})
	}
}

func TestScaleBatteryLevel(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		level float64
		want  float64
	}

	for _, tc := range []testCase{
		{name: "usable maximum maps to 100", level: 100.0, want: 100.0},
		{name: "usable floor maps to 0", level: 5.0, want: 0.0},
		{name: "midpoint maps proportionally", level: 52.5, want: 50.0},
		{name: "below usable floor goes negative", level: 0.0, want: -5.263157894736841},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.InDelta(t, tc.want, calc.ScaleBatteryLevel(tc.level), 1e-9)
		})
	}
}
