// Package influx provides direct export of Powerwall telemetry to InfluxDB v2.
package influx

import (
	"context"
	"errors"
	"fmt"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api/write"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
)

const (
	// DefaultInterval is the default period between metric flushes.
	DefaultInterval = 30 * time.Second

	// MeasurementSummary is the InfluxDB measurement name for high-level telemetry.
	MeasurementSummary = "powerwall_summary"

	// MeasurementAggregates is the InfluxDB measurement name for meter readings.
	MeasurementAggregates = "powerwall_aggregates"

	defaultSiteName = "home"
)

var (
	// ErrMissingURL indicates InfluxDB URL was not provided.
	ErrMissingURL = errors.New("missing influxdb url")
	// ErrMissingToken indicates InfluxDB token was not provided.
	ErrMissingToken = errors.New("missing influxdb token")
	// ErrMissingOrg indicates InfluxDB organization was not provided.
	ErrMissingOrg = errors.New("missing influxdb org")
	// ErrMissingBucket indicates InfluxDB bucket was not provided.
	ErrMissingBucket = errors.New("missing influxdb bucket")
	// ErrNilCollector indicates metric collector callback is nil.
	ErrNilCollector = errors.New("nil metric collector")
)

// Config configures InfluxDB client connection and export parameters.
type Config struct {
	URL      string
	Token    string
	Org      string
	Bucket   string
	SiteName string
	Interval time.Duration
}

// Validate checks required configuration fields.
func (c *Config) Validate() error {
	if c.URL == "" {
		return ErrMissingURL
	}
	if c.Token == "" {
		return ErrMissingToken
	}
	if c.Org == "" {
		return ErrMissingOrg
	}
	if c.Bucket == "" {
		return ErrMissingBucket
	}

	return nil
}

// EffectiveInterval returns the configured interval or DefaultInterval if unset.
func (c *Config) EffectiveInterval() time.Duration {
	if c.Interval <= 0 {
		return DefaultInterval
	}

	return c.Interval
}

// EffectiveSiteName returns the configured site tag or defaultSiteName.
func (c *Config) EffectiveSiteName() string {
	if c.SiteName == "" {
		return defaultSiteName
	}

	return c.SiteName
}

// PointWriter abstracts the InfluxDB write API for testability.
type PointWriter interface {
	WritePoint(ctx context.Context, point ...*write.Point) error
}

// Client manages publishing telemetry points to InfluxDB.
type Client struct {
	rawClient influxdb2.Client
	writer    PointWriter
	cfg       Config
}

// New creates an InfluxDB exporter Client connected to the target database.
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	raw := influxdb2.NewClient(cfg.URL, cfg.Token)
	blockingAPI := raw.WriteAPIBlocking(cfg.Org, cfg.Bucket)

	return &Client{
		rawClient: raw,
		writer:    blockingAPI,
		cfg:       cfg,
	}, nil
}

// NewWithWriter constructs an exporter with a custom PointWriter (useful for testing).
func NewWithWriter(cfg Config, writer PointWriter) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &Client{
		writer: writer,
		cfg:    cfg,
	}, nil
}

// Close closes the underlying InfluxDB client.
func (c *Client) Close() {
	if c.rawClient != nil {
		c.rawClient.Close()
	}
}

// BuildPoints converts Powerwall telemetry into InfluxDB line protocol points.
func (c *Client) BuildPoints(snap models.Snapshot, agg models.MetersAggregates, timestamp time.Time) []*write.Point {
	site := c.cfg.EffectiveSiteName()

	gridStatusInt := 0
	if snap.GridConnected {
		gridStatusInt = 1
	}

	summaryPoint := influxdb2.NewPoint(
		MeasurementSummary,
		map[string]string{"site": site},
		map[string]any{
			"grid":                 snap.Grid,
			"home":                 snap.Home,
			"solar":                snap.Solar,
			"battery":              snap.Battery,
			"soe":                  snap.BatteryLevel,
			"grid_status":          gridStatusInt,
			"reserve":              snap.Reserve,
			"time_remaining_hours": snap.TimeRemaining.Hours(),
			"full_pack_energy":     snap.FullPackEnergy,
			"energy_remaining":     snap.EnergyRemaining,
		},
		timestamp,
	)

	aggregatesPoint := influxdb2.NewPoint(
		MeasurementAggregates,
		map[string]string{"site": site},
		map[string]any{
			"site_instant_power":    agg.Site.InstantPower,
			"solar_instant_power":   agg.Solar.InstantPower,
			"battery_instant_power": agg.Battery.InstantPower,
			"load_instant_power":    agg.Load.InstantPower,
		},
		timestamp,
	)

	return []*write.Point{summaryPoint, aggregatesPoint}
}

// Write writes snapshot and aggregates points to InfluxDB.
func (c *Client) Write(ctx context.Context, snap models.Snapshot, agg models.MetersAggregates) error {
	pts := c.BuildPoints(snap, agg, time.Now())
	if err := c.writer.WritePoint(ctx, pts...); err != nil {
		return fmt.Errorf("influx write point: %w", err)
	}

	return nil
}

// MetricCollector defines a function returning current Snapshot and MetersAggregates.
type MetricCollector func(ctx context.Context) (models.Snapshot, models.MetersAggregates, error)

// Run starts a periodic collection loop, publishing metrics until context cancellation.
func (c *Client) Run(ctx context.Context, collect MetricCollector) error {
	if collect == nil {
		return ErrNilCollector
	}

	ticker := time.NewTicker(c.cfg.EffectiveInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.flushOnce(ctx, collect)
		}
	}
}

func (c *Client) flushOnce(ctx context.Context, collect MetricCollector) {
	snap, agg, err := collect(ctx)
	if err != nil {
		logger.Load(ctx).ErrorContext(ctx, "influx exporter collect failed", "error", err)

		return
	}

	if writeErr := c.Write(ctx, snap, agg); writeErr != nil {
		logger.Load(ctx).ErrorContext(ctx, "influx exporter write failed", "error", writeErr)
	}
}
