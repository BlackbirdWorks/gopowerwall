package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/powerwall"
)

const (
	streamEventAggregates = "aggregates"
	streamEventSOE        = "soe"
	streamEventError      = "error"
	streamEventMeta       = "meta"
	streamEventKinds      = 4

	// streamRetry is the SSE reconnection delay (ms) advertised to clients.
	streamRetry = 3000
	// streamHeartbeat is how often an idle stream emits a ": ping" comment.
	streamHeartbeat = 15 * time.Second

	// DefaultStreamInterval is the poll interval used when PW_STREAM_INTERVAL
	// is unset.
	DefaultStreamInterval = time.Second
	// MinStreamInterval is the smallest PW_STREAM_INTERVAL honoured.
	MinStreamInterval = 250 * time.Millisecond
	// CloudStreamInterval is the interval floor in cloud and Fleet API
	// modes, where the data source is slow and rate limited.
	CloudStreamInterval = 30 * time.Second
	// maxStreamBackoff caps the exponential retry delay while fetches fail.
	maxStreamBackoff = 10 * time.Second

	headerFetchedAt = "X-PW-Fetched-At" // deliberately non-canonical, as documented
	pathAggregates  = "/aggregates"
	pathSOE         = "/soe"
	apiAggregates   = "/api/meters/aggregates"
	apiSOE          = "/api/system_status/soe"
)

var errStreamFetch = errors.New("gateway fetch failed")

// streamEvent is one SSE event. id is the fetch time in unix milliseconds.
type streamEvent struct {
	name string
	data []byte
	id   int64
}

// streamSub is one subscriber. Rather than a queue it keeps only the latest
// event per name, so a slow reader never blocks the poller and always
// catches up to the current state.
type streamSub struct {
	notify  chan struct{}
	pending map[string]streamEvent
	mu      sync.Mutex
}

func newStreamSub() *streamSub {
	return &streamSub{
		notify:  make(chan struct{}, 1),
		pending: make(map[string]streamEvent, streamEventKinds),
	}
}

func (sub *streamSub) offer(ev streamEvent) {
	sub.mu.Lock()
	sub.pending[ev.name] = ev
	sub.mu.Unlock()

	select {
	case sub.notify <- struct{}{}:
	default:
	}
}

// take returns and clears the pending events in a stable order.
func (sub *streamSub) take() []streamEvent {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	out := make([]streamEvent, 0, len(sub.pending))
	for _, name := range []string{streamEventMeta, streamEventError, streamEventAggregates, streamEventSOE} {
		if ev, ok := sub.pending[name]; ok {
			out = append(out, ev)
			delete(sub.pending, name)
		}
	}

	return out
}

// streamHub fans poller output out to subscribers and owns the poller's
// lifecycle: it runs only while at least one subscriber is registered.
type streamHub struct {
	subs      map[*streamSub]struct{}
	latest    map[string]streamEvent
	cancel    context.CancelFunc
	done      chan struct{}
	lastFetch time.Time
	lastErr   string
	mu        sync.Mutex
}

func newStreamHub() *streamHub {
	return &streamHub{
		subs:   make(map[*streamSub]struct{}),
		latest: make(map[string]streamEvent),
	}
}

// subscribe registers a subscriber seeded with the latest known state and
// starts the poller if it is the first one.
func (s *Server) subscribe() *streamSub {
	h := s.stream
	sub := newStreamSub()

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, ev := range h.latest {
		sub.offer(ev)
	}
	h.subs[sub] = struct{}{}

	if len(h.subs) == 1 {
		ctx, cancel := context.WithCancel(s.baseCtx)
		prev := h.done
		done := make(chan struct{})
		h.cancel = cancel
		h.done = done
		go func() {
			defer close(done)
			if prev != nil {
				<-prev // a previous poller may still be winding down
			}
			s.runStreamPoller(ctx)
		}()
	}

	return sub
}

// unsubscribe removes sub and stops the poller after the last one leaves.
func (s *Server) unsubscribe(sub *streamSub) {
	h := s.stream

	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.subs, sub)
	if len(h.subs) == 0 && h.cancel != nil {
		h.cancel()
		h.cancel = nil
		clear(h.latest)
		h.lastErr = ""
	}
}

