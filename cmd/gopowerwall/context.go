package main

import (
	"context"
	"io"
	"os"
)

// Context holds runtime state passed down to Kong commands.
type Context struct {
	context.Context
	// Out is where human-readable command output is written. It is
	// intentionally injectable (see Output) rather than every command
	// writing straight to os.Stdout: tests can then supply a bytes.Buffer
	// and run in parallel instead of reassigning the process-wide
	// os.Stdout, and a caller embedding these commands elsewhere (a
	// server, a TUI) can redirect output without touching global state.
	Out io.Writer
}

// Output returns the writer commands should use for human-readable output,
// defaulting to os.Stdout when Out is unset so existing construction sites
// such as &Context{Context: ctx} keep working unchanged.
func (c *Context) Output() io.Writer {
	if c.Out == nil {
		return os.Stdout
	}

	return c.Out
}
