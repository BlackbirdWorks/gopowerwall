package proxy_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/blackbirdworks/gopowerwall/powerwall"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

const streamWaitTimeout = 5 * time.Second

// fetchedAtKey is the canonical form Go clients see for X-PW-Fetched-At.
const fetchedAtKey = "X-Pw-Fetched-At"

type sseEvent struct {
	id    string
	event string
	data  string
}

// sseClient reads a /stream response, delivering parsed events and comments.
type sseClient struct {
	resp     *http.Response
	events   chan sseEvent
	comments chan string
	cancel   context.CancelFunc
}

func openStream(t *testing.T, base, query string) *sseClient {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/stream"+query, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	c := &sseClient{
		resp:     resp,
		events:   make(chan sseEvent, 256),
		comments: make(chan string, 256),
		cancel:   cancel,
	}
	t.Cleanup(c.close)

	go func() {
		defer close(c.events)
		sc := bufio.NewScanner(resp.Body)
		var cur sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if cur.event != "" {
					c.events <- cur
				}
				cur = sseEvent{}
			case strings.HasPrefix(line, ":"):
				c.comments <- line
			case strings.HasPrefix(line, "id: "):
				cur.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				cur.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data += strings.TrimPrefix(line, "data: ")
			}
		}
	}()

	return c
}

func (c *sseClient) close() {
	c.cancel()
	_ = c.resp.Body.Close()
}

func (c *sseClient) next(t *testing.T) sseEvent {
	t.Helper()

	select {
	case ev, ok := <-c.events:
		require.True(t, ok, "stream closed")

		return ev
	case <-time.After(streamWaitTimeout):
		require.FailNow(t, "timed out waiting for event")

		return sseEvent{}
	}
}

// nextNamed returns the next event with the given name, skipping others.
func (c *sseClient) nextNamed(t *testing.T, name string) sseEvent {
	t.Helper()

	for {
		if ev := c.next(t); ev.event == name {
			return ev
		}
	}
}

// expectNone asserts no event with the given name arrives within d.
func (c *sseClient) expectNone(t *testing.T, name string, d time.Duration) {
	t.Helper()

	timer := time.After(d)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			require.NotEqual(t, name, ev.event, "unexpected %s event: %s", name, ev.data)
		case <-timer:
			return
		}
	}
}

func streamConfig() proxy.Config {
	cfg := baseTestConfig()
	cfg.StreamInterval = proxy.MinStreamInterval

	return cfg
}

func httpGet(t *testing.T, url string) (*http.Response, string) {
	t.Helper()

	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp, string(body)
}

func TestStreamInitialEvents(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	ts := newProxyServer(t, streamConfig(), newLocalPowerwall(t, gw))

	c := openStream(t, ts.URL, "")
	assert.Equal(t, http.StatusOK, c.resp.StatusCode)
	assert.Equal(t, "text/event-stream", c.resp.Header.Get("Content-Type"))
	assert.Equal(t, "no-cache", c.resp.Header.Get("Cache-Control"))
	assert.Equal(t, "no", c.resp.Header.Get("X-Accel-Buffering"))

	meta := c.nextNamed(t, "meta")
	assert.JSONEq(t, `{"source":"local","interval_ms":250}`, meta.data)

	agg := c.nextNamed(t, "aggregates")
	soe := c.nextNamed(t, "soe")

	_, wantAgg := httpGet(t, ts.URL+"/aggregates")
	_, wantSOE := httpGet(t, ts.URL+"/soe")
	assert.JSONEq(t, wantAgg, agg.data)
	assert.JSONEq(t, wantSOE, soe.data)

	id, err := strconv.ParseInt(agg.id, 10, 64)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), time.UnixMilli(id), 5*time.Second)
	assert.Equal(t, agg.id, soe.id, "events from one tick share a fetch time")
}

