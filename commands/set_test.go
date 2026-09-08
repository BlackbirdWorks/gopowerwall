package commands_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/commands"
)

// TestSetCmdNoActionSpecified covers the guard that rejects a `set` call
// naming no setting to change. It never attempts to connect, so it is safe
// to run in parallel.
func TestSetCmdNoActionSpecified(t *testing.T) {
	t.Parallel()

	cmd := commands.SetCmd{Reserve: -1}

	err := cmd.Run(&commands.Context{Context: t.Context()})
	assert.ErrorIs(t, err, commands.ErrNoActionSpecified)
}

// TestSetCmdRequiresConnection covers SetCmd's disconnected error path; the
// non-routable host fails fast, so it is safe to run in parallel.
func TestSetCmdRequiresConnection(t *testing.T) {
	t.Parallel()

	cmd := commands.SetCmd{
		Reserve: 50,
		Local:   true,
		Host:    "127.0.0.1:9",
	}

	err := cmd.Run(&commands.Context{Context: t.Context()})
	assert.ErrorIs(t, err, commands.ErrUnableToConnect)
}

// TestSetCmdAppliesAllSettingsLocally drives SetCmd.Run against a fake local
// gateway with every setting flag populated, exercising applyMode,
// applyReserve's local-mode success path (no cloud/FleetAPI cap warning or
// confirmation re-poll), applyGridCharging and applyGridExport's success
// paths.
func TestSetCmdAppliesAllSettingsLocally(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t)
	cmd := commands.SetCmd{
		Mode:            "backup",
		GridCharging:    "on",
		GridExport:      "battery_ok",
		Reserve:         30,
		ConnectionFlags: connectionFlags(t, gw),
	}

	err := cmd.Run(&commands.Context{Context: t.Context()})
	require.NoError(t, err)
}

// TestSetCmdCurrentUsesLiveChargeLevel exercises applyCurrent's success path
// (SetReserve to the live battery charge level) on its own, since SetCmd
// treats Reserve == -1 as "no value supplied".
func TestSetCmdCurrentUsesLiveChargeLevel(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t)
	cmd := commands.SetCmd{
		Current:         true,
		ConnectionFlags: connectionFlags(t, gw),
	}

	err := cmd.Run(&commands.Context{Context: t.Context()})
	require.NoError(t, err)
}

// TestSetCmdInvalidValues covers the validation branch of each apply*
// helper: an unrecognised value returns an ErrNoActionSpecified-wrapped
// error without the command ever reaching the underlying Powerwall setter.
func TestSetCmdInvalidValues(t *testing.T) {
	t.Parallel()

	type testCase struct {
		build func(flags commands.ConnectionFlags) commands.SetCmd
		name  string
	}

	for _, tc := range []testCase{
		{
			name: "invalid mode",
			build: func(flags commands.ConnectionFlags) commands.SetCmd {
				return commands.SetCmd{Mode: "bogus", Reserve: -1, ConnectionFlags: flags}
			},
		},
		{
			name: "invalid gridcharging",
			build: func(flags commands.ConnectionFlags) commands.SetCmd {
				return commands.SetCmd{GridCharging: "bogus", Reserve: -1, ConnectionFlags: flags}
			},
		},
		{
			name: "invalid gridexport",
			build: func(flags commands.ConnectionFlags) commands.SetCmd {
				return commands.SetCmd{GridExport: "bogus", Reserve: -1, ConnectionFlags: flags}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gw := newFakeGateway(t)
			cmd := tc.build(connectionFlags(t, gw))

			err := cmd.Run(&commands.Context{Context: t.Context()})
			assert.ErrorIs(t, err, commands.ErrNoActionSpecified)
		})
	}
}
