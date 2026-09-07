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
