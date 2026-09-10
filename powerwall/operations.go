package powerwall

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/calc"
)

func (p *Powerwall) Operation(ctx context.Context) (models.Operation, error) {
	return p.readOperation(ctx, false)
}

// readOperation fetches /api/operation, optionally bypassing the poll cache.
// force must be true whenever a stale cached value would be unsafe to use -
// e.g. SetOperation's local-mode back-fill, or confirming the value a
// cloud/FleetAPI write actually applied.
func (p *Powerwall) readOperation(ctx context.Context, force bool) (models.Operation, error) {
	var opts []PollOption
	if force {
		opts = append(opts, WithForce(true))
	}
	raw := p.PollRaw(ctx, "/api/operation", opts...)
	if len(raw) == 0 {
		return models.Operation{}, ErrNotFound
	}
	var res models.Operation
	if err := json.Unmarshal(raw, &res); err != nil {
		return models.Operation{}, fmt.Errorf("unmarshal operation: %w", err)
	}

	return res, nil
}

// GetReserve returns the current backup reserve percentage as the gateway
// reports it, or an error if it could not be retrieved - this wraps
// [Powerwall.Operation]. GetReserve uses the poll cache; see
// [Powerwall.GetReserveForced] for an uncached, always-scaled read, and
// [Powerwall.GetReserveScaled] for a cached, scaled read.
func (p *Powerwall) GetReserve(ctx context.Context) (float64, error) {
	op, err := p.Operation(ctx)
	if err != nil {
		return 0, err
	}

	return op.BackupReservePercent, nil
}

// GetReserveScaled returns the current backup reserve percentage rescaled
// with [github.com/blackbirdworks/gopowerwall/pkgs/calc.ScaleBatteryLevel],
// the same scaling [Powerwall.LevelScaled] applies. See [Powerwall.GetReserve]
// for the unscaled, cached read this builds on.
func (p *Powerwall) GetReserveScaled(ctx context.Context) (float64, error) {
	val, err := p.GetReserve(ctx)
	if err != nil {
		return 0, err
	}

	return calc.ScaleBatteryLevel(val), nil
}

// GetReserveForced returns the current backup reserve percentage (always
// scaled, unlike [Powerwall.GetReserve]), bypassing the poll cache, or an
// error if it could not be retrieved. Callers use this right after a
// reserve write to confirm the value Tesla actually applied, since
// cloud/FleetAPI silently cap the requested reserve (e.g. to 80%) rather
// than rejecting the write.
func (p *Powerwall) GetReserveForced(ctx context.Context) (float64, error) {
	op, err := p.readOperation(ctx, true)
	if err != nil {
		return 0, err
	}

	return calc.ScaleBatteryLevel(op.BackupReservePercent), nil
}

// GetMode returns the current real operating mode (e.g.
// "self_consumption"), or an error if it could not be retrieved or was
// empty - an error from the underlying [Powerwall.Operation] call, and "the
// gateway reported an empty mode string" ([ErrFieldMissing]), are
// distinguished from each other via errors.Is/errors.As.
func (p *Powerwall) GetMode(ctx context.Context) (string, error) {
	op, err := p.Operation(ctx)
	if err != nil {
		return "", err
	}
	if op.RealMode == "" {
		return "", ErrFieldMissing
	}

	return op.RealMode, nil
}

// SetReserve sets the battery backup reserve to level, a percentage in
// [0, 100]. It is a thin wrapper over [Powerwall.SetOperation] with mode
// left nil; see SetOperation's doc comment for the local-mode back-fill
// behavior this triggers and for what the returned [models.Operation]
// actually contains.
func (p *Powerwall) SetReserve(ctx context.Context, level float64) (models.Operation, error) {
	return p.SetOperation(ctx, &level, nil)
}

// SetMode sets the battery's real operating mode (e.g.
// "self_consumption"). It is a thin wrapper over [Powerwall.SetOperation]
// with level left nil; see SetOperation's doc comment for the local-mode
// back-fill behavior this triggers and for what the returned
// [models.Operation] actually contains.
func (p *Powerwall) SetMode(ctx context.Context, mode string) (models.Operation, error) {
	return p.SetOperation(ctx, nil, &mode)
}

