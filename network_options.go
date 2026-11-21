package main

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// A nil Network preserves the settings of clients created before the settings editor.
type NetworkOptions struct {
	LANOnly      bool     `json:"lan_only,omitempty"`
	Quick        int      `json:"quick_profiles"`
	Heartbeat    int      `json:"heartbeat_seconds"`
	Startup      int      `json:"startup_probe_seconds"`
	Probe        string   `json:"probe"`
	STUN         string   `json:"stun"`
	Language     string   `json:"language"`
	Address      string   `json:"address"`
	Mask         string   `json:"mask"`
	Gateway      string   `json:"gateway,omitempty"`
	Bypass       []string `json:"direct_ips,omitempty"`
	Processes    []string `json:"processes,omitempty"`
	Children     bool     `json:"include_children"`
	TCP          bool     `json:"tcp"`
	UDP          bool     `json:"udp"`
	Echo         bool     `json:"icmp_echo"`
	EchoDelay    int      `json:"echo_delay_ms"`
	DNS          bool     `json:"redirect_dns"`
	SelectedDNS  bool     `json:"selected_dns_only"`
	ProxyDNS     bool     `json:"proxy_dns"`
	CustomDNS    string   `json:"custom_dns,omitempty"`
	ChinaDNS     string   `json:"china_dns,omitempty"`
	OtherDNS     string   `json:"other_dns"`
	ChinaDomains []string `json:"china_domains,omitempty"`
}

func (c *Config) networkOptions() NetworkOptions {
	if c.Network != nil {
		return *c.Network
	}
	servers := c.DNSUpstreams
	if len(servers) == 0 {
		servers = []string{"1.1.1.1:53", "9.9.9.9:53"}
	}
	return NetworkOptions{Quick: 4, Heartbeat: 20, Startup: -1, Probe: "CM", STUN: "stun.syncthing.net:3478", Language: "zh", Address: "10.0.85.2", Mask: "255.255.255.0", TCP: true, UDP: true, EchoDelay: 10, DNS: true, ProxyDNS: true, OtherDNS: strings.Join(servers, ", ")}
}
func (o NetworkOptions) subnet() (netip.Prefix, error) {
	ip, e := netip.ParseAddr(o.Address)
	mask := net.ParseIP(o.Mask).To4()
	ones, bits := net.IPMask(mask).Size()
	if e != nil || !ip.Is4() || !ip.IsGlobalUnicast() || bits != 32 || ones < 1 || ones > 30 {
		return netip.Prefix{}, fmt.Errorf("TUN 需要有效的单播 IPv4 地址与连续子网掩码（/1–/30）")
	}
	p := netip.PrefixFrom(ip, ones)
	usable := func(a netip.Addr) bool {
		if !p.Contains(a) || a == p.Masked().Addr() {
			return false
		}
		v := a.As4()
		for i := range v {
			if v[i]|mask[i] != 255 {
				return true
			}
		}
		return false
	}
	if !usable(ip) {
		return p, fmt.Errorf("TUN 地址不能是网络或广播地址")
	}
	if o.Gateway != "" {
		g, e := netip.ParseAddr(o.Gateway)
		if e != nil || !usable(g) || g == ip {
			return p, fmt.Errorf("网关须为同一子网中的其他主机地址")
		}
	}
	return p, nil
}
func dnsAddresses(s string) ([]string, error) {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\r' || r == '\n' }) {
		v = strings.TrimPrefix(v, "tcp://")
		if a, e := netip.ParseAddr(v); e == nil {
			v = netip.AddrPortFrom(a, 53).String()
		}
		a, e := netip.ParseAddrPort(v)
		if e != nil || a.Port() == 0 || a.Addr().IsUnspecified() || a.Addr().IsMulticast() || a.Addr().Zone() != "" {
			return nil, fmt.Errorf("DNS 地址无效 %q（例 tcp://1.1.1.1:53）", v)
		}
		out = append(out, a.String())
	}
	if len(out) > 12 {
		return nil, fmt.Errorf("每组最多 12 个 DNS 地址")
	}
	return out, nil
}
func ipRanges(v []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range v {
		p, e := netip.ParsePrefix(s)
		if e != nil {
			a, e := netip.ParseAddr(s)
			if e != nil || a.Zone() != "" {
				return nil, fmt.Errorf("无效 IP 或 CIDR %q", s)
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
func (o NetworkOptions) validate() error {
	if o.Quick < 0 || o.Quick > 12 || o.Heartbeat < 2 || o.Heartbeat > 300 || o.Startup < -1 || o.Startup > 600 || o.EchoDelay < 0 || o.EchoDelay > 2000 {
		return fmt.Errorf("快捷节点 0–12，心跳 2–300 秒，启动测速 -1–600 秒，ICMP 延迟 0–2000 ms")
	}
	if o.Probe != "CM" && o.Probe != "TCP" && o.Probe != "ICMP" {
		return fmt.Errorf("测速协议须为 CM、TCP 或 ICMP")
	}
	if o.Language != "zh" && o.Language != "en" && o.Language != "system" {
		return fmt.Errorf("语言须为 zh、en 或 system")
	}
	h, p, e := net.SplitHostPort(o.STUN)
	n, e2 := strconv.Atoi(p)
	if e != nil || e2 != nil || h == "" || strings.ContainsAny(h, " /\\\t") || n < 1 || n > 65535 {
		return fmt.Errorf("STUN 需要服务器名:端口")
	}
	if !o.DNS && o.CustomDNS != "" {
		addrs, e := dnsAddresses(o.CustomDNS)
		if e != nil {
			return e
		}
		for _, s := range addrs {
			if netip.MustParseAddrPort(s).Port() != 53 {
				return fmt.Errorf("关闭 DNS 接管时，网卡 DNS 必须使用 53 端口")
			}
		}
	}
	if _, e = o.subnet(); e != nil {
		return e
	}
	if len(o.Bypass) > 512 || len(o.Processes) > 128 || len(o.ChinaDomains) > 2048 {
		return fmt.Errorf("直连网段、进程或域名列表过长")
	}
	if _, e = ipRanges(o.Bypass); e != nil {
		return e
	}
	for _, p := range o.Processes {
		if !strings.HasSuffix(strings.ToLower(p), ".exe") || strings.ContainsAny(p, "/\\:*?\r\n") {
			return fmt.Errorf("请输入 EXE 文件名：%q", p)
		}
	}
	for _, s := range []string{o.ChinaDNS, o.OtherDNS, o.CustomDNS} {
		if _, e = dnsAddresses(s); e != nil {
			return e
		}
	}
	if a, _ := dnsAddresses(o.OtherDNS); len(a) == 0 {
		return fmt.Errorf("至少需要一个其他 DNS")
	}
	for _, s := range o.ChinaDomains {
		d, e := inputDomain(s)
		if e != nil || d != strings.ToLower(s) {
			return fmt.Errorf("无效分流域名 %q", s)
		}
	}
	return nil
}
func lanPeer(addr net.Addr) bool {
	p, ok := addr.(*net.TCPAddr)
	return ok && (p.IP.IsLoopback() || p.IP.IsPrivate() || p.IP.IsLinkLocalUnicast())
}
func matchesSuffix(host string, list []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, s := range list {
		s = strings.ToLower(s)
		if host == s || strings.HasSuffix(host, "."+s) {
			return true
		}
	}
	return false
}
