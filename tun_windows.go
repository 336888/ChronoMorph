package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"github.com/xjasonlyu/tun2socks/v2/proxy"
	"github.com/xjasonlyu/tun2socks/v2/tunnel"
	"github.com/xjasonlyu/tun2socks/v2/tunnel/statistic"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

type ipv4Handler struct{ adapter.TransportHandler }

func (h ipv4Handler) HandleTCP(c adapter.TCPConn) {
	if c.ID().LocalAddress.BitLen() == 128 {
		c.Close()
		return
	}
	h.TransportHandler.HandleTCP(c)
}
func (h ipv4Handler) HandleUDP(c adapter.UDPConn) {
	if c.ID().LocalAddress.BitLen() == 128 {
		c.Close()
		return
	}
	h.TransportHandler.HandleUDP(c)
}

func (g *GUIController) networkCheck() string {
	g.mu.Lock()
	cm := g.cm
	block := g.cfg.TUNBlockIPv6
	g.mu.Unlock()
	report := GlobalTUN.diagnostics()
	if block {
		report += "\nIPv6 策略：阻止 TUN 接到的 IPv6 连接"
	}
	if cm == nil {
		return report + "\n代理未启动"
	}
	probe := func(n *ClientMux) string {
		r, e := MeasureTunnelLatency(n)
		if e != nil {
			return e.Error()
		}
		return fmt.Sprintf("%d ms", r)
	}
	if cm.entry != nil {
		report += "\n前置应答：" + probe(cm.entry)
	}
	report += "\n出口应答：" + probe(cm) + "\n实际网页链路：" + cm.routeLabel()
	GlobalTUN.mu.Lock()
	s := GlobalTUN.dns
	GlobalTUN.mu.Unlock()
	if s != nil {
		q := new(dns.Msg).SetQuestion("example.com.", dns.TypeA)
		resp, _, err := (&dns.Client{Net: "udp", Timeout: 10 * time.Second}).Exchange(q, s.udp.LocalAddr().String())
		switch {
		case err != nil:
			report += "\n本地 DNS 自检：" + err.Error()
		case s.router.ShouldBlock("example.com"):
			report += "\n本地 DNS 自检：测试域名被本地规则拦截"
		default:
			report += "\n本地 DNS 自检：" + dns.RcodeToString[resp.Rcode]
			if state := s.status.Load(); state != nil {
				report += " · " + *state
			}
		}
	}
	return report
}

const tunName = "ChronoMorphTUN"

type hostRoute struct {
	nic winipcfg.LUID
	dst netip.Prefix
	via netip.Addr
}
type TUNManager struct {
	mu          sync.Mutex
	stopStack   func()
	luid        winipcfg.LUID
	ifIndex     uint32
	dns         *tunnelDNS
	activeNode  *ClientMux
	pins        map[*ClientMux]hostRoute
	owned       map[hostRoute]bool
	interrupted bool
	recovery    string
}

var GlobalTUN = &TUNManager{}
var tunPrefixes = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1"), netip.MustParsePrefix("2000::/3")}

func (g *GUIController) toggleTUN() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if GlobalTUN.IsActive() {
		GlobalTUN.StopTUN()
		return nil
	}
	if !g.running {
		return fmt.Errorf("请先启动连接")
	}
	opts := g.cfg.networkOptions()
	if err := opts.validate(); err != nil {
		return err
	}
	upstreams, _ := dnsAddresses(opts.OtherDNS)
	if opts.CustomDNS != "" {
		upstreams, _ = dnsAddresses(opts.CustomDNS)
	}
	if len(upstreams) == 0 {
		upstreams = []string{"1.1.1.1:53", "9.9.9.9:53"}
	}
	for _, s := range upstreams {
		a, e := netip.ParseAddrPort(s)
		if e != nil || a.Port() == 0 {
			return fmt.Errorf("DNS 上游需要 IP:端口，收到 %q", s)
		}
	}
	// Reserve physical routes for already-running latency probes before redirecting IP.
	nodes := []*ClientMux{g.cm}
	for p := range g.probes {
		nodes = append(nodes, p)
	}
	return GlobalTUN.start(g.proxyAddressLocked(), nodes, g.router, "127.0.0.1:53", upstreams, tunPrefixes, g.cfg.TUNBlockIPv6, opts)
}