// stopStream cancels the poller and waits for it to exit.
func (s *Server) stopStream() {
	h := s.stream

	h.mu.Lock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	done := h.done
	h.mu.Unlock()

	if done != nil {
		<-done
	}
}

func (h *streamHub) publish(ev streamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ev.name != streamEventMeta {
		h.latest[ev.name] = ev
	}
	for sub := range h.subs {
		sub.offer(ev)
	}
}

func (h *streamHub) clearError() {
	h.mu.Lock()
	delete(h.latest, streamEventError)
	h.mu.Unlock()
}

// streamIntervalFor returns the effective poll interval for the configured
// interval and connection mode: clamped to the minimum and raised to the
// cloud floor for cloud/Fleet API sources.
func streamIntervalFor(configured time.Duration, mode powerwall.ConnectionMode) time.Duration {
	if configured <= 0 {
		configured = DefaultStreamInterval
	}
	interval := max(configured, MinStreamInterval)
	if mode == powerwall.ModeCloud || mode == powerwall.ModeFleetAPI {
		interval = max(interval, CloudStreamInterval)
	}

	return interval
}

func (s *Server) streamMode() powerwall.ConnectionMode {
	if s.PW == nil {
		return powerwall.ModeUnknown
	}

	return s.PW.Mode()
}

func (s *Server) streamInterval() time.Duration {
	return streamIntervalFor(s.Config.StreamInterval, s.streamMode())
}

// streamSource names the data source for clients: "cloud" for the Tesla
// cloud and Fleet API (rate limited), "local" otherwise.
func (s *Server) streamSource() string {
	switch s.streamMode() {
	case powerwall.ModeCloud, powerwall.ModeFleetAPI:
		return "cloud"
	default:
		return "local"
	}
}

func (s *Server) streamMetaJSON() []byte {
	b, _ := json.Marshal(map[string]any{
		"source":      s.streamSource(),
		"interval_ms": s.streamInterval().Milliseconds(),
	})

	return b
}

func (s *Server) streamFetchTimeout(interval time.Duration) time.Duration {
	timeout := interval * 2 //nolint:mnd // twice the poll interval
	if t := s.Config.TimeoutDuration(); t > 0 {
		timeout = min(timeout, t)
	}

	return timeout
}

// streamFetchResult is the outcome of one gateway fetch. Either payload may
// be nil with err covering the failure; the other is still usable.
type streamFetchResult struct {
	err error
	agg []byte
	soe []byte
}

// streamFetchAggregates force-fetches the meter aggregates, bypassing (but
// repopulating) the backend response cache, and marshals them exactly as
// /aggregates does.
func (s *Server) streamFetchAggregates(ctx context.Context) ([]byte, error) {
	if len(s.PW.PollRaw(ctx, apiAggregates, powerwall.WithForce(true))) == 0 {
		return nil, fmt.Errorf("%w: aggregates", errStreamFetch)
	}

	// The forced fetch refreshed the backend cache, so this read is fresh and
	// gets the same corrections /aggregates applies.
	agg, err := s.PW.Aggregates(ctx, s.aggregatesOptions()...)
	if err != nil {
		return nil, fmt.Errorf("aggregates: %w", err)
	}

	return json.Marshal(agg)
}

// streamFetch force-fetches aggregates and SOE from the gateway.
func (s *Server) streamFetch(ctx context.Context) streamFetchResult {
	var res streamFetchResult
	var errs []error

	agg, err := s.streamFetchAggregates(ctx)
	if err != nil {
		errs = append(errs, err)
	} else {
		res.agg = agg
	}

	if str := s.PW.PollJSON(ctx, apiSOE, powerwall.WithForce(true)); str != "" {
		res.soe = []byte(str)
	} else {
		errs = append(errs, fmt.Errorf("%w: soe", errStreamFetch))
	}
	res.err = errors.Join(errs...)

	return res
}

// streamPollState is the poller's private state.
type streamPollState struct {
	lastMsg  string
	lastAgg  []byte
	lastSOE  []byte
	failures int
	failing  bool
}

