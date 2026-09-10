package powerwall

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/lookup"
)

func (p *Powerwall) SiteName(ctx context.Context) (string, error) {
	data, err := p.pollInternal(ctx, "/api/site_info/site_name", false, false, false)
	if err != nil {
		return "", err
	}
	name := lookup.Lookup(data, "site_name")
	if name == nil {
		return "", ErrFieldMissing
	}

	return fmt.Sprintf("%v", name), nil
}

// Status returns the gateway's decoded "/api/status" response as a
// [models.GatewayStatus], or an error if it could not be retrieved. Use
// [Powerwall.Version], [Powerwall.Uptime], or [Powerwall.Din] for a single
// field rather than fetching and decoding the whole document.
func (p *Powerwall) Status(ctx context.Context) (models.GatewayStatus, error) {
	raw := p.PollRaw(ctx, "/api/status")
	if len(raw) == 0 {
		return models.GatewayStatus{}, ErrNotFound
	}
	var res models.GatewayStatus
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.GatewayStatus{}, fmt.Errorf("unmarshal status: %w", err)
	}

	return res, nil
}

// Version returns the gateway firmware version string (e.g.
// "23.44.10 abc12345"), or an error if it could not be retrieved. See
// [Powerwall.VersionNumeric] for the same value parsed into a comparable
// int.
func (p *Powerwall) Version(ctx context.Context) (string, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return "", err
	}
	if st.Version == "" {
		return "", ErrFieldMissing
	}

	return st.Version, nil
}

// VersionNumeric returns the gateway firmware version parsed by
// [github.com/blackbirdworks/gopowerwall/pkgs/version.ParseVersion] -
// major*10000 + minor*100 + patch from the version string's leading
// dotted-numeric run (e.g. "23.44.10" becomes 234410) - or an error if the
// version itself could not be retrieved. A version string with no
// recognisable dotted-numeric run parses to 0 without an error, matching
// ParseVersion's own zero-on-no-match contract.
func (p *Powerwall) VersionNumeric(ctx context.Context) (int, error) {
	s, err := p.Version(ctx)
	if err != nil {
		return 0, err
	}

	return version.ParseVersion(s), nil
}

// Uptime returns the gateway's reported uptime, or an error if it could not
// be retrieved or the gateway's "up_time_seconds" string could not be parsed
// as a [time.Duration] (it is already formatted like one, e.g.
// "1541h38m20.998412744s", despite the field's name).
func (p *Powerwall) Uptime(ctx context.Context) (time.Duration, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return 0, err
	}
	if st.UpTimeSeconds == "" {
		return 0, ErrFieldMissing
	}
	d, parseErr := time.ParseDuration(st.UpTimeSeconds)
	if parseErr != nil {
		return 0, fmt.Errorf("parse uptime %q: %w", st.UpTimeSeconds, parseErr)
	}

	return d, nil
}

// Din returns the gateway's device identification number, or an error if it
// could not be retrieved.
func (p *Powerwall) Din(ctx context.Context) (string, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return "", err
	}
	if st.DIN == "" {
		return "", ErrFieldMissing
	}

	return st.DIN, nil
}

// SystemStatus returns the full decoded "/api/system_status" response as a
// [models.SystemStatus]. It returns [ErrNotFound] if the underlying poll
// produced no data (which also covers "no connection" and any network
// failure, since [Powerwall.PollRaw] does not distinguish those from a
// genuine 404), or a wrapped JSON error if the response could not be
// decoded into the struct.
func (p *Powerwall) SystemStatus(ctx context.Context) (models.SystemStatus, error) {
	raw := p.PollRaw(ctx, "/api/system_status")
	if len(raw) == 0 {
		return models.SystemStatus{}, ErrNotFound
	}
	var res models.SystemStatus
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SystemStatus{}, fmt.Errorf("unmarshal system status: %w", err)
	}

	return res, nil
}

