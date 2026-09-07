package commands

import "context"

// Context holds runtime state passed down to Kong commands.
type Context struct {
	context.Context
}
