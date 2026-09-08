package backend

import "errors"

var (
	// ErrInvalidConfig indicates an invalid configuration parameter.
	ErrInvalidConfig = errors.New("invalid powerwall configuration parameter")

	// ErrLogin indicates an authentication or authorization failure.
	ErrLogin = errors.New("powerwall login failed")

	// ErrNoClient indicates that no backend client is connected.
	ErrNoClient = errors.New("not connected to Powerwall - no backend client available")

	// ErrRateLimited indicates an HTTP 429 rate limit or 503 cooldown.
	ErrRateLimited = errors.New("rate limited by Powerwall API")

	// ErrTimeout indicates a network call timeout.
	ErrTimeout = errors.New("timeout waiting for Powerwall API")

	// ErrNotFound indicates a 404 endpoint not found.
	ErrNotFound = errors.New("endpoint not found")

	// ErrUnsupported indicates a feature unsupported by the active backend.
	ErrUnsupported = errors.New("operation not supported by active backend")

	// ErrOffGridConfirm indicates confirm=true was not provided for go_off_grid.
	ErrOffGridConfirm = errors.New("go_off_grid requires confirm=true")

	// ErrUnexpectedStatus indicates an unexpected HTTP response status code.
	ErrUnexpectedStatus = errors.New("unexpected HTTP response status")

	// ErrMissingAuthFile indicates that the auth file was not found.
	ErrMissingAuthFile = errors.New("missing auth file")

	// ErrEmailNotFound indicates that the requested email was not found in credentials.
	ErrEmailNotFound = errors.New("email not found in auth file")

	// ErrNoEnergySite indicates that no energy site was found for the user account.
	ErrNoEnergySite = errors.New("no energy site found for user")

	// ErrRSAKeyParse indicates that the RSA key could not be parsed.
	ErrRSAKeyParse = errors.New("failed to parse RSA private key")

	// ErrMessageFault indicates a protobuf fault response in TEDAPI.
	ErrMessageFault = errors.New("v1r message fault")

	// ErrUnknownKeyID indicates that the gateway does not recognize the RSA key ID.
	ErrUnknownKeyID = errors.New("v1r RSA key is not recognized by gateway")

	// ErrUnexpectedResponse indicates unexpected response data.
	ErrUnexpectedResponse = errors.New("unexpected response payload")

	// ErrEmptyResponse indicates an empty response from the gateway.
	ErrEmptyResponse = errors.New("empty response from gateway")

	// ErrInvalidBackupDuration indicates a ScheduleMaxBackup duration that
	// cannot be safely represented in the gateway's uint32 DurationSeconds
	// field: either negative, or larger than the field can hold.
	ErrInvalidBackupDuration = errors.New("invalid max backup duration")

	// ErrDinTooLong indicates a gateway DIN longer than the v1r TLV
	// encoding's single-byte length prefix (tag 2, TAG_PERSONALIZATION)
	// can represent.
	ErrDinTooLong = errors.New("din too long for v1r TLV encoding")

	// ErrFieldMissing indicates the active backend answered successfully
	// (a connection exists and the call did not fail), but the specific
	// field a typed accessor was after was absent from, or the wrong type
	// in, the decoded response. This is distinct from ErrNoClient ("not
	// connected at all") and ErrUnsupported ("this backend never offers
	// that data"): ErrFieldMissing means the gateway answered, just not
	// with the field asked for.
	ErrFieldMissing = errors.New("field missing from gateway response")
)

// InvalidConfigError provides details about an invalid configuration parameter.
type InvalidConfigError struct {
	Param   string
	Message string
}

func (e *InvalidConfigError) Error() string {
	if e.Param != "" {
		return e.Param + ": " + e.Message
	}

	return e.Message
}

// ConnectError reports that constructing a Powerwall succeeded (the
// configuration itself was valid) but the initial connection attempt across
// every applicable mode failed. The caller still gets back a usable, non-nil
// Powerwall - it is simply not connected yet ([Powerwall.IsConnected] false) -
// so a ConnectError is informational rather than fatal: log it, surface it,
// or ignore it and call [Powerwall.Connect] again later, all remain valid
// responses. Mode names the [ConnectionMode] that was active when every
// retry was exhausted, which is not necessarily the mode originally
// configured, since Connect's fallback can move between modes on the way
// there.
type ConnectError struct {
	Mode string
}

func (e *ConnectError) Error() string {
	return "failed to connect to Powerwall in " + e.Mode + " mode after exhausting all retries/fallbacks"
}

var (
	// ErrReserveOutOfRange indicates a backup reserve level outside 0-100.
	ErrReserveOutOfRange = errors.New("reserve level must be between 0 and 100")

	// ErrSetOperationFailed indicates the gateway rejected an operation change.
	ErrSetOperationFailed = errors.New("failed to set operation")

	// ErrInvalidGridExportMode indicates an unrecognised grid export mode.
	ErrInvalidGridExportMode = errors.New("invalid grid export mode")

	// ErrInvalidIslandMode indicates an unsupported or invalid island mode (must be 1 or 6).
	ErrInvalidIslandMode = errors.New("invalid island mode: must be 1 (reconnect) or 6 (off-grid)")

	// ErrOperationBackfillFailed indicates that a local-mode partial
	// SetOperation call could not read back the current backup_reserve_percent
	// or real_mode value needed to avoid clobbering the field the caller did
	// not supply. The local gateway's /api/operation endpoint is a full
	// overwrite, so the write is refused rather than sent as a dangerous
	// partial payload.
	ErrOperationBackfillFailed = errors.New("failed to read current operation settings for local-mode backfill")
)
