package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

// ProxyCmd runs the Powerwall HTTP proxy service.
type ProxyCmd struct {
	BindAddress string `default:"0.0.0.0" env:"PW_BIND_ADDRESS" help:"Bind address"      name:"bind"`
	ConnectionFlags
	Port int `default:"8675"    env:"PW_PORT"         help:"Port to listen on" name:"port"`
}

// Run executes the proxy command.
func (c *ProxyCmd) Run(cmdCtx *Context) error {
	sigCtx, stop := signal.NotifyContext(cmdCtx.Context, os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx := c.WithLogger(sigCtx)
	cfg := proxy.DefaultConfig()
	if c.BindAddress != "" {
		cfg.BindAddress = c.BindAddress
	}
	if c.Port != 0 {
		cfg.Port = c.Port
	}
	if c.Host != "" {
		cfg.Host = c.Host
	}
	if c.Password != "" {
		cfg.Password = c.Password
	}
	if c.GwPwd != "" {
		cfg.GwPwd = c.GwPwd
	}
	if c.RsaKeyPath != "" {
		cfg.RsaKeyPath = c.RsaKeyPath
	}
	if c.AuthPath != "" {
		cfg.AuthPath = c.AuthPath
	}

	srv := proxy.NewServer(ctx, cfg, nil)
	addr := fmt.Sprintf("%s:%d", cfg.BindAddress, cfg.Port)
	fmt.Fprintf(
		cmdCtx.Output(),
		"gopowerwall [%s] Proxy Server [%s] starting on http://%s\n",
		version.Version,
		proxy.Build,
		addr,
	)

	if err := srv.Start(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
