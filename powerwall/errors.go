package powerwall

import "github.com/blackbirdworks/gopowerwall/models"

var (
	// ErrInvalidConfig indicates an invalid configuration parameter.
	ErrInvalidConfig = models.ErrInvalidConfig

	// ErrLogin indicates an authentication or authorization failure.
	ErrLogin = models.ErrLogin

	// ErrNoClient indicates that no backend client is connected.
	ErrNoClient = models.ErrNoClient

	// ErrRateLimited indicates an HTTP 429 rate limit or 503 cooldown.
	ErrRateLimited = models.ErrRateLimited

	// ErrTimeout indicates a network call timeout.
	ErrTimeout = models.ErrTimeout

	// ErrNotFound indicates a 404 endpoint not found.
	ErrNotFound = models.ErrNotFound

	// ErrUnsupported indicates a feature unsupported by the active backend.
	ErrUnsupported = models.ErrUnsupported

	// ErrOffGridConfirm indicates confirm=true was not provided for go_off_grid.
	ErrOffGridConfirm = models.ErrOffGridConfirm

	// ErrUnexpectedStatus indicates an unexpected HTTP response status code.
	ErrUnexpectedStatus = models.ErrUnexpectedStatus

	// ErrInvalidBackupDuration indicates a ScheduleMaxBackup duration that
	// cannot be safely represented in the gateway's uint32 DurationSeconds
	// field: either negative, or larger than the field can hold.
	ErrInvalidBackupDuration = models.ErrInvalidBackupDuration

	// ErrDinTooLong indicates a gateway DIN longer than the v1r TLV
	// encoding's single-byte length prefix (tag 2, TAG_PERSONALIZATION)
	// can represent.
	ErrDinTooLong = models.ErrDinTooLong

	// ErrReserveOutOfRange indicates a backup reserve level outside 0-100.
	ErrReserveOutOfRange = models.ErrReserveOutOfRange

	// ErrSetOperationFailed indicates the gateway rejected an operation change.
	ErrSetOperationFailed = models.ErrSetOperationFailed

	// ErrInvalidGridExportMode indicates an unrecognised grid export mode.
	ErrInvalidGridExportMode = models.ErrInvalidGridExportMode

	// ErrOperationBackfillFailed indicates a local-mode partial SetOperation
	// call could not read back the field the caller omitted, so the write
	// was refused rather than sent as a dangerous partial payload.
	ErrOperationBackfillFailed = models.ErrOperationBackfillFailed

	// ErrFieldMissing indicates a typed accessor's underlying poll
	// succeeded, but the specific field it was after was absent from, or
	// the wrong type in, the decoded response. Distinct from [ErrNoClient]
	// ("not connected at all") and [ErrUnsupported] ("this backend never
	// offers that data").
	ErrFieldMissing = models.ErrFieldMissing
)

// InvalidConfigError is an alias of
// [github.com/blackbirdworks/gopowerwall/models.InvalidConfigError],
// returned by [ValidateConfig] (and so by [New], which calls it) when a
// [Config] field is malformed - an unparsable host/port, an invalid email in
// cloud mode, or a cache/auth directory that cannot be created or written
// to. Use [errors.As] to recover the offending parameter name and message.
type InvalidConfigError = models.InvalidConfigError

// ConnectError is an alias of
// [github.com/blackbirdworks/gopowerwall/models.ConnectError], wrapped by
// [New] when construction succeeds (the [Config] itself validated) but the
// initial connection attempt failed across every applicable mode. Use
// [errors.As] to recover the mode that was active when retries were
// exhausted.
type ConnectError = models.ConnectError
