package commands_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/internal/commands"
)

// TestTrivialCommands exercises the commands whose Run method has no branch
// logic: they print version/help text to stdout and always succeed.
func TestTrivialCommands(t *testing.T) {
	t.Parallel()

	type testCase struct {
		run  func(t *testing.T) error
		name string
	}

	for _, tc := range []testCase{
		{
			name: "version command",
			run: func(t *testing.T) error {
				t.Helper()
				cmd := commands.VersionCmd{}

				return cmd.Run(&commands.Context{Context: t.Context()})
			},
		},
		{
			name: "setup command",
			run: func(t *testing.T) error {
				t.Helper()
				cmd := commands.SetupCmd{}

				return cmd.Run(&commands.Context{Context: t.Context()})
			},
		},
		{
			name: "authtoken command",
			run: func(t *testing.T) error {
				t.Helper()
				cmd := commands.AuthTokenCmd{}

				return cmd.Run(&commands.Context{Context: t.Context()})
			},
		},
		{
			name: "cloudcheck command",
			run: func(t *testing.T) error {
				t.Helper()
				cmd := commands.CloudCheckCmd{}

				return cmd.Run(&commands.Context{Context: t.Context()})
			},
		},
		{
			name: "tedapi command",
			run: func(t *testing.T) error {
				t.Helper()
				cmd := commands.TedapiCmd{Host: "127.0.0.1"}

				return cmd.Run(&commands.Context{Context: t.Context()})
			},
		},
		{
			name: "register command",
			run: func(t *testing.T) error {
				t.Helper()
				cmd := commands.RegisterCmd{}

				return cmd.Run(&commands.Context{Context: t.Context()})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, tc.run(t))
		})
	}
}
