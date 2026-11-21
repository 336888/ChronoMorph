package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// WebsiteConfig serves a real HTTPS virtual host. The HTTP origin is fixed
// by the operator. Unauthenticated requests can never choose an upstream.
type WebsiteConfig struct {
	Name     string `json:"name"`
	Listen   string `json:"listen"`
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	Origin   string `json:"origin"`
	Only     bool   `json:"only,omitempty"`
}

func validWebsiteName(s string) bool {
	if len(s) > 253 || !strings.Contains(s, ".") || net.ParseIP(s) != nil {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func (w WebsiteConfig) server(users *MultiUserEngine) (*http.Server, error) {
	if !validWebsiteName(w.Name) {
		return nil, errors.New("网站域名无效，请使用小写域名")
	}
	bindHost, bindPort, err := net.SplitHostPort(w.Listen)
	if err != nil || (bindHost != "" && net.ParseIP(bindHost) == nil) {
		return nil, errors.New("网站监听地址无效")
	}
	port, err := strconv.Atoi(bindPort)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("网站监听端口无效")
	}
	origin, err := url.Parse(w.Origin)
	if err != nil || origin.Scheme != "http" || origin.User != nil || !net.ParseIP(origin.Hostname()).IsLoopback() ||
		origin.Port() == "" || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("网站源站必须是带端口的本机 HTTP 地址，例如 http://127.0.0.1:8080")
	}
	op, err := strconv.Atoi(origin.Port())
	if err != nil || op < 1 || op > 65535 || op == port {
		return nil, errors.New("源站端口必须有效且与网站入口不同")
	}
	pair, err := tls.LoadX509KeyPair(w.CertFile, w.KeyFile)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if err := leaf.VerifyHostname(w.Name); err != nil {
		return nil, err
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return nil, errors.New("网站证书尚未生效或已经过期")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 60 * time.Second}
	site := &httputil.ReverseProxy{Transport: transport, Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(origin)
		p.Out.Host = p.In.Host
	}}
	authority := net.JoinHostPort(w.Name, bindPort)
	server := &http.Server{Addr: w.Listen, ReadHeaderTimeout: 8 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}}
	server.RegisterOnShutdown(transport.CloseIdleConnections)
	server.Handler = http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, authority) && !strings.EqualFold(r.Host, w.Name) {
			http.NotFound(out, r)
			return
		}
		if r.Method != http.MethodConnect {
			site.ServeHTTP(out, r)
			return
		}
		if r.ProtoMajor == 1 && r.TLS != nil && r.TLS.Version == tls.VersionTLS13 && r.RequestURI == authority {
			if u := websiteUser(r.TLS, r.Header.Get("Proxy-Authorization"), users); u != nil {
				conn, rw, err := http.NewResponseController(out).Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || rw.Flush() != nil {
					return
				}
				_ = conn.SetDeadline(time.Time{})
				handleServerSession(&tlsMeterConn{Conn: &bufferedProxyConn{Conn: conn, reader: rw.Reader}, profile: tlsProfileReady, user: u, users: users})
				return
			}
		}
		http.NotFound(out, r)
	})
	return server, nil
}

// Bind each proof to one verified TLS connection. No reusable credential
// travels in an HTTP header. Existing registered Ed25519 keys authorize users.
func websiteBinding(state *tls.ConnectionState) ([]byte, error) {
	return state.ExportKeyingMaterial("EXPORTER-ChronoMorph-HTTPS-v1", nil, 32)
}

func websiteUser(state *tls.ConnectionState, header string, users *MultiUserEngine) *UserEntry {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(token) != base64.RawURLEncoding.EncodedLen(ed25519.PublicKeySize+ed25519.SignatureSize) {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil
	}
	binding, err := websiteBinding(state)
	if err != nil {
		return nil
	}
	users.mu.RLock()
	defer users.mu.RUnlock()
	for _, u := range users.users {
		if bytes.Equal(raw[:ed25519.PublicKeySize], u.tlsPublic) && !u.IsOverQuota(u.LimitGB) &&
			ed25519.Verify(u.tlsPublic, binding, raw[ed25519.PublicKeySize:]) {
			return u
		}
	}
	return nil
}

func (cm *ClientMux) websiteTunnel(conn *tls.Conn) (net.Conn, error) {
	state := conn.ConnectionState()
	binding, err := websiteBinding(&state)
	if err != nil {
		return nil, err
	}
	proof := append([]byte(nil), cm.webKey.Public().(ed25519.PublicKey)...)
	proof = append(proof, ed25519.Sign(cm.webKey, binding)...)
	auth := net.JoinHostPort(cm.tlsNode.WebName, strconv.Itoa(cm.tlsNode.TLSPort))
	req := &http.Request{Method: http.MethodConnect, Host: auth, URL: &url.URL{Host: auth}, Header: make(http.Header)}
	req.Header.Set("Proxy-Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(proof))
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if err := req.Write(conn); err != nil {
		return nil, err
	}
	// Limit unauthenticated response headers without limiting the tunnel.
	limited := &io.LimitedReader{R: conn, N: 32 << 10}
	br := bufio.NewReader(limited)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTPS 节点拒绝连接（HTTP %d），请检查用户密钥和配额", resp.StatusCode)
	}
	prefix, _ := br.Peek(br.Buffered())
	reader := bufio.NewReader(io.MultiReader(bytes.NewReader(bytes.Clone(prefix)), conn))
	_ = conn.SetDeadline(time.Time{})
	return &tlsMeterConn{Conn: &bufferedProxyConn{Conn: conn, reader: reader}, profile: tlsProfileReady}, nil
}

// Exercise the ordinary website and the authenticated transport separately.
func checkWebsite(path string, configOnly bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if cfg.Website == nil {
		return errors.New("尚未配置 HTTPS 网站入口")
	}
	w := cfg.Website
	if configOnly {
		_, err := w.server(nil)
		return err
	}
	h, ps, err := net.SplitHostPort(w.Listen)
	if err != nil {
		return err
	}
	if h == "" || h == "0.0.0.0" {
		h = "127.0.0.1"
	} else if h == "::" {
		h = "::1"
	}
	port, err := strconv.Atoi(ps)
	if err != nil {
		return err
	}
	users := cfg.serverUsers()
	if len(users) == 0 {
		return errors.New("网站入口未配置用户")
	}
	cm, err := newNodeMux(h, users[0].KeyHex, TLSNode{TLSPort: port, WebName: w.Name})
	if err != nil {
		return err
	}
	defer cm.Close()
	tr := &http.Transport{TLSClientConfig: &tls.Config{ServerName: w.Name}, ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(h, ps))
		}}
	defer tr.CloseIdleConnections()
	cl := &http.Client{Timeout: 6 * time.Second, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := cl.Get("https://" + net.JoinHostPort(w.Name, ps) + "/")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("网站首页返回 HTTP %d", resp.StatusCode)
	}
	_, err = MeasureTunnelLatency(cm)
	return err
}