func (t *TUNManager) start(proxyAddr string, nodes []*ClientMux, router *Router, dnsAddr string, upstreams []string, prefixes []netip.Prefix, block6 bool, options ...NetworkOptions) (err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopStack != nil {
		return nil
	}
	if !IsAdmin() {
		return fmt.Errorf("请以管理员身份运行客户端后再启用 TUN")
	}
	opts := (&Config{}).networkOptions()
	if len(options) > 0 {
		opts = options[0]
	}
	if err = opts.validate(); err != nil {
		return err
	}
	subnet, _ := opts.subnet()
	h, err := loadBundledWintun()
	if err != nil {
		return err
	}
	defer windows.FreeLibrary(h)
	p, err := proxy.NewSocks5(proxyAddr, "", "")
	if err != nil {
		return err
	}
	dev, err := tun.Open(tunName, 1400)
	if err != nil {
		return fmt.Errorf("打开 Wintun: %w", err)
	}
	handler := tunnel.New(p, statistic.DefaultManager)
	handler.ProcessAsync()
	var transport adapter.TransportHandler = handler
	if block6 {
		transport = ipv4Handler{handler}
	}
	link := newEchoLink(dev, opts.Echo, time.Duration(opts.EchoDelay)*time.Millisecond, block6)
	stack, err := core.CreateStack(&core.Config{LinkEndpoint: link, TransportHandler: transport})
	if err != nil {
		dev.Close()
		handler.Close()
		return err
	}
	t.stopStack = func() {
		link.stop()
		dev.Close()
		dev.Wait()
		link.wait.Wait()
		stack.Close()
		stack.Wait()
		handler.Close()
	}
	defer func() {
		if err != nil {
			t.stopLocked()
		}
	}()
	nic, err := net.InterfaceByName(dev.Name())
	if err != nil {
		return err
	}
	t.ifIndex = uint32(nic.Index)
	t.luid, err = winipcfg.LUIDFromIndex(t.ifIndex)
	if err != nil {
		return err
	}
	if err = t.luid.AddIPAddresses([]netip.Prefix{subnet, netip.MustParsePrefix("fd70:636d::2/128")}); err != nil {
		return fmt.Errorf("配置 TUN 双栈地址: %w", err)
	}
	t.pins = make(map[*ClientMux]hostRoute)
	t.owned = make(map[hostRoute]bool)
	t.activeNode = nodes[0]
	for _, cm := range nodes {
		if err = t.pinLocked(cm); err != nil {
			return err
		}
	}
	bypass, _ := ipRanges(opts.Bypass)
	policy := &tunPolicy{Dialer: p, opts: opts, virtual: t.luid, bypass: bypass}
	if opts.DNS {
		if opts.SelectedDNS && len(opts.Processes) > 0 {
			dnsAddr = "127.0.0.1:0"
		}
		t.dns, err = startTunnelDNS(dnsAddr, nodes[0], router, upstreams, func(s *tunnelDNS) {
			if opts.CustomDNS == "" {
				s.china, _ = dnsAddresses(opts.ChinaDNS)
				s.domains = append([]string{"cn"}, opts.ChinaDomains...)
			}
			if !opts.ProxyDNS {
				s.directDial = func(address string) (net.Conn, error) {
					if address == s.tcp.Addr().String() {
						return nil, fmt.Errorf("DNS 上游指回本地监听地址")
					}
					ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
					defer cancel()
					return physicalDial(ctx, "tcp", address, policy.virtual)
				}
			}
		})
		if err != nil {
			return fmt.Errorf("启动本地 DNS: %w", err)
		}
		policy.resolver = t.dns.udp.LocalAddr().String()
	}
	handler.SetDialer(policy)
	if opts.DNS && (!opts.SelectedDNS || len(opts.Processes) == 0) {
		if err = t.luid.SetDNS(windows.AF_INET, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil); err != nil {
			return fmt.Errorf("设置 TUN DNS: %w", err)
		}

	} else if !opts.DNS && opts.CustomDNS != "" {
		addresses, _ := dnsAddresses(opts.CustomDNS)
		v4, v6 := []netip.Addr{}, []netip.Addr{}
		for _, a := range addresses {
			ap := netip.MustParseAddrPort(a)
			if ap.Port() != 53 {
				return fmt.Errorf("关闭 DNS 接管时，网卡 DNS 必须使用 53 端口")
			}
			if ap.Addr().Is4() {
				v4 = append(v4, ap.Addr())
			} else {
				v6 = append(v6, ap.Addr())
			}
		}
		if len(v4) > 0 {
			if err = t.luid.SetDNS(windows.AF_INET, v4, nil); err != nil {
				return err
			}
		}
		if len(v6) > 0 {
			if err = t.luid.SetDNS(windows.AF_INET6, v6, nil); err != nil {
				return err
			}
		}
	}
	for _, prefix := range prefixes {
		via := netip.IPv4Unspecified()
		if opts.Gateway != "" {
			via = netip.MustParseAddr(opts.Gateway)
		}
		if prefix.Addr().Is6() {
			via = netip.IPv6Unspecified()
		}
		if err = t.luid.AddRoute(prefix, via, 5); err != nil {
			return fmt.Errorf("配置 TUN 路由 %s: %w", prefix, err)
		}
	}
	t.recovery = "等待网络变更"
	return nil
}
func (t *TUNManager) pinLocked(cm *ClientMux) error {
	ip, ok := netip.AddrFromSlice(cm.physicalIP())
	if !ok {
		return fmt.Errorf("节点 IP 无效")
	}
	ip = ip.Unmap()
	// Windows already has a more-specific route for loopback.
	if ip.IsLoopback() {
		t.pins[cm] = hostRoute{}
		return nil
	}
	family := winipcfg.AddressFamily(windows.AF_INET)
	if ip.Is6() {
		family = windows.AF_INET6
	}
	routes, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		return err
	}
	ifaces, err := winipcfg.GetIPInterfaceTable(family)
	if err != nil {
		return err
	}
	best, ok := selectUplink(ip, t.luid, routes, ifaces, t.owned)
	if !ok {
		return fmt.Errorf("未找到到前置节点 %s 的可用上行路由", ip)
	}
	if _, err = best.nic.Route(best.dst, best.via); err != nil {
		if err = best.nic.AddRoute(best.dst, best.via, 1); err != nil && !errors.Is(err, windows.ERROR_OBJECT_ALREADY_EXISTS) {
			return fmt.Errorf("预留节点出站路由: %w", err)
		}
		if err == nil {
			t.owned[best] = true
		}
	}
	t.pins[cm] = best
	return nil
}
func selectUplink(ip netip.Addr, virtual winipcfg.LUID, rows []winipcfg.MibIPforwardRow2, ifaces []winipcfg.MibIPInterfaceRow, owned map[hostRoute]bool) (hostRoute, bool) {
	metrics := make(map[winipcfg.LUID]uint32)
	for _, i := range ifaces {
		if i.Connected && i.InterfaceLUID != virtual {
			metrics[i.InterfaceLUID] = i.Metric
		}
	}
	var best hostRoute
	bits := -1
	var cost uint64
	for _, r := range rows {
		metric, up := metrics[r.InterfaceLUID]
		p := r.DestinationPrefix.Prefix()
		if !up || !p.Contains(ip) || r.Loopback {
			continue
		}
		next := hostRoute{r.InterfaceLUID, p, r.NextHop.Addr()}
		if owned[next] {
			continue
		}
		score := uint64(metric) + uint64(r.Metric)
		if p.Bits() > bits || (p.Bits() == bits && (score < cost || score == cost && r.InterfaceLUID < best.nic)) {
			best = next
			bits = p.Bits()
			cost = score
		}
	}
	best.dst = netip.PrefixFrom(ip, ip.BitLen())
	return best, bits >= 0
}
func (t *TUNManager) collect() {
	for r := range t.owned {
		used := false
		for _, pin := range t.pins {
			if pin == r {
				used = true
				break
			}
		}
		if used {
			continue
		}
		err := r.nic.DeleteRoute(r.dst, r.via)
		if err == nil || errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			delete(t.owned, r)
		} else {
			log.Printf("[TUN] 移除旧节点路由: %v", err)
		}
	}
}
func (t *TUNManager) pin(cm *ClientMux) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopStack == nil {
		return nil
	}
	return t.pinLocked(cm)
}
func (t *TUNManager) unpin(cm *ClientMux) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pins, cm)
	t.collect()
}
func (t *TUNManager) adopt(old, next *ClientMux) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopStack == nil {
		return
	}
	t.activeNode = next
	if t.dns != nil {
		t.dns.client.Store(next)
	}
	delete(t.pins, old)
	t.collect()
}
func (t *TUNManager) refreshNetwork() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopStack == nil {
		return
	}
	for cm, old := range t.pins {
		if err := t.pinLocked(cm); err != nil {
			if cm == t.activeNode {
				t.interrupted = true
				t.recovery = err.Error()
			}
			continue
		}
		if t.pins[cm] != old || cm == t.activeNode && t.interrupted {
			if cm.entry != nil {
				cm.entry.Reset()
			}
			cm.Reset()
			if cm == t.activeNode {
				t.interrupted = false
				t.recovery = "已更新上行路由 · " + time.Now().Format("15:04:05")
				log.Print("[TUN] ", t.recovery)
			}
		}
	}
	t.collect()
}
func (t *TUNManager) stopLocked() {
	if t.stopStack == nil {
		return
	}
	if t.dns != nil {
		t.dns.Close()
		t.dns = nil
	}
	t.pins = nil
	t.collect()
	t.stopStack()
	t.stopStack = nil
	t.activeNode = nil
	t.interrupted = false
}
func (t *TUNManager) StopTUN()       { t.mu.Lock(); defer t.mu.Unlock(); t.stopLocked() }
func (t *TUNManager) IsActive() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.stopStack != nil }

func (t *TUNManager) diagnostics() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopStack == nil {
		return "TUN 未开启"
	}
	check := func(sa windows.Sockaddr) string {
		var idx uint32
		if err := windows.GetBestInterfaceEx(sa, &idx); err != nil {
			return "无路由"
		}
		if idx == t.ifIndex {
			return "TUN"
		}
		return fmt.Sprintf("其他接口 #%d", idx)
	}
	v4 := check(&windows.SockaddrInet4{Addr: [4]byte{192, 0, 2, 1}})
	v6 := check(&windows.SockaddrInet6{Addr: netip.MustParseAddr("2001:db8::1").As16()})
	dnsState := "尚无查询"
	if t.dns == nil {
		dnsState = "DNS 接管已关闭"
	} else if s := t.dns.status.Load(); s != nil {
		dnsState = *s
	}
	return fmt.Sprintf("IPv4 路由 %s · IPv6 路由 %s\nDNS %s\n%s", v4, v6, dnsState, t.recovery)
}