// runStreamPoller polls until ctx is cancelled. It is the only goroutine
// fetching for the stream; overlapping fetches are coalesced by singleflight.
func (s *Server) runStreamPoller(ctx context.Context) {
	logger.Load(ctx).DebugContext(ctx, "stream poller started")
	defer logger.Load(ctx).DebugContext(ctx, "stream poller stopped")

	var st streamPollState
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		start := time.Now()
		interval := s.streamInterval()
		s.streamTick(ctx, interval, &st)

		timer.Reset(streamWait(interval, time.Since(start), st.failures))
	}
}

// streamWait returns the delay before the next fetch: exponential backoff
// while failing, otherwise the remainder of the current interval. A fetch
// that overran skips the missed ticks and waits for the next aligned one.
func streamWait(interval, elapsed time.Duration, failures int) time.Duration {
	switch {
	case failures > 0:
		return streamBackoff(interval, failures)
	case elapsed >= interval:
		return interval - elapsed%interval
	default:
		return interval - elapsed
	}
}

// streamBackoff doubles the delay per consecutive failure, from interval up
// to maxStreamBackoff (or interval itself, if that is already larger).
func streamBackoff(interval time.Duration, failures int) time.Duration {
	d := interval
	for range failures {
		d *= 2
		if d >= maxStreamBackoff {
			break
		}
	}

	return max(min(d, maxStreamBackoff), interval)
}

func (s *Server) streamTick(ctx context.Context, interval time.Duration, st *streamPollState) {
	fetchCtx, cancel := context.WithTimeout(ctx, s.streamFetchTimeout(interval))
	defer cancel()

	res, _, _ := s.streamGroup.Do("stream", func() (any, error) {
		return s.streamFetch(fetchCtx), nil
	})
	fr, _ := res.(streamFetchResult)
	if ctx.Err() != nil {
		return
	}

	now := time.Now()
	id := now.UnixMilli()
	h := s.stream

	if fr.err == nil {
		h.mu.Lock()
		h.lastFetch = now
		h.lastErr = ""
		h.mu.Unlock()
		if st.failing {
			st.failing = false
			h.clearError()
			// Tell clients the error cleared even if the data is unchanged.
			h.publish(streamEvent{name: streamEventMeta, data: s.streamMetaJSON(), id: id})
		}
		st.failures = 0
		st.lastMsg = ""
	} else {
		st.failures++
		msg := "gateway fetch failed"
		h.mu.Lock()
		h.lastErr = msg
		h.mu.Unlock()
		if !st.failing || msg != st.lastMsg {
			st.failing = true
			st.lastMsg = msg
			body, _ := json.Marshal(map[string]string{keyError: msg})
			h.publish(streamEvent{name: streamEventError, data: body, id: id})
		}
		logger.Load(ctx).DebugContext(ctx, "stream fetch failed", "error", fr.err, "failures", st.failures)
	}

	if fr.agg != nil {
		s.noteFetched(pathAggregates, now)
		s.PerfCache.Set(pathAggregates, string(fr.agg))
		if !bytes.Equal(fr.agg, st.lastAgg) {
			st.lastAgg = fr.agg
			h.publish(streamEvent{name: streamEventAggregates, data: fr.agg, id: id})
		}
	}
	if fr.soe != nil {
		s.noteFetched(pathSOE, now)
		if !bytes.Equal(fr.soe, st.lastSOE) {
			st.lastSOE = fr.soe
			h.publish(streamEvent{name: streamEventSOE, data: fr.soe, id: id})
		}
	}
}

// noteFetched records when an endpoint's data was last fetched from the
// gateway, for the X-PW-Fetched-At header.
func (s *Server) noteFetched(key string, t time.Time) {
	s.fetchedMu.Lock()
	s.fetched[key] = t
	s.fetchedMu.Unlock()
}

// fetchedAtHeader returns the unix-ms fetch time for key, or "" if unknown.
func (s *Server) fetchedAtHeader(key string) string {
	s.fetchedMu.RLock()
	t, ok := s.fetched[key]
	s.fetchedMu.RUnlock()
	if !ok {
		return ""
	}

	return strconv.FormatInt(t.UnixMilli(), 10)
}

// noteHandlerFetch records a fetch performed by a request handler whose
// result may have been served from the backend response cache. If the last
// recorded fetch is still within the cache window, that entry is assumed to
// be what was served and its time is kept.
func (s *Server) noteHandlerFetch(key string) {
	now := time.Now()
	s.fetchedMu.Lock()
	defer s.fetchedMu.Unlock()
	if prev, ok := s.fetched[key]; ok && now.Sub(prev) < s.Config.CacheExpireDuration() {
		return
	}
	s.fetched[key] = now
}

