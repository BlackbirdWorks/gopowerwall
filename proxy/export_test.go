package proxy

import "context"

// SafePWCall exposes safePWCall to proxy_test tests.
func (s *Server) SafePWCall(
	ctx context.Context,
	endpoint string,
	fn func() (any, error),
) (any, bool) {
	return s.safePWCall(ctx, endpoint, fn)
}
