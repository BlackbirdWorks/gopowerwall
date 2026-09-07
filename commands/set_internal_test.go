package commands

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall"
)

// TestApplyReserveCloudModeWarnsAboveCap white-box tests applyReserve's
// cloud/FleetAPI branch: a reserve above 80 logs the cap warning, and a
// failed write (there is no real Tesla connection here - no auth file is
// present) returns without attempting the confirmation re-poll. It cannot
// reach the confirmation re-poll's success path itself: that requires a
// genuinely connected cloud or FleetAPI backend, which is only real (and
// owned by a different area of this codebase) once authenticated against
// Tesla. gopowerwall.New still reports IsCloud() == true immediately from
// config, before any connection attempt is made, which is what lets this
// test reach applyReserve's cap-warning branch at all.
func TestApplyReserveCloudModeWarnsAboveCap(t *testing.T) {
	t.Parallel()

	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithCloudMode(true),
		gopowerwall.WithAuthPath(t.TempDir()),
	)
	require.NoError(t, err)
	require.True(t, pw.IsCloud())
	require.False(t, pw.IsConnected())

	cmd := &SetCmd{Reserve: 90}
	cmd.applyReserve(t.Context(), pw, io.Discard)
}

// TestApplyReserveLocalModeSkipsCapCheck white-box tests that applyReserve
// never treats local mode as capped, regardless of the requested level.
func TestApplyReserveLocalModeSkipsCapCheck(t *testing.T) {
	t.Parallel()

	pw, err := gopowerwall.New(
		t.Context(),
		gopowerwall.WithHost("127.0.0.1:9"),
		gopowerwall.WithPassword("test"),
		gopowerwall.WithCloudMode(false),
	)
	require.NoError(t, err)
	assert.False(t, pw.IsCloud())
	assert.False(t, pw.IsFleetAPI())

	cmd := &SetCmd{Reserve: 90}
	cmd.applyReserve(t.Context(), pw, io.Discard)
}
