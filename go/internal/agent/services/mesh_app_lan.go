package services

import (
	"context"
	"net"
	"sync"

	"github.com/wendylabsinc/wendy/go/internal/agent/mtls"
	"github.com/wendylabsinc/wendy/go/internal/shared/meshappstream"
	"github.com/wendylabsinc/wendy/go/internal/shared/meshid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// meshDialAppLAN connects to the discovered agent, not to an unverified app
// socket. Discovery only selects an address: TLS pins the full device principal,
// then MeshDial authorizes the requested port against the live app registry.
func (d *MeshDialer) meshDialAppLAN(ctx context.Context, hostport string, device meshid.Device, port uint16) (net.Conn, error) {
	identity := d.identity()
	config, err := mtls.NewClientTLSConfigExpectingDevice(identity.certPEM, identity.chainPEM, identity.keyPEM, d.logger, device)
	if err != nil {
		return nil, err
	}
	cc, err := grpc.NewClient(meshDialTarget,
		grpc.WithTransportCredentials(credentials.NewTLS(config)),
		grpc.WithContextDialer(meshDialContextDialer(hostport)))
	if err != nil {
		return nil, err
	}
	conn, err := meshappstream.Dial(ctx, cc, port)
	if err != nil {
		_ = cc.Close()
		return nil, err
	}
	return &ownedAppConnection{Conn: conn, closeTransport: cc.Close}, nil
}

// Each LAN dial owns its agent connection. The wrapped application stream keeps
// the same half-close behavior while Close also releases the HTTP/2 transport.
type ownedAppConnection struct {
	net.Conn
	closeTransport func() error
	once           sync.Once
	closeErr       error
}

func (c *ownedAppConnection) Close() error {
	c.once.Do(func() {
		c.closeErr = c.Conn.Close()
		if err := c.closeTransport(); c.closeErr == nil {
			c.closeErr = err
		}
	})
	return c.closeErr
}

func (c *ownedAppConnection) CloseWrite() error {
	if half, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return half.CloseWrite()
	}
	return c.Close()
}
