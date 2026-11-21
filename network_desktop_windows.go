package main

import (
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
	"unsafe"
)

const (
	openNetwork = 300 + iota
	tabGeneral
	tabProcess
	tabAdapter
	tabDNS
	saveNetwork
	netPort
	netLAN
	netQuick
	netHeartbeat
	netStartup
	netProbe
	netSTUN
	netLanguage
	testSTUN
	quickList
	useQuick
	procNames
	procTCP
	procUDP
	procEcho
	procDelay
	procChildren
	procDNS
	procScope
	procProxyDNS
	netAddress
	netMask
	netGateway
	netBypass
	dnsCustom
	dnsCustomAddr
	dnsChina
	dnsOther
	dnsDomains
)

type networkField struct {
	id, page, x, y, w, h int
	label, kind          string
}

var networkFields = []networkField{
	{netPort, generalNetPage, 230, 164, 115, 30, "本地代理端口", "edit"},
	{netLAN, generalNetPage, 360, 164, 180, 30, "允许局域网设备接入", "check"},
	{netProbe, generalNetPage, 563, 164, 234, 30, "延迟测试协议", "combo"},
	{netQuick, generalNetPage, 230, 229, 145, 30, "快捷节点数", "edit"},
	{netHeartbeat, generalNetPage, 400, 229, 155, 30, "检测心跳（秒）", "edit"},
	{netStartup, generalNetPage, 580, 229, 217, 30, "连接后测速（秒，-1 关闭）", "edit"},
	{netSTUN, generalNetPage, 230, 297, 347, 30, "STUN 服务器", "edit"},
	{netLanguage, generalNetPage, 605, 297, 192, 30, "界面语言", "combo"},
	{quickList, generalNetPage, 230, 367, 347, 30, "快捷切换节点", "combo"},
	{procNames, processNetPage, 230, 164, 565, 55, "接管的进程 · 每行一个 EXE，留空接管全部", "multi"},
	{procTCP, processNetPage, 230, 232, 123, 31, "处理 TCP", "check"},
	{procUDP, processNetPage, 364, 232, 123, 31, "处理 UDP", "check"},
	{procChildren, processNetPage, 499, 232, 186, 31, "包含子进程", "check"},
	{procEcho, processNetPage, 230, 283, 205, 31, "本地回应 ICMP Echo", "check"},
	{procDelay, processNetPage, 600, 284, 195, 31, "模拟延迟（毫秒）", "edit"},
	{procDNS, processNetPage, 230, 337, 166, 32, "接管 53 端口 DNS", "check"},
	{procScope, processNetPage, 408, 337, 187, 32, "仅指定进程的 DNS", "check"},
	{procProxyDNS, processNetPage, 606, 337, 191, 32, "DNS 经过 CM 节点", "check"},
	{netAddress, adapterNetPage, 230, 168, 175, 31, "虚拟网卡 IPv4", "edit"},
	{netMask, adapterNetPage, 422, 168, 175, 31, "子网掩码", "edit"},
	{netGateway, adapterNetPage, 614, 168, 183, 31, "网关（空为链路直达）", "edit"},
	{netBypass, adapterNetPage, 230, 247, 567, 128, "TUN 全局直连 IP / CIDR · 每行一条", "multi"},
	{dnsChina, dnsNetPage, 230, 166, 273, 30, "国内 DNS（可留空）", "edit"},
	{dnsOther, dnsNetPage, 523, 166, 274, 30, "其他 DNS", "edit"},
	{dnsDomains, dnsNetPage, 230, 234, 567, 66, "国内域名后缀 · 每行一个，自动包含 .cn", "multi"},
	{dnsCustom, dnsNetPage, 230, 334, 214, 33, "统一使用自定义 DNS", "check"},
	{dnsCustomAddr, dnsNetPage, 469, 334, 328, 32, "自定义 DNS 地址", "edit"},
}