func TestStreamEmitsOnlyOnChange(t *testing.T) {
	t.Parallel()

	var level atomic.Int64
	level.Store(50)
	var soeHits atomic.Int64
	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/system_status/soe": func(w http.ResponseWriter, _ *http.Request) {
			soeHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"percentage":`+strconv.FormatInt(level.Load(), 10)+`}`)
		},
	})
	ts := newProxyServer(t, streamConfig(), newLocalPowerwall(t, gw))

	c := openStream(t, ts.URL, "")
	first := c.nextNamed(t, "soe")
	assert.JSONEq(t, `{"percentage":50}`, first.data)

	// Several polls with an unchanged value must produce no further event.
	hits := soeHits.Load()
	c.expectNone(t, "soe", 900*time.Millisecond)
	assert.Greater(t, soeHits.Load(), hits, "poller should keep polling")

	level.Store(51)
	changed := c.nextNamed(t, "soe")
	assert.JSONEq(t, `{"percentage":51}`, changed.data)
}

func TestStreamFilter(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	ts := newProxyServer(t, streamConfig(), newLocalPowerwall(t, gw))

	c := openStream(t, ts.URL, "?events=soe")
	c.nextNamed(t, "soe")
	c.expectNone(t, "aggregates", 600*time.Millisecond)

	c2 := openStream(t, ts.URL, "?events=aggregates")
	c2.nextNamed(t, "aggregates")
	c2.expectNone(t, "soe", 600*time.Millisecond)

	resp, body := httpGet(t, ts.URL+"/stream?events=bogus")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body, "events must be")
}

func TestStreamPopulatesCaches(t *testing.T) {
	t.Parallel()

	var aggHits atomic.Int64
	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/meters/aggregates": func(w http.ResponseWriter, _ *http.Request) {
			aggHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "api.meters.aggregates.json"))
		},
	})
	cfg := streamConfig()
	cfg.CacheExpire = 60
	cfg.StreamInterval = time.Minute
	ts := newProxyServer(t, cfg, newLocalPowerwall(t, gw))

	c := openStream(t, ts.URL, "")
	c.nextNamed(t, "aggregates")
	hits := aggHits.Load()
	require.Positive(t, hits)

	httpGet(t, ts.URL+"/aggregates")
	assert.Equal(t, hits, aggHits.Load(), "/aggregates should be served from the cache the poller filled")
}

func TestFetchedAtHeader(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	ts := newProxyServer(t, baseTestConfig(), newLocalPowerwall(t, gw))

	for _, path := range []string{"/aggregates", "/soe"} {
		before := time.Now().Add(-time.Second).UnixMilli()
		resp, _ := httpGet(t, ts.URL+path)
		after := time.Now().Add(time.Second).UnixMilli()

		v := resp.Header.Get(fetchedAtKey)
		require.NotEmpty(t, v, path)
		ms, err := strconv.ParseInt(v, 10, 64)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, ms, before, path)
		assert.LessOrEqual(t, ms, after, path)

		// A cache hit reports the original fetch time, not "now".
		time.Sleep(20 * time.Millisecond)
		resp2, _ := httpGet(t, ts.URL+path)
		assert.Equal(t, v, resp2.Header.Get(fetchedAtKey), path)
	}
}

func TestStreamMetaEndpoint(t *testing.T) {
	t.Parallel()

	gw := newFakeGateway(t, nil)
	ts := newProxyServer(t, streamConfig(), newLocalPowerwall(t, gw))

	read := func() map[string]any {
		_, body := httpGet(t, ts.URL+"/stream/meta")
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &m))

		return m
	}

	m := read()
	assert.InDelta(t, 0, m["subscribers"], 0)
	assert.InDelta(t, 250, m["interval_ms"], 0)
	assert.Equal(t, "local", m["source"])
	assert.Nil(t, m["last_error"])

	c := openStream(t, ts.URL, "")
	c.nextNamed(t, "aggregates")
	m = read()
	assert.InDelta(t, 1, m["subscribers"], 0)
	assert.Positive(t, m["last_fetch_unix_ms"])
}

func TestStreamIntervalFloors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       powerwall.ConnectionMode
		configured time.Duration
		want       time.Duration
	}{
		{"default", powerwall.ModeLocal, 0, time.Second},
		{"configured", powerwall.ModeLocal, 2 * time.Second, 2 * time.Second},
		{"minimum", powerwall.ModeLocal, 10 * time.Millisecond, 250 * time.Millisecond},
		{"tedapi", powerwall.ModeTEDAPI, time.Second, time.Second},
		{"cloud floor", powerwall.ModeCloud, time.Second, 30 * time.Second},
		{"fleetapi floor", powerwall.ModeFleetAPI, 250 * time.Millisecond, 30 * time.Second},
		{"cloud above floor", powerwall.ModeCloud, time.Minute, time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, proxy.StreamIntervalFor(tt.configured, tt.mode))
		})
	}
}

func TestStreamWaitBackoff(t *testing.T) {
	t.Parallel()

	const iv = time.Second

	assert.Equal(t, 600*time.Millisecond, proxy.StreamWait(iv, 400*time.Millisecond, 0))
	// A fetch overrunning the interval skips ticks and aligns to the next one.
	assert.Equal(t, 500*time.Millisecond, proxy.StreamWait(iv, 2500*time.Millisecond, 0))
	// Exponential backoff while failing, capped at 10s.
	want := []int{2, 4, 8, 10, 10, 10}
	for i, w := range want {
		assert.Equal(t, time.Duration(w)*time.Second, proxy.StreamWait(iv, 0, i+1), "failure %d", i+1)
	}
	// An interval already above the cap is never shortened.
	assert.Equal(t, 30*time.Second, proxy.StreamWait(30*time.Second, 0, 3))
}

func TestStreamConfigInterval(t *testing.T) {
	cases := map[string]time.Duration{
		"":       proxy.DefaultStreamInterval,
		"2s":     2 * time.Second,
		"500":    500 * time.Millisecond,
		"10ms":   proxy.MinStreamInterval,
		"junk":   proxy.DefaultStreamInterval,
		"-5":     proxy.DefaultStreamInterval,
		"1500ms": 1500 * time.Millisecond,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if in != "" {
				t.Setenv("PW_STREAM_INTERVAL", in)
			}
			assert.Equal(t, want, proxy.DefaultConfig().StreamInterval)
		})
	}
}

func TestStreamErrorEventAndRecovery(t *testing.T) {
	t.Parallel()

	var fail atomic.Bool
	fail.Store(true)
	var soeHits atomic.Int64
	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/system_status/soe": func(w http.ResponseWriter, _ *http.Request) {
			soeHits.Add(1)
			if fail.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)

				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"percentage":42}`)
		},
	})
	cfg := streamConfig()
	cfg.CacheExpire = 0
	ts := newProxyServer(t, cfg, newLocalPowerwall(t, gw))

	c := openStream(t, ts.URL, "")
	errEv := c.nextNamed(t, "error")
	assert.JSONEq(t, `{"error":"gateway fetch failed"}`, errEv.data)

	// Still failing: no repeat error event, and polling backs off
	// (250ms interval would give >6 attempts in 2s; 250,500,1000... gives few).
	hits := soeHits.Load()
	c.expectNone(t, "error", 2*time.Second)
	assert.LessOrEqual(t, soeHits.Load()-hits, int64(4), "polling should back off while failing")

	_, metaBody := httpGet(t, ts.URL+"/stream/meta")
	assert.Contains(t, metaBody, `"last_error":"gateway fetch failed"`)

	fail.Store(false)
	soe := c.nextNamed(t, "soe")
	assert.JSONEq(t, `{"percentage":42}`, soe.data)
	c.nextNamed(t, "meta") // recovery marker
}

