package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
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
		if s := strings.ToLower(strings.TrimSpace(d)); s != "" {
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
		if s := strings.ToLower(strings.TrimSpace(d)); s != "" {
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
	host = strings.ToLower(host)
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
	if r.globalMode.Load() {
		return false
	}
	host = strings.ToLower(host)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.directSuffixes {
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}

func dialWithRouting(target string, cm *ClientMux, router *Router) (io.ReadWriteCloser, error) {
	if router.ShouldBlock(target) {
		return nil, fmt.Errorf("blocked: %s", target)
	}
	if router.ShouldDirect(target) {
		return net.DialTimeout("tcp", target, 8*time.Second)
	}
	stream, err := cm.OpenStream()
	if err != nil {
		return nil, err
	}
	if len(target) > 255 {
		_ = stream.Close()
		return nil, fmt.Errorf("target too long")
	}
	hdr := make([]byte, 2+len(target))
	hdr[0] = CmdTCP
	hdr[1] = byte(len(target))
	copy(hdr[2:], target)
	if _, err := stream.Write(hdr); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

// Proxy negotiation can read ahead into the first application record.
type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

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
		handleHTTPMixed(c, br, cm, router)
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
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer remote.Close()
	_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	pipeBidirectional(c, remote)
}

func handleHTTPMixed(c net.Conn, br *bufio.Reader, cm *ClientMux, router *Router) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	target := req.Host
	if !strings.Contains(target, ":") {
		if req.Method == http.MethodConnect {
			target += ":443"
		} else {
			target += ":80"
		}
	}
	remote, err := dialWithRouting(target, cm, router)
	if err != nil {
		_, _ = c.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer remote.Close()

	if req.Method == http.MethodConnect {
		_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	} else {
		_ = req.Write(remote)
	}
	pipeBidirectional(c, remote)
}
