package influx_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/gopowerwall/models"
	"github.com/blackbirdworks/gopowerwall/pkgs/influx"
)

var (
	errMockTimeout = errors.New("network timeout")
	errMockCollect = errors.New("collect failure")
)

type mockPointWriter struct {
	err    error
	points []*write.Point
	mu     sync.Mutex
}

func (m *mockPointWriter) WritePoint(_ context.Context, point ...*write.Point) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.points = append(m.points, point...)

	return nil
}

func (m *mockPointWriter) getPoints() []*write.Point {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]*write.Point, len(m.points))
	copy(copied, m.points)

	return copied
}

type validateTestCase struct {
	wantErr error
	name    string
	cfg     influx.Config
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	cases := []validateTestCase{
		{
			name: "valid configuration",
			cfg: influx.Config{
				URL:    "http://localhost:8086",
				Token:  "test-token",
				Org:    "test-org",
				Bucket: "test-bucket",
			},
			wantErr: nil,
		},
		{
			name: "missing url",
			cfg: influx.Config{
				Token:  "test-token",
				Org:    "test-org",
				Bucket: "test-bucket",
			},
			wantErr: influx.ErrMissingURL,
		},
		{
			name: "missing token",
			cfg: influx.Config{
				URL:    "http://localhost:8086",
				Org:    "test-org",
				Bucket: "test-bucket",
			},
			wantErr: influx.ErrMissingToken,
		},
		{
			name: "missing org",
			cfg: influx.Config{
				URL:    "http://localhost:8086",
				Token:  "test-token",
				Bucket: "test-bucket",
			},
			wantErr: influx.ErrMissingOrg,
		},
		{
			name: "missing bucket",
			cfg: influx.Config{
				URL:   "http://localhost:8086",
				Token: "test-token",
				Org:   "test-org",
			},
			wantErr: influx.ErrMissingBucket,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.cfg.Validate()
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}
			require.NoError(t, err)
		})
	}
}

type defaultsTestCase struct {
	name         string
	wantSiteName string
	cfg          influx.Config
	wantInterval time.Duration
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()

	cases := []defaultsTestCase{
		{
			name:         "empty defaults applied",
			cfg:          influx.Config{},
			wantInterval: influx.DefaultInterval,
			wantSiteName: "home",
		},
		{
			name: "custom values preserved",
			cfg: influx.Config{
				Interval: 10 * time.Second,
				SiteName: "vacation-cabin",
			},
			wantInterval: 10 * time.Second,
			wantSiteName: "vacation-cabin",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantInterval, tc.cfg.EffectiveInterval())
			assert.Equal(t, tc.wantSiteName, tc.cfg.EffectiveSiteName())
		})
	}
}

type buildPointsTestCase struct {
	name           string
	cfg            influx.Config
	snap           models.Snapshot
	agg            models.MetersAggregates
	wantGridStatus int
}

func TestBuildPoints(t *testing.T) {
	t.Parallel()

	cases := []buildPointsTestCase{
		{
			name: "grid connected",
			cfg: influx.Config{
				URL:      "http://localhost:8086",
				Token:    "token",
				Org:      "org",
				Bucket:   "bucket",
				SiteName: "main-house",
			},
			snap: models.Snapshot{
				Grid:            1500.0,
				Home:            2000.0,
				Solar:           3000.0,
				Battery:         -500.0,
				BatteryLevel:    85.5,
				GridConnected:   true,
				Reserve:         20.0,
				TimeRemaining:   4 * time.Hour,
				FullPackEnergy:  13500.0,
				EnergyRemaining: 11500.0,
			},
			agg: models.MetersAggregates{
				Site:    models.MeterReading{InstantPower: 1500.0},
				Solar:   models.MeterReading{InstantPower: 3000.0},
				Battery: models.MeterReading{InstantPower: -500.0},
				Load:    models.MeterReading{InstantPower: 2000.0},
			},
			wantGridStatus: 1,
		},
		{
			name: "islanded off-grid",
			cfg: influx.Config{
				URL:    "http://localhost:8086",
				Token:  "token",
				Org:    "org",
				Bucket: "bucket",
			},
			snap: models.Snapshot{
				Grid:          0.0,
				Home:          1200.0,
				Solar:         2000.0,
				Battery:       800.0,
				BatteryLevel:  95.0,
				GridConnected: false,
			},
			agg: models.MetersAggregates{
				Site:    models.MeterReading{InstantPower: 0.0},
				Solar:   models.MeterReading{InstantPower: 2000.0},
				Battery: models.MeterReading{InstantPower: 800.0},
				Load:    models.MeterReading{InstantPower: 1200.0},
			},
			wantGridStatus: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client, err := influx.NewWithWriter(tc.cfg, &mockPointWriter{})
			require.NoError(t, err)

			now := time.Now()
			pts := client.BuildPoints(tc.snap, tc.agg, now)
			require.Len(t, pts, 2)

			summary := pts[0]
			assert.Equal(t, influx.MeasurementSummary, summary.Name())
			assert.Equal(t, now.UnixNano(), summary.Time().UnixNano())

			aggPt := pts[1]
			assert.Equal(t, influx.MeasurementAggregates, aggPt.Name())
			assert.Equal(t, now.UnixNano(), aggPt.Time().UnixNano())
		})
	}
}

