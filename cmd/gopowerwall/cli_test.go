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

// TestParserEnvVarResolution tests that environment variables are correctly
// parsed by Kong into CLI struct fields.
func TestParserEnvVarResolution(t *testing.T) {
	type testCase struct {
		validate func(t *testing.T, cli *CLI)
		envKey   string
		envVal   string
		name     string
		args     []string
	}

	cases := []testCase{
		{
			name:   "PW_HOST populates Get Host",
			envKey: "PW_HOST",
			envVal: "192.168.1.100",
			args:   []string{"get"},
			validate: func(t *testing.T, cli *CLI) {
				t.Helper()
				assert.Equal(t, "192.168.1.100", cli.Get.Host)
			},
		},
		{
			name:   "PW_PASSWORD populates Get Password",
			envKey: "PW_PASSWORD",
			envVal: "secret123",
			args:   []string{"get"},
			validate: func(t *testing.T, cli *CLI) {
				t.Helper()
				assert.Equal(t, "secret123", cli.Get.Password)
			},
		},
		{
			name:   "PW_GW_PWD populates Tedapi GwPwd",
			envKey: "PW_GW_PWD",
			envVal: "gatewaypass",
			args:   []string{"tedapi"},
			validate: func(t *testing.T, cli *CLI) {
				t.Helper()
				assert.Equal(t, "gatewaypass", cli.Tedapi.GwPwd)
			},
		},
		{
			name:   "PW_EMAIL populates Setup Email",
			envKey: "PW_EMAIL",
			envVal: "user@example.com",
			args:   []string{"setup"},
			validate: func(t *testing.T, cli *CLI) {
				t.Helper()
				assert.Equal(t, "user@example.com", cli.Setup.Email)
			},
		},
		{
			name:   "PW_DEBUG populates Proxy Debug",
			envKey: "PW_DEBUG",
			envVal: "true",
			args:   []string{"proxy"},
			validate: func(t *testing.T, cli *CLI) {
				t.Helper()
				assert.True(t, cli.Proxy.Debug)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)

			var cli CLI
			parser, err := newParser(&cli)
			require.NoError(t, err)

			_, err = parser.Parse(tc.args)
			require.NoError(t, err)

			tc.validate(t, &cli)
		})
	}
}
