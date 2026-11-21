package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A client may send its first application bytes in the same write as CONNECT.
func TestProxyForwardsBufferedPayload(t *testing.T) {
	origin, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	go func() {
		conn, err := origin.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		b := make([]byte, 5)
		if _, err = io.ReadFull(conn, b); err == nil {
			_, _ = conn.Write(b)
		}
	}()
	client, proxy := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	go handleMixedProxy(proxy, nil, NewRouter(nil, nil))
	addr := origin.Addr().String()
	if _, err := fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nhello", addr, addr); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("HTTP %d", response.StatusCode)
	}
	b := make([]byte, 5)
	if _, err = io.ReadFull(reader, b); err != nil || string(b) != "hello" {
		t.Fatalf("first payload = %q, err = %v", b, err)
	}
}
