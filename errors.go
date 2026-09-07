package gopowerwall

import "github.com/blackbirdworks/gopowerwall/backend"

var (
	// ErrInvalidConfig indicates an invalid configuration parameter.
	ErrInvalidConfig = backend.ErrInvalidConfig

	// ErrLogin indicates an authentication or authorization failure.
	ErrLogin = backend.ErrLogin

	// ErrNoClient indicates that no backend client is connected.
	ErrNoClient = backend.ErrNoClient

	// ErrRateLimited indicates an HTTP 429 rate limit or 503 cooldown.
	ErrRateLimited = backend.ErrRateLimited

	// ErrTimeout indicates a network call timeout.
	ErrTimeout = backend.ErrTimeout

	// ErrNotFound indicates a 404 endpoint not found.
	ErrNotFound = backend.ErrNotFound

	// ErrUnsupported indicates a feature unsupported by the active backend.
	ErrUnsupported = backend.ErrUnsupported

	// ErrOffGridConfirm indicates confirm=true was not provided for go_off_grid.
	ErrOffGridConfirm = backend.ErrOffGridConfirm

	// ErrUnexpectedStatus indicates an unexpected HTTP response status code.
	ErrUnexpectedStatus = backend.ErrUnexpectedStatus

	// ErrReserveOutOfRange indicates a backup reserve level outside 0-100.
	ErrReserveOutOfRange = backend.ErrReserveOutOfRange

	// ErrSetOperationFailed indicates the gateway rejected an operation change.
	ErrSetOperationFailed = backend.ErrSetOperationFailed

	// ErrInvalidGridExportMode indicates an unrecognised grid export mode.
	ErrInvalidGridExportMode = backend.ErrInvalidGridExportMode
)

type InvalidConfigError = backend.InvalidConfigError
