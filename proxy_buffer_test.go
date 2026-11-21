package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

func TestHTTPOriginDoesNotReceiveProxyHeaders(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Proxy-Connection") != "" ||
			r.Header.Get("X-Hop-Only") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" {
			http.Error(w, "received a proxy-only header", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer origin-only" {
			http.Error(w, "origin credential missing", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, r.URL.Path)
	}))
	defer origin.Close()
	client, proxy := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	go handleMixedProxy(proxy, nil, NewRouter(nil, nil))
	reader := bufio.NewReader(client)
	for _, path := range []string{"/first", "/second"} {
		_, err := fmt.Fprintf(client, "GET %s%s HTTP/1.1\r\nHost: %s\r\nConnection: X-Hop-Only\r\nX-Hop-Only: private\r\nProxy-Connection: keep-alive\r\nProxy-Authorization: Basic test\r\nForwarded: for=192.0.2.1\r\nX-Forwarded-For: 192.0.2.1\r\nAuthorization: Bearer origin-only\r\n\r\n", origin.URL, path, origin.Listener.Addr())
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || string(body) != path {
			t.Fatalf("%s: status %d body %q error %v", path, resp.StatusCode, body, err)
		}
	}
}

func TestHTTPUpgradeStillTunnelsData(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "echo" || r.Header.Get("Proxy-Authorization") != "" {
			http.Error(w, "upgrade headers invalid", http.StatusBadRequest)
			return
		}
		c, br, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = br.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		if err = br.Flush(); err != nil {
			return
		}
		var payload [4]byte
		if _, err = io.ReadFull(br, payload[:]); err == nil {
			_, _ = c.Write(payload[:])
		}
	}))
	defer origin.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if c, err := listener.Accept(); err == nil {
			handleMixedProxy(c, nil, NewRouter(nil, nil))
		}
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintf(client, "GET %s/ HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: echo\r\nProxy-Authorization: Basic example\r\n\r\n", origin.URL, origin.Listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(client)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("upgrade response: %v %v", resp, err)
	}
	if _, err = io.WriteString(client, "echo"); err != nil {
		t.Fatal(err)
	}
	var reply [4]byte
	if _, err = io.ReadFull(r, reply[:]); err != nil || string(reply[:]) != "echo" {
		t.Fatalf("upgraded data %q, err=%v", reply, err)
	}
}
