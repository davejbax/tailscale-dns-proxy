package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/davejbax/tailscale-dns-proxy/internal/iplist"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

var (
	errTotalUpstreamTimeoutExceeded = fmt.Errorf("timeout exceeded for response from any upstream servers: %w", context.DeadlineExceeded)
	errAnswerNotIPRecord            = errors.New("answer is not an A or AAAA record")
	errNoTailscaleIPs               = errors.New("no tailscale IPs found for given address")
	errNotInterceptableQuestion     = errors.New("more than one question or question is not A/AAAA")
	errNoTailscaleIPsAfterFiltering = errors.New("we found tailscale IPs, but none were of the requested record type (IPv4 vs IPv6)")
)

type handler struct {
	server *Server
	client *dns.Client
}

// Convenience function to log when writing responses fails
func (h *handler) writeMsg(w dns.ResponseWriter, msg *dns.Msg) {
	err := w.WriteMsg(msg)
	if err != nil {
		h.server.logger.Warn("failed to write response to client", zap.Error(err))
	}
}

func (h *handler) intercept(ctx context.Context, w dns.ResponseWriter, req *dns.Msg) {
	resp, err := h.resolveUpstream(ctx, req)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			h.server.logger.Warn("upstream resolution failed: %w", zap.Error(err))
		}

		msg := new(dns.Msg)
		msg.SetRcode(req, dns.RcodeServerFailure)
		h.writeMsg(w, msg)
		return
	}

	newResp, err := h.doInterception(ctx, req, resp)
	if err != nil {
		h.server.logger.Debug("decided not to intercept",
			zap.NamedError("reason", err),
			zap.Any("req", req),
			zap.Any("resp", resp),
		)
		h.writeMsg(w, resp)
		return
	}

	h.writeMsg(w, newResp)
}

func (h *handler) doInterception(ctx context.Context, req *dns.Msg, resp *dns.Msg) (*dns.Msg, error) {
	// We can't deal with things that aren't A/AAAA queries and exactly one question.
	// I don't think anyone sends things with multiple questions anyway!
	if len(req.Question) != 1 || (req.Question[0].Qtype != dns.TypeA && req.Question[0].Qtype != dns.TypeAAAA) {
		return nil, errNotInterceptableQuestion
	}

	g, ctx := errgroup.WithContext(ctx)
	resolvedIPs := make(chan []net.IP)

	// XXX: This is almost certainly a premature parallelisation!!
	for _, answer := range resp.Answer {
		answer := answer

		g.Go(func() error {
			var ips []net.IP
			var err error
			if a, ok := answer.(*dns.A); ok {
				ips, err = h.server.resolver.GetTailscaleIPsByExternalIP(a.A)
				if err != nil {
					return fmt.Errorf("error getting tailscale IPs: %w", err)
				}

				// Generally, all answers will be the same type; if we get a
				// Tailscale IP that isn't the same type as our answer, we should
				// get rid of it, as we shouldn't return *mixed* A/AAAA answers
				// for a single A or AAAA query!
				ips = iplist.FilterIPv4Only(ips)
			} else if aaaa, ok := answer.(*dns.AAAA); ok {
				ips, err = h.server.resolver.GetTailscaleIPsByExternalIP(aaaa.AAAA)
				if err != nil {
					return fmt.Errorf("error getting tailscale IPs: %w", err)
				}
				ips = iplist.FilterIPv6Only(ips)
			} else {
				// We can't deal with non A/AAAA records, so bail out if we see one
				return errAnswerNotIPRecord
			}

			// If we get a record in the answers with no Tailscale IPs, we should
			// *not* return our intercepted response: if we had an answer with
			// Tailscale IPs as well, then we'd be returning a mixture of TS
			// & non-TS IPs, which is bad!
			if len(ips) == 0 {
				return errNoTailscaleIPs
			}

			select {
			case resolvedIPs <- ips:
			case <-ctx.Done():
				return ctx.Err()
			}

			return nil
		})
	}

	go func() {
		// Close the channel after the errgroup is finished so that the read
		// loop below doesn't hang!
		// We don't care about the error here: we check it outside of this goroutine
		_ = g.Wait()
		close(resolvedIPs)
	}()

	var tailscaleIPs []net.IP
	for resolvedIPSet := range resolvedIPs {
		tailscaleIPs = append(tailscaleIPs, resolvedIPSet...)
	}

	if err := g.Wait(); err != nil {
		if !errors.Is(err, errAnswerNotIPRecord) && !errors.Is(err, errNoTailscaleIPs) {
			h.server.logger.Error("unerror during wait for concurrent resolution of tailscale IPs", zap.Error(err))
		}

		return nil, err
	}

	msg := new(dns.Msg)
	msg.SetReply(req)

	var makeRR func(ip net.IP) dns.RR

	if req.Question[0].Qtype == dns.TypeA {
		tailscaleIPs = iplist.FilterIPv4Only(tailscaleIPs)
		makeRR = func(ip net.IP) dns.RR {
			rr := new(dns.A)
			rr.Hdr = dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300} // TODO: TTL config
			rr.A = ip
			return rr
		}
	} else {
		tailscaleIPs = iplist.FilterIPv6Only(tailscaleIPs)
		makeRR = func(ip net.IP) dns.RR {
			rr := new(dns.AAAA)
			rr.Hdr = dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 300} // TODO: TTL config
			rr.AAAA = ip
			return rr
		}
	}

	if len(tailscaleIPs) == 0 {
		return nil, errNoTailscaleIPsAfterFiltering
	}

	for _, ip := range tailscaleIPs {
		rr := makeRR(ip)
		msg.Answer = append(msg.Answer, rr)
	}

	return msg, nil
}

func (h *handler) forward(ctx context.Context, w dns.ResponseWriter, req *dns.Msg) {
	resp, err := h.resolveUpstream(ctx, req)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			h.server.logger.Warn("upstream resolution failed: %w", zap.Error(err))
		}

		resp = new(dns.Msg)
		resp.SetRcode(req, dns.RcodeServerFailure)
	}

	h.writeMsg(w, resp)
}

func (h *handler) resolveUpstream(ctx context.Context, req *dns.Msg) (*dns.Msg, error) {
	ctx, cancel := context.WithTimeoutCause(
		ctx,
		time.Duration(h.server.config.UpstreamTotalTimeoutSeconds)*time.Second,
		errTotalUpstreamTimeoutExceeded,
	)
	defer cancel()

	for _, upstream := range h.server.config.Upstreams {
		resp, _, err := h.client.ExchangeContext(ctx, req, upstream)
		if err != nil {
			// errTotalUpstreamTimeoutExceeded wraps a DeadlineExceeded, so we
			// should check for this first.
			if errors.Is(err, errTotalUpstreamTimeoutExceeded) {
				return nil, err
			} else if errors.Is(err, context.DeadlineExceeded) {
				// This specific upstream didn't work, but we still have time: try the next upstream
				continue
			}

			// We're not sure what the error is; bail out
			return nil, err
		}

		// We got a response! Return it
		return resp, nil
	}

	return nil, fmt.Errorf("all upstreams timed out (without exceeding total timeout): %w", context.DeadlineExceeded)
}
