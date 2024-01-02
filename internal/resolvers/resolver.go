package resolvers

import (
	"context"
	"net"
	"time"
)

type Resolver interface {
	GetTailscaleIPsByExternalIP(ip net.IP) ([]net.IP, error)
}

type SelfResolver interface {
	GetProcessTailscaleIPs() ([]net.IP, error)
}

type Startable interface {
	Start(cancel <-chan struct{}) error
}

func StartWithTimeout(ctx context.Context, s Startable, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if err := s.Start(ctx.Done()); err != nil {
		return err
	}

	return nil
}
