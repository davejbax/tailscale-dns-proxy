package proxy

import (
	"context"
	"time"

	"github.com/davejbax/tailscale-dns-proxy/internal/resolvers"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Server struct {
	logger   *zap.Logger
	config   *Config
	resolver resolvers.Resolver
}

func New(logger *zap.Logger, resolver resolvers.Resolver, config *Config) *Server {
	server := &Server{
		logger:   logger,
		config:   config,
		resolver: resolver,
	}

	// We want to be as transparent as possible, so we forward TCP packets when
	// we get a TCP request, and UDP packets when we get a UDP request.

	return server
}

func (s *Server) makeDNSServer(ctx context.Context, protocol string) *dns.Server {
	client := &dns.Client{
		Net:          protocol,
		DialTimeout:  time.Duration(s.config.UpstreamDialTimeoutSeconds) * time.Second,
		ReadTimeout:  time.Duration(s.config.UpstreamReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(s.config.UpstreamWriteTimeoutSeconds) * time.Second,
	}

	handler := &handler{
		server: s,
		client: client,
	}
	mux := dns.NewServeMux()
	for _, pattern := range s.config.ProxyZones {
		mux.HandleFunc(pattern, func(w dns.ResponseWriter, m *dns.Msg) { handler.intercept(ctx, w, m) })
	}

	// ServeMux uses the most-specific handler that matches the zone, so our
	// 'default' handler is the root zone (.)
	mux.HandleFunc(".", func(w dns.ResponseWriter, m *dns.Msg) { handler.forward(ctx, w, m) })

	return &dns.Server{
		Addr:    s.config.ListenAddr,
		Net:     protocol,
		Handler: mux,
	}
}

func (s *Server) ListenAndServeContext(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	tcp := s.makeDNSServer(ctx, "tcp")
	udp := s.makeDNSServer(ctx, "udp")

	g.Go(func() error {
		return tcp.ListenAndServe()
	})
	g.Go(func() error {
		return udp.ListenAndServe()
	})

	go func() {
		<-ctx.Done()
		s.logger.Info("Context done: shutting down servers")
		if err := tcp.Shutdown(); err != nil {
			s.logger.Warn("failed to shutdown TCP DNS server", zap.Error(err))
		}

		if err := udp.Shutdown(); err != nil {
			s.logger.Warn("failed to shutdown UDP DNS server", zap.Error(err))
		}
	}()

	return g.Wait()
}