type writeTestCase struct {
	writerErr error
	name      string
	wantErr   bool
}

func TestClientWrite(t *testing.T) {
	t.Parallel()

	cases := []writeTestCase{
		{
			name:      "successful write",
			writerErr: nil,
			wantErr:   false,
		},
		{
			name:      "write failure surfaces error",
			writerErr: errMockTimeout,
			wantErr:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			writer := &mockPointWriter{err: tc.writerErr}
			client, err := influx.NewWithWriter(influx.Config{
				URL:    "http://localhost:8086",
				Token:  "token",
				Org:    "org",
				Bucket: "bucket",
			}, writer)
			require.NoError(t, err)

			writeErr := client.Write(t.Context(), models.Snapshot{}, models.MetersAggregates{})
			if tc.wantErr {
				require.Error(t, writeErr)

				return
			}
			require.NoError(t, writeErr)
			assert.Len(t, writer.getPoints(), 2)
		})
	}
}

type runTestCase struct {
	wantErr     error
	collectFunc influx.MetricCollector
	name        string
}

func TestClientRun(t *testing.T) {
	t.Parallel()

	cases := []runTestCase{
		{
			name:        "nil collector returns error",
			collectFunc: nil,
			wantErr:     influx.ErrNilCollector,
		},
		{
			name: "collects on interval until cancelled",
			collectFunc: func(_ context.Context) (models.Snapshot, models.MetersAggregates, error) {
				return models.Snapshot{Grid: 100}, models.MetersAggregates{}, nil
			},
			wantErr: context.Canceled,
		},
		{
			name: "collect error is handled gracefully",
			collectFunc: func(_ context.Context) (models.Snapshot, models.MetersAggregates, error) {
				return models.Snapshot{}, models.MetersAggregates{}, errMockCollect
			},
			wantErr: context.Canceled,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			writer := &mockPointWriter{}
			client, err := influx.NewWithWriter(influx.Config{
				URL:      "http://localhost:8086",
				Token:    "token",
				Org:      "org",
				Bucket:   "bucket",
				Interval: 10 * time.Millisecond,
			}, writer)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())
			if tc.collectFunc != nil {
				time.AfterFunc(35*time.Millisecond, cancel)
			} else {
				cancel()
			}

			runErr := client.Run(ctx, tc.collectFunc)
			require.ErrorIs(t, runErr, tc.wantErr)
		})
	}
}

type clientCloseTestCase struct {
	name string
	cfg  influx.Config
}

func TestClientNewAndClose(t *testing.T) {
	t.Parallel()

	cases := []clientCloseTestCase{
		{
			name: "construct and close real client",
			cfg: influx.Config{
				URL:    "http://localhost:8086",
				Token:  "token",
				Org:    "org",
				Bucket: "bucket",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := influx.New(tc.cfg)
			require.NoError(t, err)
			require.NotNil(t, c)
			c.Close()
		})
	}
}
