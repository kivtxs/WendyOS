package services

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"syscall"

	"github.com/wendylabsinc/wendy/go/internal/shared/discovery"
	"github.com/wendylabsinc/wendy/go/internal/shared/meshid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type applicationLANAddress struct {
	address        netip.AddrPort
	applicationTLS bool
}

// Keep discovery alive during connection attempts: a sibling address can arrive
// after the first exact-principal sighting, or while that address times out.
// Address hints never authorize traffic; dial must pin the full TLS principal
// and perform live app-port admission before returning success.
func dialApplicationLAN[T any](ctx context.Context, d *MeshDialer, peer meshid.Device, datagrams bool, dial func(context.Context, applicationLANAddress) (T, error)) (T, bool, error) {
	var zero T
	browse, cancel := context.WithTimeout(ctx, lanDiscoveryBudget)
	events := d.lanEvents(browse, discovery.StreamOptions{OnBackendError: func(err error) { d.logger.Debug("LAN peer discovery backend unavailable", zap.Error(err)) }})
	defer func() {
		cancel()
		for range events {
		}
	}()
	attempted := map[applicationLANAddress]bool{}
	found := false
	var last error
	for event := range events {
		if event.Kind != discovery.LANFound && event.Kind != discovery.LANUpdated {
			continue
		}
		device := event.Device
		if device.PrincipalURI != peer.Principal() || !device.IsMTLS {
			continue
		}
		applicationTLS := device.AppTLSPort != 0
		if datagrams && !applicationTLS {
			continue
		}
		port := int(device.AppTLSPort)
		if !applicationTLS {
			port = device.Port
		}
		if port <= 0 || port > 65535 {
			continue
		}
		addresses := append([]string{device.IPAddress}, device.Addresses...)
		addresses = append(addresses, device.WiredAddresses...)
		for _, address := range addresses {
			ip, err := netip.ParseAddr(address)
			if err != nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			candidate := applicationLANAddress{netip.AddrPortFrom(ip, uint16(port)), applicationTLS}
			if attempted[candidate] {
				continue
			}
			attempted[candidate] = true
			found = true
			attempt, stop := context.WithTimeout(browse, lanBudget)
			result, err := dial(attempt, candidate)
			stop()
			if err == nil {
				return result, true, nil
			}
			last = err
			if !retryableLANAddressError(err) {
				return zero, true, err
			}
			if ctx.Err() != nil {
				return zero, true, ctx.Err()
			}
		}
	}
	if ctx.Err() != nil {
		return zero, found, ctx.Err()
	}
	return zero, found, last
}

// Retry only failures that describe an unavailable network address. In
// particular, a TLS identity/certificate failure or a live app-port refusal
// must never be retried through another address or admission protocol.
func retryableLANAddressError(err error) bool {
	if errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ETIMEDOUT) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if status.Code(err) == codes.DeadlineExceeded {
		return true
	}
	var network *net.OpError
	return errors.As(err, &network) && network.Timeout()
}

func (a applicationLANAddress) String() string {
	return a.address.String()
}
