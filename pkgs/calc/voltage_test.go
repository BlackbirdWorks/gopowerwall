package calc_test

import (
	"math"
	"testing"

	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
)

func TestComputeLLVoltageTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		v1n       float64
		v2n       float64
		v3n       float64
		expected  float64
		tolerance float64
	}{
		{
			name:      "zero or insignificant voltages",
			v1n:       10.0,
			v2n:       20.0,
			v3n:       30.0,
			expected:  60.0,
			tolerance: 0.01,
		},
		{
			name:      "single phase active",
			v1n:       120.0,
			v2n:       0.0,
			v3n:       0.0,
			expected:  120.0,
			tolerance: 0.01,
		},
		{
			name:      "split phase active (two phases)",
			v1n:       120.0,
			v2n:       120.0,
			v3n:       0.0,
			expected:  240.0,
			tolerance: 0.01,
		},
		{
			name:      "three phase active",
			v1n:       230.0,
			v2n:       230.0,
			v3n:       230.0,
			expected:  398.37,
			tolerance: 0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := calc.ComputeLLVoltage(tt.v1n, tt.v2n, tt.v3n)
			if math.Abs(got-tt.expected) > tt.tolerance {
				t.Errorf("ComputeLLVoltage(%v, %v, %v) = %v, want %v", tt.v1n, tt.v2n, tt.v3n, got, tt.expected)
			}
		})
	}
}
