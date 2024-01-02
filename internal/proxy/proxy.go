package proxy

import (
	"context"
	"time"

	"github.com/davejbax/tailscale-dns-proxy/internal/resolvers"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type ProxyServer struct {
	logger               *zap.Logger
	ctx                  context.Context
	upstreams            []string
	totalUpstreamTimeout time.Duration
	servers              map[string]*dns.Server
	resolver             resolvers.Resolver
}

func NewProxyServer(ctx context.Context, logger *zap.Logger, resolver resolvers.Resolver, config *Config) *ProxyServer {
	server := &ProxyServer{
		logger:               logger,
		ctx:                  ctx,
		upstreams:            config.Upstreams,
		totalUpstreamTimeout: time.Duration(config.UpstreamTotalTimeoutSeconds) * time.Second,
		resolver:             resolver,
		servers:              make(map[string]*dns.Server),
	}

	// We want to be as transparent as possible, so we forward TCP packets when
	// we get a TCP request, and UDP packets when we get a UDP request.
	for _, protocol := range []string{"tcp", "udp"} {
		client := &dns.Client{
			Net:          protocol,
			DialTimeout:  time.Duration(config.UpstreamDialTimeoutSeconds) * time.Second,
			ReadTimeout:  time.Duration(config.UpstreamReadTimeoutSeconds) * time.Second,
			WriteTimeout: time.Duration(config.UpstreamWriteTimeoutSeconds) * time.Second,
		}

		handler := &handler{
			server: server,
			client: client,
		}
		mux := dns.NewServeMux()
		for _, pattern := range config.ProxyZones {
			mux.HandleFunc(pattern, handler.intercept)
		}

		// ServeMux uses the most-specific handler that matches the zone, so our
		// 'default' handler is the root zone (.)
		mux.HandleFunc(".", handler.forward)

		server.servers[protocol] = &dns.Server{
			Addr:    config.ListenAddr,
			Net:     protocol,
			Handler: mux,
		}
	}

	return server
}

func (s *ProxyServer) ListenAndServe() error {
	g, ctx := errgroup.WithContext(s.ctx)
	g.Go(func() error {
		return s.servers["tcp"].ListenAndServe()
	})
	g.Go(func() error {
		return s.servers["udp"].ListenAndServe()
	})

	go func() {
		<-ctx.Done()
		s.logger.Info("Context done: shutting down servers")
		s.servers["tcp"].Shutdown()
		s.servers["udp"].Shutdown()
	}()

	return g.Wait()
}
