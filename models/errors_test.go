package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/gopowerwall/models"
)

type invalidConfigTestCase struct {
	name string
	err  *models.InvalidConfigError
	want string
}

func TestInvalidConfigErrorError(t *testing.T) {
	t.Parallel()

	cases := []invalidConfigTestCase{
		{
			name: "with a param prefixes the message",
			err:  &models.InvalidConfigError{Param: "Host", Message: "must not be empty"},
			want: "Host: must not be empty",
		},
		{
			name: "without a param returns the message alone",
			err:  &models.InvalidConfigError{Message: "invalid configuration"},
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

type connectErrorTestCase struct {
	name string
	err  *models.ConnectError
	want string
}

func TestConnectErrorError(t *testing.T) {
	t.Parallel()

	cases := []connectErrorTestCase{
		{
			name: "formats error with mode",
			err:  &models.ConnectError{Mode: "local"},
			want: "failed to connect to Powerwall in local mode after exhausting all retries/fallbacks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.err.Error())
		})
	}
}
