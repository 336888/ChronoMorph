package main

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

type tunnelDNS struct {
	udp       net.PacketConn
	tcp       net.Listener
	client    atomic.Pointer[ClientMux]
	servers   []string
	preferred atomic.Uint32
	status    atomic.Pointer[string]
	workers   chan struct{}
	stop      chan struct{}
	once      sync.Once
	router    *Router
}

func startTunnelDNS(addr string, cm *ClientMux, router *Router, upstreams []string) (*tunnelDNS, error) {
	if len(upstreams) == 0 {
		return nil, fmt.Errorf("缺少 DNS 上游")
	}
	udp, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp", udp.LocalAddr().String())
	if err != nil {
		udp.Close()
		return nil, err
	}
	s := &tunnelDNS{udp: udp, tcp: tcp, router: router, servers: upstreams, workers: make(chan struct{}, 64), stop: make(chan struct{})}
	s.client.Store(cm)
	go s.serveUDP()
	go s.serveTCP()
	return s, nil
}
func (s *tunnelDNS) Close() {
	s.once.Do(func() { close(s.stop); s.client.Store(nil); s.udp.Close(); s.tcp.Close() })
}
func (s *tunnelDNS) resolve(q *dns.Msg) *dns.Msg {
	fail := new(dns.Msg).SetRcode(q, dns.RcodeServerFailure)
	if resp := localDNSReply(s.router, q); resp != nil {
		return resp
	}
	start := int(s.preferred.Load())
	var last error
	for i := range s.servers {
		idx := (start + i) % len(s.servers)
		cm := s.client.Load()
		if cm == nil {
			return fail
		}
		resp, err := exchangeTunnelDNS(cm, s.servers[idx], q)
		if err == nil {
			s.preferred.Store(uint32(idx))
			label := fmt.Sprintf("隧道正常 · 上游 %s", s.servers[idx])
			s.status.Store(&label)
			return resp
		}
		last = err
	}
	label := fmt.Sprintf("解析失败 · %v", last)
	s.status.Store(&label)
	return fail
}
func localDNSReply(router *Router, q *dns.Msg) *dns.Msg {
	if q.Response || q.Opcode != dns.OpcodeQuery || len(q.Question) != 1 {
		return new(dns.Msg).SetRcode(q, dns.RcodeFormatError)
	}
	if !router.ShouldBlock(q.Question[0].Name) {
		return nil
	}
	resp := new(dns.Msg).SetRcode(q, dns.RcodeNameError)
	resp.RecursionAvailable = true
	return resp
}
func exchangeTunnelDNS(cm *ClientMux, target string, q *dns.Msg) (*dns.Msg, error) {
	conn, err := dialWithRouting(target, cm, nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	wire := &dns.Conn{Conn: conn}
	if err = wire.WriteMsg(q); err != nil {
		return nil, err
	}
	resp, err := wire.ReadMsg()
	if err != nil {
		return nil, err
	}
	if !resp.Response || resp.Id != q.Id || len(resp.Question) != 1 || !strings.EqualFold(resp.Question[0].Name, q.Question[0].Name) || resp.Question[0].Qtype != q.Question[0].Qtype || resp.Question[0].Qclass != q.Question[0].Qclass || resp.Truncated {
		return nil, fmt.Errorf("DNS 应答不匹配或不完整")
	}
	if resp.Rcode == dns.RcodeServerFailure || resp.Rcode == dns.RcodeRefused {
		return nil, fmt.Errorf("上游返回 %s", dns.RcodeToString[resp.Rcode])
	}
	return resp, nil
}
func (s *tunnelDNS) serveUDP() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := s.udp.ReadFrom(buf)
		if err != nil {
			return
		}
		var query dns.Msg
		if query.Unpack(buf[:n]) != nil || query.Response {
			continue
		}
		select {
		case s.workers <- struct{}{}:
			go func(q *dns.Msg, peer net.Addr) {
				defer func() { <-s.workers }()
				resp := s.resolve(q)
				limit := dns.MinMsgSize
				if opt := q.IsEdns0(); opt != nil {
					limit = min(4096, int(opt.UDPSize()))
				}
				resp.Truncate(max(dns.MinMsgSize, limit))
				if wire, err := resp.Pack(); err == nil {
					_, _ = s.udp.WriteTo(wire, peer)
				}
			}(&query, addr)
		default:
			resp := new(dns.Msg).SetRcode(&query, dns.RcodeServerFailure)
			if wire, err := resp.Pack(); err == nil {
				_, _ = s.udp.WriteTo(wire, addr)
			}
		}
	}
}
func (s *tunnelDNS) serveTCP() {
	for {
		c, err := s.tcp.Accept()
		if err != nil {
			return
		}
		select {
		case s.workers <- struct{}{}:
		default:
			c.Close()
			continue
		}
		go func() {
			defer func() { c.Close(); <-s.workers }()
			wire := &dns.Conn{Conn: c}
			for {
				c.SetDeadline(time.Now().Add(12 * time.Second))
				q, err := wire.ReadMsg()
				if err != nil || q.Response {
					return
				}
				select {
				case <-s.stop:
					return
				default:
				}
				if wire.WriteMsg(s.resolve(q)) != nil {
					return
				}
			}
		}()
	}
}
