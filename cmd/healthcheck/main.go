// Command healthcheck is a minimal binary for use as a Docker HEALTHCHECK
// against the proxy's scratch-based image, which has no shell or HTTP
// utilities (curl, wget) available to probe an endpoint. It performs a
// single GET against the proxy's own /health route and exits 0 on a 2xx
// response, 1 otherwise.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"
)

const (
	defaultPort    = 8675
	requestTimeout = 3 * time.Second
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 1
	}

	return 0
}

// port returns PW_PORT from the environment, matching the port the proxy
// itself binds to, falling back to the proxy's own default.
func port() int {
	if v, ok := os.LookupEnv("PW_PORT"); ok {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}

	return defaultPort
}
