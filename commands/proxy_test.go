package commands_test

import (
	"bytes"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/commands"
)

// TestProxyCmdFailsFastOnPortInUse drives ProxyCmd.Run far enough to build
// its config, print the startup banner and reach http.Server.ListenAndServe
// - which is otherwise unsuitable to unit test, since a successful bind
// blocks forever serving traffic. Binding Port to an address this test
// already holds open makes ListenAndServe fail immediately with "address
// already in use" instead, so Run returns without blocking.
func TestProxyCmdFailsFastOnPortInUse(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	cmd := commands.ProxyCmd{
		BindAddress: "127.0.0.1",
		Port:        port,
	}

	var buf bytes.Buffer
	err = cmd.Run(&commands.Context{Context: t.Context(), Out: &buf})
	require.Error(t, err)
	assert.Contains(t, buf.String(), "starting on")
}