// backfillLocalOperation fills in whichever of level/mode the caller omitted
// using the CURRENT gateway state, but only when running in local mode - see
// SetOperation's doc comment for why cloud/FleetAPI/TEDAPI must never receive
// a back-filled payload. The read bypasses the poll cache: a stale cached
// value here would defeat the safeguard. If that read fails, an error is
// returned so the caller can refuse the write outright rather than fall back
// to a partial (and potentially destructive) payload.
func (p *Powerwall) backfillLocalOperation(
	ctx context.Context,
	level *float64,
	mode *string,
) (*float64, *string, error) {
	haveLevel := level != nil
	haveMode := mode != nil && *mode != ""
	if !p.IsLocal() || haveLevel == haveMode {
		return level, mode, nil
	}

	current, err := p.readOperation(ctx, true)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", models.ErrOperationBackfillFailed, err)
	}
	if !haveLevel {
		backfillLevel := current.BackupReservePercent
		level = &backfillLevel
	}
	if !haveMode {
		backfillMode := current.RealMode
		mode = &backfillMode
	}

	return level, mode, nil
}

// SetOperation sets the battery's backup reserve percentage and/or
// operating mode; pass nil for whichever of level/mode should be left
// unchanged. It returns [models.ErrReserveOutOfRange] if level is outside
// [0, 100] without attempting the write. On success, the returned
// [models.Operation] simply echoes back the level/mode values SetOperation
// sent (after any local-mode back-fill) - it is not a fresh read-back
// confirming what the gateway actually applied; call
// [Powerwall.GetReserveForced] or [Powerwall.Operation] afterward if that
// confirmation matters (notably, cloud/FleetAPI silently cap the requested
// reserve rather than rejecting an out-of-range write of their own).
//
// The local gateway's /api/operation endpoint is a full overwrite: any field
// omitted from the POST body is reset by the gateway rather than left
// unchanged. So when running in local mode and the caller supplies only one
// of level/mode, backfillLocalOperation reads back the current value of the
// other field and merges it into the payload before it is sent.
//
// Cloud, FleetAPI and TEDAPI apply BACKUP_RESERVE and OPERATION_MODE as two
// independent, asynchronous commands, so a partial payload must reach them
// unchanged: back-filling the omitted field there would race the other
// write. The back-fill therefore only ever applies in local mode.
func (p *Powerwall) SetOperation(ctx context.Context, level *float64, mode *string) (models.Operation, error) {
	if level != nil && (*level < 0 || *level > 100) {
		return models.Operation{}, models.ErrReserveOutOfRange
	}

	level, mode, err := p.backfillLocalOperation(ctx, level, mode)
	if err != nil {
		return models.Operation{}, err
	}

	payload := make(map[string]any)
	if level != nil {
		payload["backup_reserve_percent"] = *level
	}
	if mode != nil && *mode != "" {
		payload["real_mode"] = *mode
	}

	dinStr, _ := p.Din(ctx)

	res := p.Post(ctx, "/api/operation", payload, dinStr)
	if res == nil {
		return models.Operation{}, models.ErrSetOperationFailed
	}

	result := models.Operation{}
	if level != nil {
		result.BackupReservePercent = *level
	}
	if mode != nil {
		result.RealMode = *mode
	}

	return result, nil
}

// SetGridCharging enables or disables charging the battery from the grid.
// Only [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported]. On both success and backend failure the returned
// [models.Operation] simply echoes mode back in its GridCharging field - it
// is not a read-back of what the gateway actually applied, so a non-nil
// error must still be checked even though the result value looks
// "correct".
func (p *Powerwall) SetGridCharging(ctx context.Context, mode bool) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			_, err := p.cloud.SetGridCharging(ctx, mode)

			return models.Operation{GridCharging: mode}, err
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			_, err := p.fleetapi.SetGridCharging(ctx, mode)

			return models.Operation{GridCharging: mode}, err
		}
	default:
		// Remaining modes do not support this operation.
	}

	return models.Operation{}, ErrUnsupported
}

// GetGridCharging returns whether charging the battery from the grid is
// currently enabled, or an error if it could not be retrieved. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported].
func (p *Powerwall) GetGridCharging(ctx context.Context) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		val *bool
		err error
	)

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			val, err = p.cloud.GetGridCharging(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			val, err = p.fleetapi.GetGridCharging(ctx)
		} else {
			err = ErrNoClient
		}
	default:
		err = ErrUnsupported
	}

	if err != nil {
		return false, err
	}
	if val == nil {
		return false, ErrFieldMissing
	}

	return *val, nil
}

