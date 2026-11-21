package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/xtaci/smux"
)

const (
	transportUDP = 1
	transportTCP = 2
)

type tunnelPath struct {
	session *smux.Session
	udp     *RTPPacketConn
	tcp     *tlsMeterConn
	mode    byte
}

func (p *tunnelPath) close() {
	if p.udp != nil {
		_ = p.udp.Close()
	}
	if p.session != nil {
		_ = p.session.Close()
	}
}

// Each candidate owns its sockets until the mux accepts it. The two candidates
// only authenticate the node. Neither sends an application request.
func (cm *ClientMux) dialPath(ctx context.Context, tcp bool) (tunnelPath, error) {
	var p tunnelPath
	var conn net.Conn
	var err error
	if tcp {
		conn, err = cm.dialTLS(ctx)
		if err == nil {
			p.tcp = conn.(*tlsMeterConn)
		}
	} else {
		conn, p.udp, err = cm.dialUDP()
	}
	if err != nil {
		return tunnelPath{}, err
	}
	p.session, err = smux.Client(conn, smuxTransportConfig(conn))
	if err != nil {
		p.close()
		_ = conn.Close()
		return tunnelPath{}, err
	}
	stop := context.AfterFunc(ctx, p.close)
	defer stop()
	p.mode = CmdDialTCP
	if tcp && p.tcp.profile == tlsProfileReady {
		p.mode = CmdDialReady
	} else if !tcp || cm.tlsNode.UDPPort != 0 {
		mode, err := negotiateDialMode(p.session)
		if err != nil {
			p.close()
			if !tcp {
				return tunnelPath{}, fmt.Errorf("UDP 节点未应答，请检查 UDP 放行、密钥和两端时间: %w", err)
			}
			return tunnelPath{}, fmt.Errorf("TLS/TCP 节点未应答: %w", err)
		}
		if !tcp || mode != CmdTCP {
			p.mode = mode
		}
	}
	return p, nil
}

func (cm *ClientMux) connectTransport(parent context.Context, firstTCP bool) (tunnelPath, error) {
	if cm.tlsNode.UDPPort == 0 {
		return cm.dialPath(parent, cm.tlsConfig != nil)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	type outcome struct {
		path tunnelPath
		tcp  bool
		err  error
	}
	results := make(chan outcome)
	launch := func(tcp bool) {
		go func() {
			path, err := cm.dialPath(ctx, tcp)
			select {
			case results <- outcome{path, tcp, err}:
			case <-ctx.Done():
				path.close()
			}
		}()
	}
	launch(firstTCP)
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	fallback := timer.C
	startOther := func() { launch(!firstTCP); fallback = nil }
	var tcpErr, udpErr error
	for {
		select {
		case <-ctx.Done():
			return tunnelPath{}, ctx.Err()
		case <-fallback:
			startOther()
		case r := <-results:
			if r.err == nil {
				label := "UDP"
				if r.tcp {
					label = "TLS/TCP"
				}
				lane := "网页"
				if cm.preferUDP {
					lane = "应用 UDP"
				}
				log.Printf("[双协议] %s通道已连接 %s", lane, label)
				return r.path, nil
			}
			if r.tcp {
				tcpErr = r.err
			} else {
				udpErr = r.err
			}
			if tcpErr != nil && udpErr != nil {
				return tunnelPath{}, fmt.Errorf("双协议节点均未应答（TCP: %v，UDP: %v）", tcpErr, udpErr)
			}
			if fallback != nil {
				startOther()
			}
		}
	}
}

func (cm *ClientMux) transportName() string {
	if cm != nil {
		switch cm.activeTransport.Load() {
		case transportUDP:
			return "UDP"
		case transportTCP:
			if cm.tlsNode.RealityName != "" {
				return "REALITY"
			}
			if cm.tlsNode.WebName != "" {
				return "HTTPS"
			}
			return "TLS/TCP"
		}
	}
	return "待连接"
}