func TestStreamSingleflightNoOverlap(t *testing.T) {
	t.Parallel()

	var inFlight, maxInFlight, calls atomic.Int64
	gw := newFakeGateway(t, map[string]http.HandlerFunc{
		"/api/meters/aggregates": func(w http.ResponseWriter, _ *http.Request) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}
			calls.Add(1)
			time.Sleep(400 * time.Millisecond) // slower than the 250ms interval
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(readFixture(t, "api.meters.aggregates.json"))
		},
	})
	ts := newProxyServer(t, streamConfig(), newLocalPowerwall(t, gw))

	// Two subscribers share one poller.
	c1 := openStream(t, ts.URL, "?events=aggregates")
	c2 := openStream(t, ts.URL, "?events=aggregates")
	c1.nextNamed(t, "aggregates")
	c2.nextNamed(t, "aggregates")
	time.Sleep(2 * time.Second)

	assert.Equal(t, int64(1), maxInFlight.Load(), "fetches must never overlap")
	// Ticks are skipped rather than queued: 250ms ticks would allow ~10.
	assert.LessOrEqual(t, calls.Load(), int64(7))
	assert.GreaterOrEqual(t, calls.Load(), int64(3))
}

//nolint:paralleltest // goleak must not see other tests' goroutines
func TestStreamPollerLifecycle(t *testing.T) {
	gw := newFakeGateway(t, nil)
	pw := newLocalPowerwall(t, gw)
	srv := proxy.NewServer(t.Context(), streamConfig(), pw)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	assert.False(t, srv.StreamPollerRunning(), "no poller without subscribers")

	// Warm up lazily created goroutines (HTTP transports, caches) first so
	// the leak check only sees what the stream itself leaves behind.
	warm := openStream(t, ts.URL, "")
	warm.nextNamed(t, "aggregates")
	warm.close()
	require.Eventually(t, func() bool { return !srv.StreamPollerRunning() }, streamWaitTimeout, 10*time.Millisecond)
	http.DefaultClient.CloseIdleConnections()
	opt := goleak.IgnoreCurrent()

	c1 := openStream(t, ts.URL, "")
	c2 := openStream(t, ts.URL, "")
	c1.nextNamed(t, "aggregates")
	c2.nextNamed(t, "aggregates")
	assert.True(t, srv.StreamPollerRunning())

	c1.close()
	time.Sleep(100 * time.Millisecond)
	assert.True(t, srv.StreamPollerRunning(), "poller keeps running while a subscriber remains")

	c2.close()
	require.Eventually(t, func() bool { return !srv.StreamPollerRunning() }, streamWaitTimeout, 10*time.Millisecond)

	http.DefaultClient.CloseIdleConnections()
	ts.CloseClientConnections()
	require.NoError(t, goleak.Find(opt))

	// A new subscriber restarts it.
	c3 := openStream(t, ts.URL, "")
	c3.nextNamed(t, "aggregates")
	assert.True(t, srv.StreamPollerRunning())
}

