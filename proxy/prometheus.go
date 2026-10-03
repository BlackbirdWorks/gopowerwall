package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/blackbirdworks/gopowerwall/powerwall"
)

const metricsBufInitialCap = 2048

func (s *Server) writeProxyMetrics(b *strings.Builder) {
	s.statsMu.RLock()
	uptimeSec := int(time.Since(s.StartTime).Seconds())
	gets := s.statsGets
	posts := s.statsPost
	errs := s.statsErr
	timeouts := s.statsTime
	s.statsMu.RUnlock()

	b.WriteString("# HELP powerwall_proxy_uptime_seconds Proxy uptime in seconds.\n")
	b.WriteString("# TYPE powerwall_proxy_uptime_seconds gauge\n")
	fmt.Fprintf(b, "powerwall_proxy_uptime_seconds %d\n\n", uptimeSec)

	b.WriteString("# HELP powerwall_proxy_requests_total Total number of HTTP requests processed by the proxy.\n")
	b.WriteString("# TYPE powerwall_proxy_requests_total counter\n")
	fmt.Fprintf(b, "powerwall_proxy_requests_total{method=\"GET\"} %d\n", gets)
	fmt.Fprintf(b, "powerwall_proxy_requests_total{method=\"POST\"} %d\n\n", posts)

	b.WriteString("# HELP powerwall_proxy_errors_total Total number of proxy request errors.\n")
	b.WriteString("# TYPE powerwall_proxy_errors_total counter\n")
	fmt.Fprintf(b, "powerwall_proxy_errors_total %d\n\n", errs)

	b.WriteString("# HELP powerwall_proxy_timeouts_total Total number of proxy timeouts.\n")
	b.WriteString("# TYPE powerwall_proxy_timeouts_total counter\n")
	fmt.Fprintf(b, "powerwall_proxy_timeouts_total %d\n\n", timeouts)
}

func writePowerwallMetrics(ctx context.Context, b *strings.Builder, pw *powerwall.Powerwall) {
	snap := pw.Snapshot(ctx)

	b.WriteString("# HELP powerwall_solar_power_watts Current solar power production in watts.\n")
	b.WriteString("# TYPE powerwall_solar_power_watts gauge\n")
	fmt.Fprintf(b, "powerwall_solar_power_watts %.2f\n\n", snap.Solar)

	b.WriteString("# HELP powerwall_battery_power_watts Current battery power in watts.\n")
	b.WriteString("# TYPE powerwall_battery_power_watts gauge\n")
	fmt.Fprintf(b, "powerwall_battery_power_watts %.2f\n\n", snap.Battery)

	b.WriteString("# HELP powerwall_grid_power_watts Current grid power in watts.\n")
	b.WriteString("# TYPE powerwall_grid_power_watts gauge\n")
	fmt.Fprintf(b, "powerwall_grid_power_watts %.2f\n\n", snap.Grid)

	b.WriteString("# HELP powerwall_load_power_watts Current home load power in watts.\n")
	b.WriteString("# TYPE powerwall_load_power_watts gauge\n")
	fmt.Fprintf(b, "powerwall_load_power_watts %.2f\n\n", snap.Home)

	b.WriteString("# HELP powerwall_battery_charge_percent Battery state of charge percentage.\n")
	b.WriteString("# TYPE powerwall_battery_charge_percent gauge\n")
	fmt.Fprintf(b, "powerwall_battery_charge_percent %.2f\n\n", snap.BatteryLevel)

	if snap.Reserve > 0 {
		b.WriteString("# HELP powerwall_backup_reserve_percent Backup reserve percentage setting.\n")
		b.WriteString("# TYPE powerwall_backup_reserve_percent gauge\n")
		fmt.Fprintf(b, "powerwall_backup_reserve_percent %.2f\n\n", snap.Reserve)
	}

	if gridStatus, gsErr := pw.GridStatusNumeric(ctx); gsErr == nil {
		b.WriteString(
			"# HELP powerwall_grid_status Grid connectivity status (1 = connected, 0 = off grid / islanded).\n",
		)
		b.WriteString("# TYPE powerwall_grid_status gauge\n")
		fmt.Fprintf(b, "powerwall_grid_status %d\n\n", gridStatus)
	}

	writeTemperatureMetrics(ctx, b, pw)
}

func writeTemperatureMetrics(ctx context.Context, b *strings.Builder, pw *powerwall.Powerwall) {
	temps := pw.Temps(ctx).Temps
	if len(temps) == 0 {
		return
	}

	b.WriteString("# HELP powerwall_temperature_celsius Temperature readings in degrees Celsius.\n")
	b.WriteString("# TYPE powerwall_temperature_celsius gauge\n")
	keys := make([]string, 0, len(temps))
	for k := range temps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "powerwall_temperature_celsius{device=\"%s\"} %.2f\n", k, temps[k])
	}
	b.WriteString("\n")
}

func (s *Server) handleMetrics(ctx context.Context, w http.ResponseWriter) {
	var b strings.Builder
	b.Grow(metricsBufInitialCap)

	s.writeProxyMetrics(&b)
	if s.PW != nil {
		writePowerwallMetrics(ctx, &b, s.PW)
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, b.String())
	s.recordStats(ctx, "/metrics", false, false)
}