// SOE returns the decoded "/api/system_status/soe" response (state-of-energy
// percentage) as a [models.SOE]. Like [Powerwall.SystemStatus], it returns
// [ErrNotFound] for "no data" (which includes "no connection") and a
// wrapped JSON error on a decode failure. [Powerwall.Level] covers the same
// field with additional [ErrFieldMissing] handling for a malformed
// response; both now return an error rather than a nil pointer.
func (p *Powerwall) SOE(ctx context.Context) (models.SOE, error) {
	raw := p.PollRaw(ctx, "/api/system_status/soe")
	if len(raw) == 0 {
		return models.SOE{}, ErrNotFound
	}
	var res models.SOE
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SOE{}, fmt.Errorf("unmarshal soe: %w", err)
	}

	return res, nil
}

// gridConnected reports whether resp represents a grid-connected state,
// matching either spelling the gateway uses for it across firmware
// versions/backends.
func gridConnected(resp models.GridStatusResponse) bool {
	return resp.GridStatus == "SystemGridConnected" || resp.GridStatus == "SystemConnectedToGrid"
}

// GridStatusString reports whether the site is connected to the grid as a
// human-readable string, "Connected" or "Transition", or an error if the
// underlying [Powerwall.GridStatusResponse] call failed. See
// [Powerwall.GridStatusNumeric] for the same information as 1/0.
func (p *Powerwall) GridStatusString(ctx context.Context) (string, error) {
	resp, err := p.GridStatusResponse(ctx)
	if err != nil {
		return "", err
	}
	if gridConnected(resp) {
		return "Connected", nil
	}

	return "Transition", nil
}

// GridStatusNumeric reports whether the site is connected to the grid as 1
// (connected) or 0 (not connected), matching pypowerwall's numeric output
// mode, or an error if the underlying [Powerwall.GridStatusResponse] call
// failed. See [Powerwall.GridStatusString] for the same information as a
// string.
func (p *Powerwall) GridStatusNumeric(ctx context.Context) (int, error) {
	resp, err := p.GridStatusResponse(ctx)
	if err != nil {
		return 0, err
	}
	if gridConnected(resp) {
		return 1, nil
	}

	return 0, nil
}

// GridStatusResponse returns the decoded "/api/system_status/grid_status"
// response as a [models.GridStatusResponse]. Like [Powerwall.SystemStatus],
// it returns [ErrNotFound] for "no data" (which includes "no connection")
// and a wrapped JSON error on a decode failure. [Powerwall.GridStatus]
// builds its formatted output on top of this call.
func (p *Powerwall) GridStatusResponse(ctx context.Context) (models.GridStatusResponse, error) {
	raw := p.PollRaw(ctx, "/api/system_status/grid_status")
	if len(raw) == 0 {
		return models.GridStatusResponse{}, ErrNotFound
	}
	var res models.GridStatusResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.GridStatusResponse{}, fmt.Errorf("unmarshal grid status: %w", err)
	}

	return res, nil
}

// SiteInfo returns the decoded "/api/site_info" response - site
// configuration parameters such as timezone, grid code, and nominal system
// energy/power - as a [models.SiteInfo]. Like [Powerwall.SystemStatus], it
// returns [ErrNotFound] for "no data" (which includes "no connection") and
// a wrapped JSON error on a decode failure.
func (p *Powerwall) SiteInfo(ctx context.Context) (models.SiteInfo, error) {
	raw := p.PollRaw(ctx, "/api/site_info")
	if len(raw) == 0 {
		return models.SiteInfo{}, ErrNotFound
	}
	var res models.SiteInfo
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.SiteInfo{}, fmt.Errorf("unmarshal site info: %w", err)
	}

	return res, nil
}

// GetTimeRemaining returns estimated backup time remaining, or an error if
// it could not be retrieved - no client for the active mode
// ([ErrNoClient]), the backend's own call failing, or the backend reporting
// no value at all ([ErrFieldMissing]) are all distinguishable via
// errors.Is/errors.As.
func (p *Powerwall) GetTimeRemaining(ctx context.Context) (time.Duration, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.client == nil {
		return 0, ErrNoClient
	}

	hours, err := p.client.GetTimeRemaining(ctx)

	if err != nil {
		return 0, err
	}
	if hours == nil {
		return 0, ErrFieldMissing
	}

	return time.Duration(*hours * float64(time.Hour)), nil
}
