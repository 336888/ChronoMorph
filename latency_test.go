package main

import (
	"net"
	"testing"

	"github.com/xtaci/smux"
)

func TestLatencyUsesAuthenticatedServerStream(t *testing.T) {
	local, remote := net.Pipe()
	server, err := smux.Server(remote, smuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := smux.Client(local, smuxConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	go func() {
		stream, err := server.AcceptStream()
		if err == nil {
			handleServerStream(stream)
		}
	}()
	ms, err := MeasureTunnelLatency(&ClientMux{sess: client})
	if ms < 0 || err != nil {
		t.Fatalf("server ping failed: %v", err)
	}
}