// setFetchedHeader sets X-PW-Fetched-At on w when the fetch time is known.
func (s *Server) setFetchedHeader(w http.ResponseWriter, key string) {
	if v := s.fetchedAtHeader(key); v != "" {
		w.Header()[headerFetchedAt] = []string{v} // non-canonical key keeps the documented spelling on the wire
	}
}

func parseStreamFilter(q string) (map[string]bool, bool) {
	want := map[string]bool{streamEventAggregates: true, streamEventSOE: true}
	if strings.TrimSpace(q) == "" {
		return want, true
	}

	clear(want)
	for name := range strings.SplitSeq(q, ",") {
		switch name = strings.TrimSpace(name); name {
		case streamEventAggregates, streamEventSOE:
			want[name] = true
		case "":
		default:
			return nil, false
		}
	}

	return want, len(want) > 0
}

func writeSSE(w io.Writer, ev streamEvent) error {
	var sb strings.Builder
	sb.WriteString("id: " + strconv.FormatInt(ev.id, 10) + "\nevent: " + ev.name + "\n")
	for line := range strings.SplitSeq(string(ev.data), "\n") {
		sb.WriteString("data: " + strings.TrimSuffix(line, "\r") + "\n")
	}
	sb.WriteString("\n")
	_, err := io.WriteString(w, sb.String())

	return err
}

// handleStream serves GET /stream as Server-Sent Events.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	want, ok := parseStreamFilter(r.URL.Query().Get("events"))
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			keyError: "events must be a comma-separated list of: aggregates, soe",
		})
		s.recordStats(ctx, "/stream", true, false)

		return
	}

	rc := http.NewResponseController(w)
	// The server-wide read/write timeouts would otherwise kill the stream.
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Content-Encoding", "identity") // never compress; keeps flushes immediate
	w.WriteHeader(http.StatusOK)
	s.recordStats(ctx, "/stream", false, false)

	sub := s.subscribe()
	defer s.unsubscribe(sub)

	if _, err := io.WriteString(w, "retry: "+strconv.Itoa(streamRetry)+"\n\n"); err != nil {
		return
	}
	meta := streamEvent{name: streamEventMeta, data: s.streamMetaJSON(), id: time.Now().UnixMilli()}
	if err := writeSSE(w, meta); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}

	s.serveStream(ctx, w, rc, sub, want)
}

// writeStreamEvents writes evs, dropping data events the client filtered out.
func writeStreamEvents(w io.Writer, evs []streamEvent, want map[string]bool) error {
	for _, ev := range evs {
		if (ev.name == streamEventAggregates || ev.name == streamEventSOE) && !want[ev.name] {
			continue
		}
		if err := writeSSE(w, ev); err != nil {
			return err
		}
	}

	return nil
}

// serveStream writes events to the client until it disconnects or a write
// fails, with a heartbeat comment every streamHeartbeat.
func (s *Server) serveStream(
	ctx context.Context,
	w http.ResponseWriter,
	rc *http.ResponseController,
	sub *streamSub,
	want map[string]bool,
) {
	ticker := time.NewTicker(streamHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
		case <-sub.notify:
			if err := writeStreamEvents(w, sub.take(), want); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

// handleStreamMeta serves GET /stream/meta.
func (s *Server) handleStreamMeta(ctx context.Context, w http.ResponseWriter) {
	h := s.stream
	h.mu.Lock()
	subscribers := len(h.subs)
	lastFetch := h.lastFetch
	lastErr := h.lastErr
	h.mu.Unlock()

	var fetchMS int64
	if !lastFetch.IsZero() {
		fetchMS = lastFetch.UnixMilli()
	}
	var errVal any
	if lastErr != "" {
		errVal = lastErr
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"subscribers":        subscribers,
		"interval_ms":        s.streamInterval().Milliseconds(),
		"last_fetch_unix_ms": fetchMS,
		"last_error":         errVal,
		"source":             s.streamSource(),
	})
	s.recordStats(ctx, "/stream/meta", false, false)
}
