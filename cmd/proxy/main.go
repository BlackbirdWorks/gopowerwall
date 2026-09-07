package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

const defaultProxyTimeout = 15 * time.Second

func main() {
	cfg := proxy.DefaultConfig()
	ctx := logger.Into(context.Background(), logger.New(os.Stderr, logger.LevelFor(cfg.DebugMode)))
	srv := proxy.NewServer(ctx, cfg, nil)

	addr := fmt.Sprintf("%s:%d", cfg.BindAddress, cfg.Port)
	protocol := "HTTP"
	if cfg.HTTPSMode == "yes" {
		protocol = "HTTPS"
	}

	fmt.Fprintf(
		os.Stdout,
		"gopowerwall [%s] Proxy Server [%s] - %s Port %d\n",
		version.Version,
		proxy.Build,
		protocol,
		cfg.Port,
	)
	fmt.Fprintln(os.Stdout, "gopowerwall Proxy Started")

	server := &http.Server{
		Addr:              addr,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Handler:           srv,
		ReadHeaderTimeout: defaultProxyTimeout,
		ReadTimeout:       defaultProxyTimeout,
		WriteTimeout:      defaultProxyTimeout,
	}

	if cfg.HTTPSMode == "yes" {
		server.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		if err := server.ListenAndServeTLS("localhost.crt", "localhost.key"); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			logger.Load(ctx).ErrorContext(ctx, "HTTPS server error", "error", err)
			os.Exit(1)
		}

		return
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Load(ctx).ErrorContext(ctx, "server error", "error", err)
		os.Exit(1)
	}
}
