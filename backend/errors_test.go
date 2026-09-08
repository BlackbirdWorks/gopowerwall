package backend_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/backend"
)

func TestInvalidConfigErrorError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		err  *backend.InvalidConfigError
		want string
	}

	cases := []testCase{
		{
			name: "with a param prefixes the message",
			err:  &backend.InvalidConfigError{Param: "Host", Message: "must not be empty"},
			want: "Host: must not be empty",
		},
		{
			name: "without a param returns the message alone",
			err:  &backend.InvalidConfigError{Message: "invalid configuration"},
			want: "invalid configuration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}
