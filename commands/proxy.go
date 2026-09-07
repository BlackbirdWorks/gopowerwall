package commands

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/blackbirdworks/gopowerwall/pkgs/version"
	"github.com/blackbirdworks/gopowerwall/proxy"
)

const defaultProxyTimeout = 15 * time.Second

// ProxyCmd runs the Powerwall HTTP proxy service.
type ProxyCmd struct {
	BindAddress string `default:"0.0.0.0" env:"PW_BIND_ADDRESS" help:"Bind address"      name:"bind"`
	ConnectionFlags
	Port int `default:"8675"    env:"PW_PORT"         help:"Port to listen on" name:"port"`
}

// Run executes the proxy command.
func (c *ProxyCmd) Run(cmdCtx *Context) error {
	ctx := c.WithLogger(cmdCtx.Context)
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
		os.Stdout,
		"gopowerwall [%s] Proxy Server [%s] starting on http://%s\n",
		version.Version,
		proxy.Build,
		addr,
	)

	server := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: defaultProxyTimeout,
		ReadTimeout:       defaultProxyTimeout,
		WriteTimeout:      defaultProxyTimeout,
	}

	return server.ListenAndServe()
}