func (u *desktopUI) setupNetworkControls() {
	u.checked = make(map[int]bool)
	u.button(openNetwork, prefsPage, 585, 463, 211, 34, "高级网络设置")
	for i, s := range []string{"常规", "进程模式", "WinTUN", "DNS 分流"} {
		u.button(tabGeneral+i, -2, 225+i*145, 93, 136, 33, s)
	}
	u.button(saveNetwork, -2, 635, 441, 162, 36, "保存本页")
	for _, f := range networkFields {
		switch f.kind {
		case "check":
			u.button(f.id, f.page, f.x, f.y, f.w, f.h, f.label)
		case "combo":
			u.input(f.id, f.page, f.x, f.y, f.w, 190, "COMBOBOX", 0x3|0x200000)
		default:
			style := uintptr(0x80)
			if f.kind == "multi" {
				style = 0x4 | 0x40 | 0x200000 | 0x1000
			}
			u.input(f.id, f.page, f.x, f.y, f.w, f.h, "EDIT", style)
		}
	}
	u.button(testSTUN, generalNetPage, 230, 441, 178, 36, "检测 UDP 映射")
	u.button(useQuick, generalNetPage, 605, 365, 192, 32, "使用选中的快捷节点")
}
func (u *desktopUI) paintNetwork(dc uintptr) {
	u.card(dc, 211, 134, 607, 352, panel)
	for _, f := range networkFields {
		if f.page == u.page && f.kind != "check" {
			u.text(dc, f.x, f.y-25, f.w, 23, 12, 500, muted, f.label)
		}
	}
	tips := map[int]string{generalNetPage: "CM 测完整隧道；TCP 和 ICMP 测物理入口。", processNetPage: "独立 DNS 可按进程识别。系统共享 DNS 无法还原原始进程。", adapterNetPage: "直连 IP 经物理网卡出站。更改后重新开启 TUN。", dnsNetPage: "多个上游以逗号分隔，支持 IP、IP:端口或 tcp:// 地址。"}
	u.text(dc, 230, 400, 565, 27, 13, 400, muted, tips[u.page])
}
func (u *desktopUI) setCheck(id int, on bool) {
	u.checked[id] = on
	for _, f := range networkFields {
		if f.id == id {
			marker := "□  "
			if on {
				marker = "✓  "
			}
			u.label(id, marker+u.tr(f.label))
			break
		}
	}
}
func (u *desktopUI) combo(id int, choices []string, sel int) {
	h := u.controls[id].h
	uc("SendMessageW", h, 0x14b, 0, 0)
	for _, s := range choices {
		p := utf(s)
		uc("SendMessageW", h, 0x143, 0, uintptr(unsafe.Pointer(p)))
		runtime.KeepAlive(p)
	}
	uc("SendMessageW", h, 0x14e, uintptr(sel), 0)
}
func (u *desktopUI) index(id int) int {
	return int(int32(uc("SendMessageW", u.controls[id].h, 0x147, 0, 0)))
}
func (u *desktopUI) loadNetworkPage() {
	g := u.ctrl
	g.mu.Lock()
	cfg := *g.cfg
	g.mu.Unlock()
	o := cfg.networkOptions()
	set := func(id int, v string) { setText(u.controls[id].h, v) }
	num := func(id, n int) { set(id, strconv.Itoa(n)) }
	switch u.page {
	case generalNetPage:
		host, p, e := net.SplitHostPort(cfg.Listen)
		if e != nil {
			host, p = "127.0.0.1", "1080"
		}
		set(netPort, p)
		u.setCheck(netLAN, host != "localhost" && !net.ParseIP(host).IsLoopback())
		num(netQuick, o.Quick)
		num(netHeartbeat, o.Heartbeat)
		num(netStartup, o.Startup)
		set(netSTUN, o.STUN)
		u.combo(netProbe, []string{"CM", "TCP", "ICMP"}, map[string]int{"CM": 0, "TCP": 1, "ICMP": 2}[o.Probe])
		u.combo(netLanguage, []string{"简体中文", "English", "System"}, map[string]int{"zh": 0, "en": 1, "system": 2}[o.Language])
		u.quickNodes = append([]NodeProfile(nil), cfg.Profiles[:min(len(cfg.Profiles), max(0, o.Quick))]...)
		choices := []string{}
		for _, p := range u.quickNodes {
			choices = append(choices, p.Name+" · "+p.ServerIP)
		}
		idx := cfg.ActiveIndex
		if idx < 0 || idx >= len(choices) {
			idx = 0
		}
		u.combo(quickList, choices, idx)
	case processNetPage:
		set(procNames, strings.Join(o.Processes, "\r\n"))
		num(procDelay, o.EchoDelay)
		for id, v := range map[int]bool{procTCP: o.TCP, procUDP: o.UDP, procEcho: o.Echo, procChildren: o.Children, procDNS: o.DNS, procScope: o.SelectedDNS, procProxyDNS: o.ProxyDNS} {
			u.setCheck(id, v)
		}
	case adapterNetPage:
		set(netAddress, o.Address)
		set(netMask, o.Mask)
		set(netGateway, o.Gateway)
		set(netBypass, strings.Join(o.Bypass, "\r\n"))
	case dnsNetPage:
		set(dnsChina, o.ChinaDNS)
		set(dnsOther, o.OtherDNS)
		set(dnsDomains, strings.Join(o.ChinaDomains, "\r\n"))
		u.setCheck(dnsCustom, o.CustomDNS != "")
		set(dnsCustomAddr, o.CustomDNS)
	}
	u.enableNetworkFields()
}
func (u *desktopUI) enableNetworkFields() {
	for id, on := range map[int]bool{procDelay: u.checked[procEcho], procScope: u.checked[procDNS], dnsCustomAddr: u.checked[dnsCustom], dnsChina: !u.checked[dnsCustom], dnsOther: !u.checked[dnsCustom], dnsDomains: !u.checked[dnsCustom]} {
		var v uintptr
		if on {
			v = 1
		}
		uc("EnableWindow", u.controls[id].h, v)
	}
}
func (u *desktopUI) networkCommand(id int) bool {
	if id < openNetwork || id > dnsDomains {
		return false
	}
	if id == openNetwork {
		u.setPage(generalNetPage)
		return true
	}
	if id >= tabGeneral && id <= tabDNS {
		u.setPage(generalNetPage + id - tabGeneral)
		return true
	}
	for _, f := range networkFields {
		if id == f.id && f.kind == "check" {
			u.setCheck(id, !u.checked[id])
			u.enableNetworkFields()
			return true
		}
	}
	g := u.ctrl
	switch id {
	case saveNetwork:
		err := u.saveNetworkPage()
		if err == nil {
			u.loadNetworkPage()
		}
		u.localFeedback("已保存；TUN 设置在下次开启时生效", err)
	case testSTUN:
		s := strings.TrimSpace(getText(u.controls[netSTUN].h))
		u.work(func() desktopResult {
			addr, e := stunMapping(s)
			return desktopResult{originPage: generalNetPage, message: "UDP 映射 · " + addr.String(), err: e}
		})
	case useQuick:
		i := u.index(quickList)
		if i < 0 || i >= len(u.quickNodes) {
			u.feedback("请选择快捷节点", true)
			return true
		}
		u.localFeedback("已切换节点", g.switchQuick(u.quickNodes[i]))
	}
	return true
}
func (g *GUIController) switchQuick(p NodeProfile) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	for i, n := range cfg.Profiles {
		if n == p {
			cfg.ActiveIndex = i
			cfg.ServerIP, cfg.MasterKey, cfg.TLSNode = p.ServerIP, p.MasterKey, p.TLSNode
			return g.useConfigLocked(&cfg)
		}
	}
	return fmt.Errorf("节点列表已更改")
}
func (u *desktopUI) saveNetworkPage() error {
	g := u.ctrl
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	o := cfg.networkOptions()
	str := func(id int) string { return strings.TrimSpace(getText(u.controls[id].h)) }
	num := func(id int) int {
		n, e := strconv.Atoi(str(id))
		if e != nil {
			return -999
		}
		return n
	}
	lines := func(id int) []string {
		var v []string
		for _, s := range strings.Split(str(id), "\n") {
			if s = strings.TrimSpace(s); s != "" {
				v = append(v, s)
			}
		}
		return v
	}
	switch u.page {
	case generalNetPage:
		port := num(netPort)
		if port < 1 || port > 65535 {
			return fmt.Errorf("请输入 1–65535 的端口")
		}
		host, _, e := net.SplitHostPort(cfg.Listen)
		if e != nil {
			host = "127.0.0.1"
		}
		share := u.checked[netLAN]
		if share && (host == "localhost" || net.ParseIP(host).IsLoopback()) {
			host = "0.0.0.0"
		}
		if !share && host != "localhost" && !net.ParseIP(host).IsLoopback() {
			host = "127.0.0.1"
		}
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		if addr != cfg.Listen && g.running {
			return fmt.Errorf("更改监听端口或共享范围前请断开连接")
		}
		cfg.Listen = addr
		o.LANOnly = share
		o.Quick, o.Heartbeat, o.Startup = num(netQuick), num(netHeartbeat), num(netStartup)
		o.STUN = str(netSTUN)
		o.Probe = map[int]string{0: "CM", 1: "TCP", 2: "ICMP"}[u.index(netProbe)]
		o.Language = map[int]string{0: "zh", 1: "en", 2: "system"}[u.index(netLanguage)]
	case processNetPage:
		o.Processes = lines(procNames)
		o.TCP, o.UDP, o.Echo, o.Children = u.checked[procTCP], u.checked[procUDP], u.checked[procEcho], u.checked[procChildren]
		o.EchoDelay = num(procDelay)
		o.DNS, o.SelectedDNS, o.ProxyDNS = u.checked[procDNS], u.checked[procScope], u.checked[procProxyDNS]
	case adapterNetPage:
		o.Address, o.Mask, o.Gateway = str(netAddress), str(netMask), str(netGateway)
		o.Bypass = lines(netBypass)
	case dnsNetPage:
		o.ChinaDNS, o.OtherDNS, o.ChinaDomains = str(dnsChina), str(dnsOther), lines(dnsDomains)
		o.CustomDNS = ""
		if u.checked[dnsCustom] {
			o.CustomDNS = str(dnsCustomAddr)
			if a, e := dnsAddresses(o.CustomDNS); e != nil || len(a) == 0 {
				return fmt.Errorf("请输入有效的自定义 DNS 地址")
			}
		}
	}
	if e := o.validate(); e != nil {
		return e
	}
	cfg.Network = &o
	if e := g.installConfigLocked(&cfg, nil); e != nil {
		return e
	}
	u.setLanguage(o.Language)
	return nil
}
