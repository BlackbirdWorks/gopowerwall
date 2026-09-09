package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/blackbirdworks/gopowerwall/pkgs/logger"
	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

func main() {
	os.Exit(run())
}

func run() int {
	// Load .env before reading configuration, so a container can be
	// configured either by real environment variables (docker-compose) or a
	// mounted .env file (local testing). Real environment variables always
	// win: godotenv.Load never overwrites a variable that is already set. A
	// missing .env file is a silent no-op; only a malformed one is reported.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: failed to load .env: %v\n", err)
	}

	cfg := proxy.DefaultConfig()
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx := logger.Into(sigCtx, logger.New(os.Stderr, logger.LevelFor(cfg.DebugMode)))
	srv := proxy.NewServer(ctx, cfg, nil)

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

	if err := srv.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Load(ctx).ErrorContext(ctx, "server error", "error", err)

		return 1
	}

	return 0
}
