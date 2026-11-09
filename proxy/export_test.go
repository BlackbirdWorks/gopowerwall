package proxy

import (
	"context"
	"time"

	"github.com/blackbirdworks/gopowerwall/powerwall"
)

// SafePWCall exposes safePWCall to proxy_test tests.
func (s *Server) SafePWCall(
	ctx context.Context,
	endpoint string,
	fn func() (any, error),
) (any, bool) {
	return s.safePWCall(ctx, endpoint, fn)
}

// StreamIntervalFor exposes streamIntervalFor to proxy_test tests.
func StreamIntervalFor(configured time.Duration, mode powerwall.ConnectionMode) time.Duration {
	return streamIntervalFor(configured, mode)
}

// StreamWait exposes streamWait to proxy_test tests.
func StreamWait(interval, elapsed time.Duration, failures int) time.Duration {
	return streamWait(interval, elapsed, failures)
}

// StreamPollerRunning reports whether the shared stream poller is active.
func (s *Server) StreamPollerRunning() bool {
	s.stream.mu.Lock()
	defer s.stream.mu.Unlock()

	return s.stream.cancel != nil
}
