package validation_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/pkgs/validation"
)

func TestIsValidHost(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		host string
		want bool
	}

	for _, tc := range []testCase{
		{name: "valid IPv4", host: "192.168.91.1", want: true},
		{name: "valid IPv4 with port", host: "192.168.91.1:443", want: true},
		{name: "valid hostname", host: "teg.lan", want: true},
		{name: "valid hostname with port", host: "teg.lan:443", want: true},
		{name: "valid fully qualified hostname with trailing dot", host: "teg.example.com.", want: true},
		{name: "valid IPv6", host: "::1", want: true},
		{name: "empty string", host: "", want: false},
		{name: "invalid hostname characters", host: "!!!bad!!!", want: false},
		{name: "too long", host: strings.Repeat("a", 256), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, validation.IsValidHost(tc.host))
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		email string
		want  bool
	}

	for _, tc := range []testCase{
		{name: "valid email", email: "user@example.com", want: true},
		{name: "missing domain", email: "user@", want: false},
		{name: "missing at sign", email: "userexample.com", want: false},
		{name: "empty string", email: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, validation.IsValidEmail(tc.email))
		})
	}
}

func TestValidateBatteryDispatch(t *testing.T) {
	t.Parallel()

	type testCase struct {
		wantErr  error
		name     string
		power    float64
		duration int
	}

	for _, tc := range []testCase{
		{name: "valid discharge setpoint", power: 5000.0, duration: 300},
		{name: "valid charge setpoint", power: -3500.0, duration: 600},
		{name: "valid zero hold setpoint", power: 0.0, duration: 1},
		{name: "valid max duration", power: 1000.0, duration: validation.MaxDispatchDurationSeconds},
		{
			name:     "NaN power rejected",
			power:    math.NaN(),
			duration: 60,
			wantErr:  validation.ErrInvalidDispatchPower,
		},
		{
			name:     "positive infinity power rejected",
			power:    math.Inf(1),
			duration: 60,
			wantErr:  validation.ErrInvalidDispatchPower,
		},
		{
			name:     "negative infinity power rejected",
			power:    math.Inf(-1),
			duration: 60,
			wantErr:  validation.ErrInvalidDispatchPower,
		},
		{
			name:     "zero duration rejected",
			power:    1000.0,
			duration: 0,
			wantErr:  validation.ErrInvalidDispatchDuration,
		},
		{
			name:     "negative duration rejected",
			power:    1000.0,
			duration: -10,
			wantErr:  validation.ErrInvalidDispatchDuration,
		},
		{
			name:     "duration above max rejected",
			power:    1000.0,
			duration: validation.MaxDispatchDurationSeconds + 1,
			wantErr:  validation.ErrInvalidDispatchDuration,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validation.ValidateBatteryDispatch(tc.power, tc.duration)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}
			require.NoError(t, err)
		})
	}
}
