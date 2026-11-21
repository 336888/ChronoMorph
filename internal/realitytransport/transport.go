// Package realitytransport provides the same pinned transport to the native
// Windows client and the separately packaged Debian companion.
package realitytransport

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"slacker/internal/bridgeconfig"

	goreality "github.com/xtls/reality"
	xnet "github.com/xtls/xray-core/common/net"
	xreality "github.com/xtls/xray-core/transport/internet/reality"
)

func Dial(ctx context.Context, cfg bridgeconfig.Config) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 6 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, err
	}
	return Client(ctx, raw, cfg)
}

func Client(ctx context.Context, raw net.Conn, cfg bridgeconfig.Config) (net.Conn, error) {
	dest, err := xnet.ParseDestination("tcp:" + cfg.Address)
	if err != nil {
		raw.Close()
		return nil, err
	}
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(handshake, func() { raw.Close() })
	// Upstream expects ten optional path, concurrency and delay bounds.
	config := &xreality.Config{ServerName: cfg.Name, PublicKey: cfg.Key, ShortId: cfg.ShortID, Fingerprint: "chrome", SpiderX: "/", SpiderY: make([]int64, 10)}
	secured, err := xreality.UClient(raw, config, handshake, dest)
	if !stop() && err == nil {
		err = handshake.Err()
	}
	if err != nil {
		raw.Close()
		return nil, err
	}
	return secured, nil
}

// Serve owns its listener. The companion uses ready to acknowledge startup
// over its pipe; the desktop executable runs the same code directly.
func Serve(ctx context.Context, cfg bridgeconfig.Config, ready func() error) error {
	var listen net.ListenConfig
	ln, err := listen.Listen(ctx, "tcp", cfg.Address)
	if err != nil {
		return err
	}
	defer ln.Close()
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	conf := (&xreality.Config{Type: "tcp", Dest: cfg.Target, ServerNames: []string{cfg.Name}, PrivateKey: cfg.Key, ShortIds: [][]byte{cfg.ShortID}}).GetREALITYConfig()
	dialer := &net.Dialer{Timeout: 6 * time.Second}
	conf.DialContext = dialer.DialContext
	go goreality.DetectPostHandshakeRecordsLens(conf)
	if ready != nil {
		if err := ready(); err != nil {
			return err
		}
	}
	slots := make(chan struct{}, 256)
	for {
		raw, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) && ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			go func() {
				defer func() { <-slots }()
				defer raw.Close()
				stop := context.AfterFunc(ctx, func() { raw.Close() })
				defer stop()
				secured, err := xreality.Server(raw, conf)
				if err != nil {
					return
				}
				defer secured.Close()
				inner, err := dialer.DialContext(ctx, "tcp", cfg.Backend)
				if err != nil {
					return
				}
				defer inner.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(inner, secured); done <- struct{}{} }()
				go func() { _, _ = io.Copy(secured, inner); done <- struct{}{} }()
				<-done
			}()
		default:
			raw.Close()
		}
	}
}
