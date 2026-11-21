package main

import (
	"context"
	"net"

	"slacker/internal/bridgeconfig"
	"slacker/internal/realitytransport"
)

func checkRealitySupport() error { return nil }

func (cm *ClientMux) dialRealityTransport(ctx context.Context, cfg bridgeconfig.Config) (net.Conn, error) {
	raw, err := cm.dialNodeTCP(ctx, cfg.Address)
	if err != nil {
		return nil, err
	}
	return realitytransport.Client(ctx, raw, cfg)
}

func runRealityListener(ctx context.Context, cfg bridgeconfig.Config) error {
	return realitytransport.Serve(ctx, cfg, nil)
}
