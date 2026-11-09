package main

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/wendylabsinc/wendy/go/internal/agent/configpartition"
	"github.com/wendylabsinc/wendy/go/internal/agent/localmesh"
	"github.com/wendylabsinc/wendy/go/internal/agent/meshsession"
	"github.com/wendylabsinc/wendy/go/internal/agent/udpmessage"
	"go.uber.org/zap"
)

// startApplicationEndpoints keeps LAN and Cloud application ingress independent
// of carrier participation. These servers receive the app-only registry, never
// the native-agent-service wrapper used by routed management.
func startApplicationEndpoints(ctx context.Context, credentials *localmesh.Credentials, ingress meshsession.Authorizer, messages *udpmessage.Manager, allowLAN bool, logger *zap.Logger, launch func(func())) (*meshsession.ApplicationListener, func()) {
	ctx, cancel := context.WithCancel(ctx)
	cloud, err := meshsession.NewServer(credentials, ingress, messages)
	if err != nil {
		logger.Error("Cloud application admission unavailable", zap.Error(err))
		cancel()
		return nil, func() {}
	}
	listener := meshsession.NewApplicationListener()
	var lan *meshsession.Server
	var endpointMu sync.Mutex
	stopped := false
	retire := func() {
		endpointMu.Lock()
		defer endpointMu.Unlock()
		if stopped {
			return
		}
		stopped = true
		cancel()
		_ = cloud.Close()
		if lan != nil {
			_ = lan.Close()
		}
		if allowLAN {
			configpartition.UpdateAvahiApplicationTLS(logger, 0)
		}
	}
	launch(func() {
		defer cloud.Close()
		if err := cloud.ServeTLS(ctx, listener); err != nil && ctx.Err() == nil {
			logger.Error("Cloud application admission stopped", zap.Error(err))
		}
	})
	if !allowLAN {
		return listener, retire
	}
	// Reconcile a stale advertisement before binding. A failed bind must not
	// advertise an old or unrelated process as this agent's app endpoint.
	configpartition.UpdateAvahiApplicationTLS(logger, 0)
	lan, err = meshsession.NewServer(credentials, ingress, messages)
	if err != nil {
		logger.Error("LAN application admission unavailable", zap.Error(err))
		return listener, retire
	}
	lanListener, err := net.Listen("tcp", fmt.Sprintf(":%d", meshsession.ApplicationLANPort))
	if err != nil {
		_ = lan.Close()
		logger.Error("LAN application admission could not bind", zap.Error(err))
		return listener, retire
	}
	launch(func() {
		defer lan.Close()
		defer configpartition.UpdateAvahiApplicationTLS(logger, 0)
		if err := lan.ServeTLS(ctx, lanListener, func() {
			endpointMu.Lock()
			defer endpointMu.Unlock()
			if !stopped {
				configpartition.UpdateAvahiApplicationTLS(logger, meshsession.ApplicationLANPort)
			}
		}); err != nil && ctx.Err() == nil {
			logger.Error("LAN application admission stopped", zap.Error(err))
		}
	})
	return listener, retire
}