// flushRecorder is a ResponseWriter safe for concurrent reads of its body.
type flushRecorder struct {
	hdr  http.Header
	body strings.Builder
	mu   sync.Mutex
}

func (f *flushRecorder) Header() http.Header { return f.hdr }
func (f *flushRecorder) WriteHeader(int)     {}
func (f *flushRecorder) Flush()              {}
func (f *flushRecorder) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.body.Write(p)
}

func (f *flushRecorder) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.body.String()
}

func TestStreamHeartbeat(t *testing.T) {
	t.Parallel()

	// Built outside the bubble: the client's cache janitors are not part of
	// what is under test and would otherwise outlive it.
	pw := newDisconnectedPowerwall(t)
	synctest.Test(t, func(t *testing.T) {
		cfg := streamConfig()
		cfg.StreamInterval = time.Hour // keep the poller quiet; only heartbeats matter
		srv := proxy.NewServer(t.Context(), cfg, pw)

		ctx, cancel := context.WithCancel(t.Context())
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/stream", nil)
		rec := &flushRecorder{hdr: http.Header{}}

		done := make(chan struct{})
		go func() {
			defer close(done)
			srv.ServeHTTP(rec, req)
		}()

		time.Sleep(14 * time.Second)
		synctest.Wait()
		assert.NotContains(t, rec.String(), ": ping")
		assert.Contains(t, rec.String(), "retry: 3000")

		time.Sleep(2 * time.Second)
		synctest.Wait()
		assert.Equal(t, 1, strings.Count(rec.String(), ": ping\n\n"))

		time.Sleep(15 * time.Second)
		synctest.Wait()
		assert.Equal(t, 2, strings.Count(rec.String(), ": ping\n\n"))

		cancel()
		<-done
		require.NoError(t, srv.Close(t.Context())) // also stops the poller
	})
}
