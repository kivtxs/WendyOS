package services

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/wendylabsinc/wendy/go/internal/agent/mesh"
	"github.com/wendylabsinc/wendy/go/internal/agent/meshsession"
	"github.com/wendylabsinc/wendy/go/internal/shared/meshid"
)

// MeshAppDialer uses authenticated local mesh app sessions, or a principal-pinned
// LAN agent connection when no local mesh route exists. The existing cloud byte relay lacks a
// device-originated app admission protocol and cannot safely carry these
// app flows until that protocol and its service policy are implemented.
type MeshAppDialer struct{ mesh *MeshDialer }

var _ mesh.PeerDialer = (*MeshAppDialer)(nil)

func NewMeshAppDialer(d *MeshDialer) *MeshAppDialer { return &MeshAppDialer{mesh: d} }

func (a *MeshAppDialer) DialDevice(ctx context.Context, device meshid.Device, port uint16) (net.Conn, string, error) {
	if a == nil || a.mesh == nil {
		return nil, "", meshsession.ErrNoRoute
	}
	if conn, mode, err, own := a.mesh.dialSelf(ctx, device, port); own {
		return conn, mode, err
	}
	a.mesh.mu.Lock()
	dial := a.mesh.localDial
	a.mesh.mu.Unlock()
	started := a.mesh.now()
	var conn net.Conn
	err := meshsession.ErrNoRoute
	if dial != nil {
		conn, err = dial(ctx, device, port)
	}
	// A Babel route can be replaced while QUIC sends its first packet.
	// Retry one kernel route failure after convergence; never retry an
	// authenticated app-port denial or a missing signed route.
	if dial != nil && (errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH)) {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(time.Second):
			conn, err = dial(ctx, device, port)
		}
	}
	ms := float64(a.mesh.now().Sub(started).Milliseconds())
	if err != nil {
		a.mesh.metrics.RecordDial(device, "local-mesh", "error", ms)
		// A denial, identity failure, or transport failure is authoritative and
		// never triggers another admission path. Only absence of a local mesh
		// route permits the independently authenticated LAN application path.
		if errors.Is(err, meshsession.ErrNoRoute) {
			return a.dialLAN(ctx, device, port)
		}
		return nil, "local-mesh", err
	}
	a.mesh.metrics.RecordDial(device, "local-mesh", "ok", ms)
	return conn, "local-mesh", nil
}

func (a *MeshAppDialer) dialLAN(ctx context.Context, device meshid.Device, port uint16) (net.Conn, string, error) {
	address, found := a.mesh.lanAddr(ctx, device)
	if !found {
		return nil, "", meshsession.ErrNoRoute
	}
	started := a.mesh.now()
	conn, err := a.mesh.appLANDial(ctx, address, device, port)
	result := "ok"
	if err != nil {
		result = "error"
	}
	a.mesh.metrics.RecordDial(device, "lan-direct", result, float64(a.mesh.now().Sub(started).Milliseconds()))
	if err != nil {
		return nil, "", err
	}
	return conn, "lan-direct", nil
}
