package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/davejbax/tailscale-dns-proxy/internal/ipstealer"
	"github.com/davejbax/tailscale-dns-proxy/internal/proxy"
	"github.com/davejbax/tailscale-dns-proxy/internal/resolvers"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	if err := mainE(); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() (*zap.Logger, error) {
	debug := flag.Bool("debug", false, "Enable debug output")
	level := zap.LevelFlag("level", zapcore.WarnLevel, "Verbosity level of logs")
	flag.Parse()

	var cfg zap.Config
	if *debug {
		cfg = zap.NewDevelopmentConfig()
	} else {
		cfg = zap.NewProductionConfig()
	}

	cfg.Level.SetLevel(*level)
	return cfg.Build()
}

func mainE() error {
	logger, err := parseFlags()
	if err != nil {
		return fmt.Errorf("failed to parse flags and/or create logger: %w", err)
	}

	defer logger.Sync()

	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	resolver, err := cfg.Resolver.Create()
	if err != nil {
		return fmt.Errorf("failed to create Tailscale IP resolver: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Some resolvers need to be started and initialised before we do anything,
	// and involve background processing. Do that now.
	if startable, ok := resolver.(resolvers.Startable); ok {
		logger.Info("starting resolver", zap.Any("resolver", resolver))
		if err := resolvers.StartWithTimeout(ctx, startable, time.Duration(cfg.Resolver.StartTimeoutSeconds)*time.Second); err != nil {
			return fmt.Errorf("failed to start resolver: %w", err)
		}
	}

	// Start the IP stealer now
	// TODO: build in some verification process so that we don't steal an IP if
	// we aren't actually up
	if cfg.IPStealer.Enabled {
		logger.Info("starting IP stealer")
		stealer := ipstealer.New(ctx, logger, &cfg.IPStealer.Config)
		ticker := stealer.Start()
		defer ticker.Stop()
	}

	proxy := proxy.NewProxyServer(ctx, logger, resolver, &cfg.Proxy)
	logger.Info("starting proxy server")
	return proxy.ListenAndServe()
}
