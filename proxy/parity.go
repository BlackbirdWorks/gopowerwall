package proxy

import "github.com/blackbirdworks/gopowerwall/pkgs/lookup"

// orNil converts a (value, error) pair returned by a gopowerwall client
// accessor into an any that is nil on error. Before the client's API
// redesign, "value unavailable" was signalled by a typed nil pointer
// returned directly from the accessor; now that every such accessor returns
// (T, error), this is the proxy-side parity-serialization glue that
// preserves the exact same "field renders as JSON null" wire behavior
// without the client itself going back to nil-pointer returns.
func orNil[T any](v T, err error) any {
	return lookup.OrNil(v, err)
}
