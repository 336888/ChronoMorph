//go:build cm_xray

// This command builds in the pinned Xray-core source tree, using upstream's
// go.mod and go.sum. Only the transport handshake lives in this process.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	goreality "github.com/xtls/reality"
	bridgeconfig "github.com/xtls/xray-core/cmbridge/wire"
	xnet "github.com/xtls/xray-core/common/net"
	xreality "github.com/xtls/xray-core/transport/internet/reality"
)

func run(output io.Writer) error {
	input := bufio.NewReader(os.Stdin)
	line, err := input.ReadSlice('\n')
	if err != nil {
		return errors.New("invalid bridge configuration")
	}
	var cfg bridgeconfig.Config
	if err := json.Unmarshal(line, &cfg); err != nil || len(cfg.Key) != 32 || len(cfg.ShortID) != 8 {
		return errors.New("invalid bridge identity")
	}
	switch cfg.Mode {
	case "client":
		raw, err := net.DialTimeout("tcp", cfg.Address, 6*time.Second)
		if err != nil {
			return err
		}
		defer raw.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dest, err := xnet.ParseDestination("tcp:" + cfg.Address)
		if err != nil {
			return err
		}
		// Xray expects ten optional path, concurrency and delay bounds.
		conf := &xreality.Config{ServerName: cfg.Name, PublicKey: cfg.Key, ShortId: cfg.ShortID, Fingerprint: "chrome", SpiderX: "/", SpiderY: make([]int64, 10)}
		secured, err := xreality.UClient(raw, conf, ctx, dest)
		if err != nil {
			return err
		}
		defer secured.Close()
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(secured, input); done <- struct{}{} }()
		go func() { _, _ = io.Copy(output, secured); done <- struct{}{} }()
		<-done
		return nil
	case "server":
		ln, err := net.Listen("tcp", cfg.Address)
		if err != nil {
			return err
		}
		defer ln.Close()
		conf := (&xreality.Config{Type: "tcp", Dest: cfg.Target, ServerNames: []string{cfg.Name}, PrivateKey: cfg.Key, ShortIds: [][]byte{cfg.ShortID}}).GetREALITYConfig()
		dialer := &net.Dialer{Timeout: 6 * time.Second}
		conf.DialContext = dialer.DialContext
		// The parent owns stdin for our lifetime. This also stops the service
		// after an unexpected parent exit on platforms without parent signals.
		go func() { _, _ = io.Copy(io.Discard, input); ln.Close() }()
		go goreality.DetectPostHandshakeRecordsLens(conf)
		if _, err := output.Write([]byte("READY\n")); err != nil {
			return err
		}
		slots := make(chan struct{}, 256)
		for {
			raw, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				return err
			}
			select {
			case slots <- struct{}{}:
			default:
				raw.Close()
				continue
			}
			go func() {
				defer func() { <-slots }()
				defer raw.Close()
				secured, err := xreality.Server(raw, conf)
				if err != nil {
					return
				}
				defer secured.Close()
				inner, err := net.DialTimeout("tcp", cfg.Backend, 4*time.Second)
				if err != nil {
					return
				}
				defer inner.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(inner, secured); done <- struct{}{} }()
				go func() { _, _ = io.Copy(secured, inner); done <- struct{}{} }()
				<-done
			}()
		}
	default:
		return errors.New("unknown bridge mode")
	}
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("ChronoMorph REALITY bridge 1 (XTLS/Xray-core v26.11.9)")
		return
	}
	// Keep upstream startup and diagnostic messages off the binary data pipe.
	output := os.Stdout
	os.Stdout = os.Stderr
	if err := run(output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
