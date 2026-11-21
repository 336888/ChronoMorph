package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebsiteTransport(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/article" || r.Header.Get("Proxy-Authorization") != "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "published article")
	}))
	defer origin.Close()
	addr, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer addr.Close()
	// httptest supplies a signing pair valid for example.com and loopback.
	fixture := httptest.NewTLSServer(nil)
	pair := fixture.TLS.Certificates[0]
	fixture.Close()
	dir := t.TempDir()
	keyBytes, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	web := WebsiteConfig{Name: "example.com", Listen: addr.Addr().String(), Origin: origin.URL, CertFile: filepath.Join(dir, "chain.pem"), KeyFile: filepath.Join(dir, "key.pem")}
	for file, block := range map[string]*pem.Block{web.CertFile: {Type: "CERTIFICATE", Bytes: pair.Certificate[0]}, web.KeyFile: {Type: "PRIVATE KEY", Bytes: keyBytes}} {
		if err := os.WriteFile(file, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	roots := x509.NewCertPool()
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots.AddCert(leaf)
	master := strings.Repeat("ab", 32)
	users, err := NewMultiUserEngine([]UserEntry{{Name: "test", KeyHex: master}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := web.server(users)
	if err != nil {
		t.Fatal(err)
	}
	captured := make(chan string, 8)
	handler := server.Handler
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			captured <- r.Header.Get("Proxy-Authorization")
		}
		handler.ServeHTTP(w, r)
	})
	go server.ServeTLS(addr, "", "")
	defer server.Close()
	port := addr.Addr().(*net.TCPAddr).Port
	authority := net.JoinHostPort(web.Name, strconv.Itoa(port))
	node := TLSNode{WebName: web.Name, TLSPort: port}
	p := ShareProfile{TLSNode: node, Name: "site", ServerIP: "127.0.0.1", MasterKey: master}
	link, err := p.encode()
	if err != nil {
		t.Fatal(err)
	}
	q, err := DecodeShareLink(link)
	if err != nil || *q != p || !strings.HasPrefix(link, "cm+https://") {
		t.Fatal("invalid website share link", err)
	}
	if _, err = DecodeShareLink(strings.Replace(link, "cm+https://", "cm+tls://", 1)); err == nil {
		t.Fatal("accepted mismatched transport")
	}
	for _, bad := range []TLSNode{{WebName: "127.0.0.1", TLSPort: 443}, {WebName: "x.test", TLSPort: 443, UDPPort: 443}, {WebName: "x.test", TLSPort: 443, TLSPin: strings.Repeat("aa", 32)}} {
		if bad.validate() == nil {
			t.Fatal("accepted ambiguous website profile", bad)
		}
	}
	base := &tls.Config{ServerName: web.Name, RootCAs: roots, GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		t.Error("public website requested a client certificate")
		return &tls.Certificate{}, nil
	}}
	tr := &http.Transport{TLSClientConfig: base.Clone(), ForceAttemptHTTP2: true}
	defer tr.CloseIdleConnections()
	browser := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	req, err := http.NewRequest("GET", "https://"+addr.Addr().String()+"/article", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = authority
	req.Header.Set("Proxy-Authorization", "Bearer must-not-reach-origin")
	resp, err := browser.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.ProtoMajor != 2 || resp.StatusCode != 200 || string(body) != "published article" || !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, leaf.Raw) {
		t.Fatal("ordinary HTTPS response incorrect", resp.Status, resp.Proto, err)
	}
	cm, err := newNodeMux(p.ServerIP, master, node)
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	cm.tlsConfig.RootCAs = x509.NewCertPool()
	if _, err = MeasureTunnelLatency(cm); err == nil {
		t.Fatal("accepted untrusted certificate")
	}
	cm.Close()
	cm, err = newNodeMux(p.ServerIP, master, node)
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	cm.tlsConfig.RootCAs = roots
	if _, err = MeasureTunnelLatency(cm); err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	go func() {
		c, e := tcp.Accept()
		if e == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	tunnel, err := dialWithRouting(tcp.Addr().String(), cm, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	tunnel.SetDeadline(time.Now().Add(4 * time.Second))
	var echo [4]byte
	if _, err = tunnel.Write([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(tunnel, echo[:]); err != nil || string(echo[:]) != "echo" {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		b := make([]byte, 16)
		n, a, e := udp.ReadFrom(b)
		if e == nil {
			udp.WriteTo(b[:n], a)
		}
	}()
	game, err := cm.openUDPRelayStream()
	if err != nil {
		t.Fatal(err)
	}
	defer game.Close()
	game.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err = game.Write([]byte{CmdUDP}); err != nil {
		t.Fatal(err)
	}
	if err = writeUDPFramed(game, udp.LocalAddr().String(), []byte("game")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := readUDPFramed(game)
	if err != nil || string(payload) != "game" {
		t.Fatal("UDP relay failed", err)
	}
	oldProof := <-captured
	if oldProof == "" {
		t.Fatal("no authenticated CONNECT was captured")
	}
	dial := &net.Dialer{Timeout: 3 * time.Second}
	for _, proof := range []string{oldProof, "Bearer invalid", ""} {
		c, err := tls.DialWithDialer(dial, "tcp", addr.Addr().String(), base)
		if err != nil {
			t.Fatal(err)
		}
		c.SetDeadline(time.Now().Add(4 * time.Second))
		fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n", authority, authority, proof)
		reply, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: http.MethodConnect})
		if err != nil {
			c.Close()
			t.Fatal(err)
		}
		reply.Body.Close()
		c.Close()
		if reply.StatusCode != 404 {
			t.Fatal("replay or invalid authorization admitted", reply.Status)
		}
	}
	if err = users.ReplaceUsers(nil); err != nil {
		t.Fatal(err)
	}
	if _, err = MeasureTunnelLatency(cm); err == nil {
		t.Fatal("revoked user retained access")
	}
}
