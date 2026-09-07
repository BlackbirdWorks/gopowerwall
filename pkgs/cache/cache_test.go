package cache_test

import (
	"testing"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/cache"
)

func TestResponseCacheTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup       func(c *cache.ResponseCache)
		expectedVal any
		name        string
		key         string
		expectFound bool
		expectNeg   bool
	}{
		{
			name: "cache hit on stored value",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/test", "value1")
			},
			key:         "/api/test",
			expectedVal: "value1",
			expectFound: true,
			expectNeg:   false,
		},
		{
			name:        "cache miss on missing key",
			setup:       func(_ *cache.ResponseCache) {},
			key:         "/api/missing",
			expectedVal: nil,
			expectFound: false,
			expectNeg:   false,
		},
		{
			name: "negative cache hit",
			setup: func(c *cache.ResponseCache) {
				c.SetNegative("/api/404", 500*time.Millisecond)
			},
			key:         "/api/404",
			expectedVal: nil,
			expectFound: true,
			expectNeg:   true,
		},
		{
			name: "invalidation removes mapped key",
			setup: func(c *cache.ResponseCache) {
				c.Set("/api/operation", "op-val")
				c.Invalidate("/api/operation")
			},
			key:         "/api/operation",
			expectedVal: nil,
			expectFound: false,
			expectNeg:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewResponseCache(1 * time.Second)
			defer c.Close()
			tt.setup(c)
			val, found, isNeg := c.Get(tt.key)
			if found != tt.expectFound {
				t.Errorf("found = %v, want %v", found, tt.expectFound)
			}
			if isNeg != tt.expectNeg {
				t.Errorf("isNegative = %v, want %v", isNeg, tt.expectNeg)
			}
			if val != tt.expectedVal {
				t.Errorf("val = %v, want %v", val, tt.expectedVal)
			}
		})
	}
}

func TestPerformanceCacheTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup       func(pc *cache.PerformanceCache)
		name        string
		key         string
		expectedVal string
		expectFound bool
	}{
		{
			name: "hit on valid key",
			setup: func(pc *cache.PerformanceCache) {
				pc.Set("/aggregates", `{"site": 100}`)
			},
			key:         "/aggregates",
			expectedVal: `{"site": 100}`,
			expectFound: true,
		},
		{
			name:        "miss on absent key",
			setup:       func(_ *cache.PerformanceCache) {},
			key:         "/absent",
			expectedVal: "",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pc := cache.NewPerformanceCache(1 * time.Second)
			defer pc.Close()
			tt.setup(pc)
			val, found := pc.Get(tt.key)
			if found != tt.expectFound {
				t.Errorf("found = %v, want %v", found, tt.expectFound)
			}
			if val != tt.expectedVal {
				t.Errorf("val = %v, want %v", val, tt.expectedVal)
			}
		})
	}
}

func TestDegradationCacheTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup         func(dc *cache.DegradationCache)
		name          string
		key           string
		expectVal     string
		expectExists  bool
		expectExpired bool
	}{
		{
			name: "fresh data in degradation cache",
			setup: func(dc *cache.DegradationCache) {
				dc.Set("/vitals", "good-data")
			},
			key:           "/vitals",
			expectVal:     "good-data",
			expectExists:  true,
			expectExpired: false,
		},
		{
			name:          "missing key",
			setup:         func(_ *cache.DegradationCache) {},
			key:           "/missing",
			expectVal:     "",
			expectExists:  false,
			expectExpired: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dc := cache.NewDegradationCache(1 * time.Second)
			tt.setup(dc)
			val, exists, expired := dc.Get(tt.key)
			if exists != tt.expectExists {
				t.Errorf("exists = %v, want %v", exists, tt.expectExists)
			}
			if expired != tt.expectExpired {
				t.Errorf("expired = %v, want %v", expired, tt.expectExpired)
			}
			if val != tt.expectVal {
				t.Errorf("val = %v, want %v", val, tt.expectVal)
			}
		})
	}
}