// SetGridExport sets the grid export mode, which must be one of
// "battery_ok", "pv_only", or "never" - any other value returns
// [models.ErrInvalidGridExportMode] without attempting the write. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported]. On both success and backend failure the returned
// [models.Operation] simply echoes mode back in its GridExport field - it
// is not a read-back of what the gateway actually applied, so a non-nil
// error must still be checked even though the result value looks
// "correct".
func (p *Powerwall) SetGridExport(ctx context.Context, mode string) (models.Operation, error) {
	if mode != "battery_ok" && mode != "pv_only" && mode != "never" {
		return models.Operation{}, fmt.Errorf(
			"%w: %s (must be battery_ok, pv_only, or never)",
			models.ErrInvalidGridExportMode,
			mode,
		)
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			_, err := p.cloud.SetGridExport(ctx, mode)

			return models.Operation{GridExport: mode}, err
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			_, err := p.fleetapi.SetGridExport(ctx, mode)

			return models.Operation{GridExport: mode}, err
		}
	default:
		// Remaining modes do not support this operation.
	}

	return models.Operation{}, ErrUnsupported
}

// GetGridExport returns the current grid export mode ("battery_ok",
// "pv_only", or "never"), or an error if it could not be retrieved. Only
// [ModeCloud] and [ModeFleetAPI] support this; other modes return
// [ErrUnsupported].
func (p *Powerwall) GetGridExport(ctx context.Context) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var (
		val *string
		err error
	)

	switch p.mode {
	case ModeCloud:
		if p.cloud != nil {
			val, err = p.cloud.GetGridExport(ctx)
		} else {
			err = ErrNoClient
		}
	case ModeFleetAPI:
		if p.fleetapi != nil {
			val, err = p.fleetapi.GetGridExport(ctx)
		} else {
			err = ErrNoClient
		}
	default:
		err = ErrUnsupported
	}

	if err != nil {
		return "", err
	}
	if val == nil {
		return "", ErrFieldMissing
	}

	return *val, nil
}

// ScheduleMaxBackup schedules a maximum backup event lasting
// durationSeconds (only durationSeconds[0] is read; the default when
// omitted is 3600, one hour). Only a TEDAPI-based connection ([ModeTEDAPI]
// or [ModeV1r]) supports this; other modes return [ErrUnsupported]. The
// returned [models.Operation] is always the zero value even on success -
// it carries no information about the scheduled event.
func (p *Powerwall) ScheduleMaxBackup(ctx context.Context, durationSeconds ...int) (models.Operation, error) {
	dur := defaultBackupDur
	if len(durationSeconds) > 0 {
		dur = durationSeconds[0]
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.ScheduleMaxBackup(ctx, dur)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// CancelMaxBackup cancels a previously scheduled maximum backup event
// ([Powerwall.ScheduleMaxBackup]). Only a TEDAPI-based connection
// ([ModeTEDAPI] or [ModeV1r]) supports this; other modes return
// [ErrUnsupported]. The returned [models.Operation] is always the zero
// value even on success.
func (p *Powerwall) CancelMaxBackup(ctx context.Context) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.CancelMaxBackup(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// GetBackupEvents returns the gateway's backup event history as a
// map[string]any decoded from the TEDAPI response - there is no typed model
// for this data yet. Only a TEDAPI-based connection ([ModeTEDAPI] or
// [ModeV1r]) supports this; other modes return [ErrUnsupported].
func (p *Powerwall) GetBackupEvents(ctx context.Context) (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		return p.tedapi.GetBackupEvents(ctx)
	}

	return nil, ErrUnsupported
}

// GoOffGrid disconnects the system from the grid, deliberately taking the
// site off-grid. confirm must be true or GoOffGrid refuses the request with
// [ErrOffGridConfirm] rather than acting on it - this guard exists because
// the operation is disruptive and hard to reverse instantly. Only a
// TEDAPI-based connection ([ModeTEDAPI] or [ModeV1r]) supports this; other
// modes return [ErrUnsupported]. The returned [models.Operation] is always
// the zero value even on success.
func (p *Powerwall) GoOffGrid(ctx context.Context, confirm bool) (models.Operation, error) {
	if !confirm {
		return models.Operation{}, ErrOffGridConfirm
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.GoOffGrid(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}

// ReconnectGrid reconnects the system to the grid after
// [Powerwall.GoOffGrid]. Only a TEDAPI-based connection ([ModeTEDAPI] or
// [ModeV1r]) supports this; other modes return [ErrUnsupported]. The
// returned [models.Operation] is always the zero value even on success.
func (p *Powerwall) ReconnectGrid(ctx context.Context) (models.Operation, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.tedapi != nil {
		_, err := p.tedapi.ReconnectGrid(ctx)

		return models.Operation{}, err
	}

	return models.Operation{}, ErrUnsupported
}
