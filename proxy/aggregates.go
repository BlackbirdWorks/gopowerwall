package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/blackbirdworks/gopowerwall/powerwall"
)

var errNoData = errors.New("no data")

const (
	csvV1BufSize = 96
	csvV2BufSize = 128
)

// aggregatesOptions builds the [powerwall.AggregatesOption] values
// carrying this server's configured corrections, so every route deriving
// power figures from meter data (aggregates, CSV, JSON) applies the same
// site-zero threshold and negative-solar correction.
func (s *Server) aggregatesOptions() []powerwall.AggregatesOption {
	return []powerwall.AggregatesOption{
		powerwall.WithSiteZeroThreshold(float64(s.Config.SiteZeroThreshold)),
		powerwall.WithNegativeSolarCorrection(!s.Config.NegSolar),
	}
}

func (s *Server) generateAggregates(ctx context.Context) (string, error) {
	agg, ok := s.safePWCall(ctx, "/aggregates", func() (any, error) {
		return s.PW.Aggregates(ctx, s.aggregatesOptions()...)
	})
	if !ok {
		return "", errNoData
	}

	b, err := json.Marshal(agg)

	return string(b), err
}

func (s *Server) formatV2CSVRow(
	includeHeaders bool,
	grid, home, solar, battery, batLevel float64,
	gridStatus int,
	reserve int,
) string {
	var sb strings.Builder
	sb.Grow(csvV2BufSize)
	if includeHeaders {
		sb.WriteString("Grid,Home,Solar,Battery,BatteryLevel,GridStatus,Reserve\n")
	}
	fmt.Fprintf(&sb, "%0.2f,%0.2f,%0.2f,%0.2f,%0.2f,%d,%d\n",
		grid, home, solar, battery, batLevel, gridStatus, reserve)

	return sb.String()
}

func (s *Server) formatV1CSVRow(
	includeHeaders bool,
	grid, home, solar, battery, batLevel float64,
) string {
	var sb strings.Builder
	sb.Grow(csvV1BufSize)
	if includeHeaders {
		sb.WriteString("Grid,Home,Solar,Battery,BatteryLevel\n")
	}
	fmt.Fprintf(&sb, "%0.2f,%0.2f,%0.2f,%0.2f,%0.2f\n",
		grid, home, solar, battery, batLevel)

	return sb.String()
}

func (s *Server) generateCSV(ctx context.Context, isV2, includeHeaders bool) (string, error) {
	snap := s.PW.Snapshot(ctx, s.aggregatesOptions()...)

	if isV2 {
		gridStatus := 0
		if snap.GridConnected {
			gridStatus = 1
		}

		return s.formatV2CSVRow(
			includeHeaders,
			snap.Grid,
			snap.Home,
			snap.Solar,
			snap.Battery,
			snap.BatteryLevel,
			gridStatus,
			int(snap.Reserve),
		), nil
	}

	return s.formatV1CSVRow(includeHeaders, snap.Grid, snap.Home, snap.Solar, snap.Battery, snap.BatteryLevel), nil
}

func (s *Server) handleCSVRoute(w http.ResponseWriter, r *http.Request, reqPath string) {
	ctx := r.Context()
	isV2 := strings.HasPrefix(reqPath, "/csv/v2")
	includeHeaders := strings.Contains(r.URL.RawQuery, "headers") || strings.Contains(reqPath, "headers")
	cacheKey := "/csv"
	if isV2 {
		cacheKey = "/csv/v2"
	}
	if includeHeaders {
		cacheKey += "_headers"
	}

	msg, ok := s.cachedRouteHandler(ctx, cacheKey, func(ctx context.Context) (string, error) {
		return s.generateCSV(ctx, isV2, includeHeaders)
	})
	s.respond(ctx, w, reqPath, "text/plain; charset=utf-8", msg, ok)
}
