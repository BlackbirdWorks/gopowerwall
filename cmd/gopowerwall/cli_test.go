package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewParserRegistersEverySubcommand exercises newParser's kong grammar:
// every subcommand registered on CLI parses with a minimal, valid argv and
// resolves to the expected command name. Run itself drives os.Args and can
// call os.Exit on a parse error, which makes it unsuitable to invoke
// directly from a test; newParser isolates the grammar so it can be built
// and parsed against arbitrary argv instead.
func TestNewParserRegistersEverySubcommand(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		want string
		args []string
	}

	for _, tc := range []testCase{
		{name: "version", args: []string{"version"}, want: "version"},
		{name: "register", args: []string{"register"}, want: "register"},
		{name: "authtoken", args: []string{"authtoken"}, want: "authtoken"},
		{name: "get", args: []string{"get"}, want: "get"},
		{name: "tedapi", args: []string{"tedapi"}, want: "tedapi"},
		{name: "setup", args: []string{"setup"}, want: "setup"},
		{name: "cloudcheck", args: []string{"cloudcheck"}, want: "cloudcheck"},
		{name: "proxy", args: []string{"proxy"}, want: "proxy"},
		{name: "set", args: []string{"set", "--reserve", "50"}, want: "set"},
		{name: "scan", args: []string{"scan"}, want: "scan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cli CLI
			parser, err := newParser(&cli)
			require.NoError(t, err)

			kctx, err := parser.Parse(tc.args)
			require.NoError(t, err)
			require.NotNil(t, kctx.Selected())
			assert.Equal(t, tc.want, kctx.Selected().Name)
		})
	}
}

// TestNewParserRejectsUnknownCommand covers newParser's failure path: an
// unrecognised subcommand is rejected by the grammar rather than silently
// accepted.
func TestNewParserRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	var cli CLI
	parser, err := newParser(&cli)
	require.NoError(t, err)

	_, err = parser.Parse([]string{"bogus-command"})
	require.Error(t, err)
}
