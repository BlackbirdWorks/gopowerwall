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

	// ErrInvalidBackupDuration indicates a ScheduleMaxBackup duration that
	// cannot be safely represented in the gateway's uint32 DurationSeconds
	// field: either negative, or larger than the field can hold.
	ErrInvalidBackupDuration = backend.ErrInvalidBackupDuration

	// ErrDinTooLong indicates a gateway DIN longer than the v1r TLV
	// encoding's single-byte length prefix (tag 2, TAG_PERSONALIZATION)
	// can represent.
	ErrDinTooLong = backend.ErrDinTooLong

	// ErrReserveOutOfRange indicates a backup reserve level outside 0-100.
	ErrReserveOutOfRange = backend.ErrReserveOutOfRange

	// ErrSetOperationFailed indicates the gateway rejected an operation change.
	ErrSetOperationFailed = backend.ErrSetOperationFailed

	// ErrInvalidGridExportMode indicates an unrecognised grid export mode.
	ErrInvalidGridExportMode = backend.ErrInvalidGridExportMode

	// ErrOperationBackfillFailed indicates a local-mode partial SetOperation
	// call could not read back the field the caller omitted, so the write
	// was refused rather than sent as a dangerous partial payload.
	ErrOperationBackfillFailed = backend.ErrOperationBackfillFailed

	// ErrFieldMissing indicates a typed accessor's underlying poll
	// succeeded, but the specific field it was after was absent from, or
	// the wrong type in, the decoded response. Distinct from [ErrNoClient]
	// ("not connected at all") and [ErrUnsupported] ("this backend never
	// offers that data").
	ErrFieldMissing = backend.ErrFieldMissing
)

// InvalidConfigError is an alias of
// [github.com/blackbirdworks/gopowerwall/backend.InvalidConfigError],
// returned by [ValidateConfig] (and so by [New], which calls it) when a
// [Config] field is malformed - an unparsable host/port, an invalid email in
// cloud mode, or a cache/auth directory that cannot be created or written
// to. Use [errors.As] to recover the offending parameter name and message.
type InvalidConfigError = backend.InvalidConfigError

// ConnectError is an alias of
// [github.com/blackbirdworks/gopowerwall/backend.ConnectError], wrapped by
// [New] when construction succeeds (the [Config] itself validated) but the
// initial connection attempt failed across every applicable mode. Use
// [errors.As] to recover the mode that was active when retries were
// exhausted.
type ConnectError = backend.ConnectError
