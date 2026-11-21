package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Router struct {
	mu             sync.RWMutex
	directSuffixes []string
	blockSuffixes  []string
	privateNets    []*net.IPNet
	globalMode     atomic.Bool
}

func NewRouter(directDomains, blockDomains []string) *Router {
	r := &Router{}
	for _, cidr := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7"} {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			r.privateNets = append(r.privateNets, n)
		}
	}
	r.UpdateDirectDomains(directDomains)
	for _, d := range blockDomains {
		if s := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), "."); s != "" {
			r.blockSuffixes = append(r.blockSuffixes, s)
		}
	}
	return r
}

func (r *Router) UpdateDirectDomains(domains []string) {
	if r == nil {
		return
	}
	var list []string
	for _, d := range domains {
		if s := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), "."); s != "" {
			list = append(list, s)
		}
	}
	r.mu.Lock()
	r.directSuffixes = list
	r.mu.Unlock()
}

func (r *Router) SetGlobalMode(global bool) {
	if r != nil {
		r.globalMode.Store(global)
	}
}

func (r *Router) IsGlobalMode() bool {
	return r != nil && r.globalMode.Load()
}

func (r *Router) ShouldBlock(target string) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.blockSuffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

func (r *Router) ShouldDirect(target string) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, n := range r.privateNets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if r.globalMode.Load() {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.directSuffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

func dialWithRouting(target string, cm *ClientMux, router *Router) (net.Conn, error) {
	if router.ShouldBlock(target) {
		return nil, fmt.Errorf("blocked: %s", target)
	}
	if router.ShouldDirect(target) {
		return net.DialTimeout("tcp", target, 8*time.Second)
	}
	if len(target) > 255 {
		return nil, fmt.Errorf("target too long")
	}
	for attempt := 0; ; attempt++ {
		stream, sess, mode, err := cm.openSessionStream()
		if err != nil {
			return nil, err
		}
		started := time.Now()
		err = sendDialRequest(stream, target, mode)
		if err == nil {
			return stream, nil
		}
		_ = stream.Close()
		if errors.Is(err, errTargetRejected) || !cm.discardSilentPath(sess, started) || attempt != 0 {
			return nil, err
		}
		// No application bytes have been forwarded yet. Retry only the setup.
	}
}

var errTargetRejected = errors.New("节点已应答，目标 TCP 连接失败")

func sendDialRequest(stream net.Conn, target string, mode byte) error {
	hdr := make([]byte, 2+len(target))
	hdr[0] = mode
	hdr[1] = byte(len(target))
	copy(hdr[2:], target)
	timeout := 10 * time.Second
	if mode == CmdDialReady {
		timeout = 3 * time.Second
	}
	_ = stream.SetDeadline(time.Now().Add(timeout))
	if _, err := stream.Write(hdr); err != nil {
		return err
	}
	if mode == CmdDialReady {
		var ready [1]byte
		if _, err := io.ReadFull(stream, ready[:]); err != nil {
			return fmt.Errorf("节点未确认收到请求: %w", err)
		}
		if ready[0] != CmdDialReady {
			return fmt.Errorf("节点返回了无效的确认")
		}
		_ = stream.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if mode != CmdTCP {
		var reply [1]byte
		if _, err := io.ReadFull(stream, reply[:]); err != nil {
			return fmt.Errorf("等待节点确认目标连接: %w", err)
		}
		if reply[0] != 0 {
			return errTargetRejected
		}
	}
	return stream.SetDeadline(time.Time{})
}

// Proxy negotiation can read ahead into the first application record.
type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) CloseWrite() error { return closeWrite(c.Conn) }

func (c *bufferedProxyConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}

func handleMixedProxy(c net.Conn, cm *ClientMux, router *Router) {
	defer c.Close()
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	c = &bufferedProxyConn{Conn: c, reader: br}
	if first[0] == 0x05 {
		handleSocks5Mixed(c, br, cm, router)
	} else {
		handleHTTPMixed(c, cm, router)
	}
}

func handleSocks5Mixed(c net.Conn, br *bufio.Reader, cm *ClientMux, router *Router) {
	var verN [2]byte
	if _, err := io.ReadFull(br, verN[:]); err != nil {
		return
	}
	methods := make([]byte, verN[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if verN[0] != 5 || !bytes.Contains(methods, []byte{0}) {
		_, _ = c.Write([]byte{5, 255})
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}

	var reqHdr [4]byte
	if _, err := io.ReadFull(br, reqHdr[:]); err != nil {
		return
	}
	if reqHdr[0] != 5 || reqHdr[2] != 0 {
		return
	}
	if reqHdr[1] != 1 && reqHdr[1] != 3 {
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}

	var host string
	switch reqHdr[3] {
	case 0x01:
		var ip [4]byte
		if _, err := io.ReadFull(br, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 0x03:
		l, err := br.ReadByte()
		if err != nil {
			return
		}
		dom := make([]byte, l)
		if _, err := io.ReadFull(br, dom); err != nil {
			return
		}
		host = string(dom)
	case 0x04:
		var ip [16]byte
		if _, err := io.ReadFull(br, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	default:
		return
	}
	var pBuf [2]byte
	if _, err := io.ReadFull(br, pBuf[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pBuf[:])
	if reqHdr[1] == 3 {
		handleSocks5UDPAssociate(c, cm, router, int(port))
		return
	}
	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	remote, err := dialWithRouting(target, cm, router)
	if err != nil {
		if cm != nil {
			cm.logProxyFailure(err)
		}
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	pipeBidirectional(c, remote)
}

// The mixed listener already accepted and classified this socket. Let
// net/http serve every request on it, including keep-alive and upgrades.
type httpProxySocket struct {
	net.Conn
	accepted bool
	done     chan struct{}
	once     sync.Once
}

func (s *httpProxySocket) Accept() (net.Conn, error) {
	if s.accepted {
		<-s.done
		return nil, net.ErrClosed
	}
	s.accepted = true
	return s, nil
}
func (s *httpProxySocket) Addr() net.Addr    { return s.LocalAddr() }
func (s *httpProxySocket) CloseWrite() error { return closeWrite(s.Conn) }
func (s *httpProxySocket) Close() (err error) {
	s.once.Do(func() { err = s.Conn.Close(); close(s.done) })
	return err
}

func handleHTTPMixed(c net.Conn, cm *ClientMux, router *Router) {
	transport := &http.Transport{
		DisableCompression: true,
		MaxIdleConns:       4,
		IdleConnTimeout:    30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return dialWithRouting(address, cm, nil)
		},
	}
	defer transport.CloseIdleConnections()
	// Separate pools so a routing-mode change cannot reuse an old direct
	// connection for a request that now belongs in the tunnel.
	direct := transport.Clone()
	direct.DialContext = (&net.Dialer{Timeout: 8 * time.Second}).DialContext
	defer direct.CloseIdleConnections()
	fail := func(w http.ResponseWriter, _ *http.Request, err error) {
		if cm != nil {
			cm.logProxyFailure(err)
		}
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}
	forward := &httputil.ReverseProxy{
		Transport: transport, ErrorHandler: fail,
		Rewrite: func(r *httputil.ProxyRequest) {
			// ReverseProxy strips proxy credentials, hop-by-hop fields and
			// forwarding metadata on every request. Preserve origin credentials.
			r.Out.URL.Scheme, r.Out.URL.Host = "http", r.In.Host
		},
	}
	directForward := *forward
	directForward.Transport = direct
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodConnect {
			if req.URL.Scheme != "" && req.URL.Scheme != "http" {
				http.Error(w, "Use CONNECT for TLS", http.StatusBadRequest)
				return
			}
			if req.URL.Host == "" {
				req.URL.Host = req.Host
			}
			host := req.URL.Hostname()
			if router.ShouldBlock(host) {
				fail(w, req, fmt.Errorf("blocked: %s", req.Host))
			} else if router.ShouldDirect(host) {
				directForward.ServeHTTP(w, req)
			} else {
				forward.ServeHTTP(w, req)
			}
			return
		}
		target := req.Host
		if _, _, err := net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(strings.Trim(target, "[]"), "443")
		}
		remote, err := dialWithRouting(target, cm, router)
		if err != nil {
			fail(w, req, err)
			return
		}
		defer remote.Close()
		client, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = rw.Flush(); err != nil {
			return
		}
		pipeBidirectional(&bufferedProxyConn{Conn: client, reader: rw.Reader}, remote)
	})}
	_ = server.Serve(&httpProxySocket{Conn: c, done: make(chan struct{})})
}
