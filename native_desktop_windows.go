package main

import (
	"fmt"
	"log"
	"net"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
	"unsafe"
)

const (
	bg       = 0x0e1521
	side     = 0x101a28
	panel    = 0x172232
	edge     = 0x2a384a
	fg       = 0xeaf1f6
	muted    = 0xa4b5c7
	mint     = 0x51e4c3
	blue     = 0x71aaff
	editorBG = 0x101b29
)
const (
	homePage = iota + 1
	nodesPage
	logsPage
	prefsPage
	importPage
	manualPage
)
const (
	navHome = iota + 100
	navNodes
	navLogs
	navPrefs
	importAction
	toggleAction
	pingAction
	proxyAction
	modeAction
	useNode
	exportNode
	bestNode
	saveImport
	pasteImport
	backAction
	manualAction
	saveManual
	saveListen
	tunAction
	autoAction
	copyLog
)
const (
	nodesList = 200 + iota
	linkEdit
	ipEdit
	keyEdit
	listenEdit
	logEdit
	noticeControl
)

type nativeControl struct {
	h     uintptr
	page  int
	label string
}
type desktopResult struct {
	message        string
	err            error
	nextPage       int
	ping           int64
	measured       bool
	measuredClient *ClientMux
}
type desktopUI struct {
	ctrl                                              *GUIController
	hwnd, instance, icon, backgroundBrush, inputBrush uintptr
	scale                                             float64
	fonts                                             map[[2]int]uintptr
	controls                                          map[int]*nativeControl
	results                                           chan desktopResult
	page                                              int
	busy                                              bool
	message                                           string
	isError                                           bool
	running, proxy, global, tun, auto                 bool
	ip, listen, nodeName                              string
	tlsPort                                           int
	current                                           *ClientMux
	profiles                                          []NodeProfile
	active                                            int
	nodeRows                                          string
	rtt                                               int64
	tx, rx                                            uint64
	logs                                              string
}

var activeDesktop *desktopUI

func startDesktop(ctrl *GUIController) (*desktopUI, error) {
	u := &desktopUI{ctrl: ctrl, fonts: map[[2]int]uintptr{}, controls: map[int]*nativeControl{}, results: make(chan desktopResult, 1), rtt: -1, active: -1}
	ready := make(chan error, 1)
	go u.run(ready)
	return u, <-ready
}

func (u *desktopUI) button(id, page, x, y, w, h int, label string) {
	control := createControl("BUTTON", label, 0, 0x4001000b, u.rect(x, y, w, h), u.hwnd, uintptr(id), u.instance)
	u.controls[id] = &nativeControl{control, page, label}
}
func (u *desktopUI) input(id, page, x, y, w, h int, class string, style uintptr) {
	control := createControl(class, "", 0x200, 0x40010000|style, u.rect(x, y, w, h), u.hwnd, uintptr(id), u.instance)
	uc("SendMessageW", control, 0x30, u.font(15, 400), 1)
	u.controls[id] = &nativeControl{control, page, ""}
}
func (u *desktopUI) setupControls() {
	n := createControl("STATIC", "", 0, 0x40004000, u.rect(214, 506, 600, 28), u.hwnd, noticeControl, u.instance)
	uc("SendMessageW", n, 0x30, u.font(13, 400), 1)
	u.controls[noticeControl] = &nativeControl{n, 0, ""}
	for i, name := range []string{"连接概览", "我的节点", "连接日志", "设置"} {
		u.button(navHome+i, 0, 14, 114+i*49, 158, 41, name)
	}
	u.button(importAction, 0, 704, 27, 113, 37, "导入节点")
	u.button(toggleAction, homePage, 650, 130, 139, 43, "启动连接")
	u.button(pingAction, homePage, 721, 239, 69, 29, "测速")
	u.button(proxyAction, homePage, 686, 356, 102, 37, "开启")
	u.button(modeAction, homePage, 669, 430, 119, 37, "智能分流")
	u.input(nodesList, nodesPage, 223, 134, 568, 188, "LISTBOX", 0x200001|0x100) // notify, scroll, no integral height
	u.button(useNode, nodesPage, 223, 346, 134, 39, "使用选中节点")
	u.button(bestNode, nodesPage, 370, 346, 111, 39, "测速优选")
	u.button(exportNode, nodesPage, 493, 346, 128, 39, "复制当前链接")
	u.button(manualAction, nodesPage, 223, 415, 140, 36, "手动添加 UDP")
	u.input(logEdit, logsPage, 220, 102, 581, 320, "EDIT", 0x200000|0x4|0x40|0x800)
	u.button(copyLog, logsPage, 672, 440, 129, 37, "复制诊断报告")
	u.input(listenEdit, prefsPage, 223, 140, 369, 35, "EDIT", 0x80)
	u.button(saveListen, prefsPage, 644, 138, 144, 39, "保存监听地址")
	u.button(tunAction, prefsPage, 685, 224, 104, 36, "开启 TUN")
	u.button(autoAction, prefsPage, 685, 302, 104, 36, "开启")
	u.input(linkEdit, importPage, 224, 151, 563, 85, "EDIT", 0x4|0x40|0x200000)
	u.button(saveImport, importPage, 224, 262, 130, 40, "导入并使用")
	u.button(pasteImport, importPage, 365, 262, 140, 40, "从剪贴板粘贴")
	u.button(backAction, -1, 690, 262, 96, 40, "返回")
	u.input(ipEdit, manualPage, 224, 130, 560, 36, "EDIT", 0x80)
	u.input(keyEdit, manualPage, 224, 214, 560, 36, "EDIT", 0x80|0x20)
	u.button(saveManual, manualPage, 224, 284, 132, 40, "保存节点")
}

func (u *desktopUI) setPage(page int) {
	u.page = page
	u.message = ""
	u.isError = false
	for id, c := range u.controls {
		show := c.page == 0 || c.page == page || (id == backAction && (page == manualPage || page == importPage))
		if id == importAction && (page == importPage || page == manualPage) {
			show = false
		}
		var cmd uintptr
		if show {
			cmd = 5
		}
		uc("ShowWindow", c.h, cmd)
	}
	switch page {
	case importPage:
		setText(u.controls[linkEdit].h, "")
		uc("SetFocus", u.controls[linkEdit].h)
	case manualPage:
		setText(u.controls[keyEdit].h, "")
		setText(u.controls[ipEdit].h, "")
		uc("SetFocus", u.controls[ipEdit].h)
	case prefsPage:
		setText(u.controls[listenEdit].h, u.listen)
	case nodesPage:
		u.nodeRows = ""
	}
	u.refresh()
	uc("InvalidateRect", u.hwnd, 0, 0)
}
func (u *desktopUI) label(id int, s string) {
	if c := u.controls[id]; c != nil && c.label != s {
		c.label = s
		setText(c.h, s)
		uc("InvalidateRect", c.h, 0, 1)
	}
}
func (u *desktopUI) refresh() {
	g := u.ctrl
	if !g.mu.TryLock() {
		return
	}
	u.running, u.proxy, u.global = g.running, g.sysProxyOn, g.router.IsGlobalMode()
	u.ip, u.listen, u.tlsPort = g.cfg.ServerIP, g.cfg.Listen, g.cfg.TLSPort
	u.profiles = append(u.profiles[:0], g.cfg.Profiles...)
	u.active = g.cfg.ActiveIndex
	u.nodeName = ""
	if u.active >= 0 && u.active < len(u.profiles) && u.profiles[u.active].ServerIP == u.ip {
		u.nodeName = u.profiles[u.active].Name
	}
	if u.current != g.cm {
		u.rtt = -1
		u.current = g.cm
	}
	g.mu.Unlock()
	u.tx, u.rx = LiveStats.txSpeedBps.Load(), LiveStats.rxSpeedBps.Load()
	if u.page == prefsPage {
		u.auto = IsAutoStartEnabled()
		u.tun = GlobalTUN.IsActive()
	}
	btn := "启动连接"
	if u.running {
		btn = "断开连接"
	} else if u.ip == "" {
		btn = "添加第一个节点"
	}
	u.label(toggleAction, btn)
	u.label(proxyAction, onLabel(u.proxy))
	mode := "智能分流"
	if u.global {
		mode = "全局代理"
	}
	u.label(modeAction, mode)
	u.label(tunAction, onLabel(u.tun))
	u.label(autoAction, onLabel(u.auto))
	if u.page == nodesPage {
		var rows []string
		for i, p := range u.profiles {
			name := p.Name
			if name == "" {
				name = p.ServerIP
			}
			t := "UDP"
			if p.TLSPort != 0 {
				t = "TLS/TCP " + strconv.Itoa(p.TLSPort)
			}
			row := fmt.Sprintf("  %s     %s     %s", name, p.ServerIP, t)
			if i == u.active {
				row += "     · 当前"
			}
			rows = append(rows, row)
		}
		key := strings.Join(rows, "\n")
		if key != u.nodeRows {
			h := u.controls[nodesList].h
			uc("SendMessageW", h, 0x184, 0, 0)
			for _, r := range rows {
				p := utf(r)
				uc("SendMessageW", h, 0x180, 0, uintptr(unsafe.Pointer(p)))
				runtime.KeepAlive(p)
			}
			uc("SendMessageW", h, 0x186, uintptr(u.active), 0)
			u.nodeRows = key
		}
	}
	if u.page == logsPage {
		s := strings.Join(AppLogs.Snapshot(), "\r\n")
		if s == "" {
			s = "暂无连接日志"
		}
		if s != u.logs {
			setText(u.controls[logEdit].h, s)
			u.logs = s
		}
	}
	u.updateNotice()
	uc("InvalidateRect", u.hwnd, 0, 0)
}
func onLabel(v bool) string {
	if v {
		return "已开启"
	}
	return "未开启"
}
func (u *desktopUI) work(task func() desktopResult) {
	if u.busy {
		return
	}
	u.busy = true
	u.message = "处理中…"
	u.isError = false
	uc("InvalidateRect", u.hwnd, 0, 0)
	go func() { u.results <- task(); notifyWindow(u.hwnd, msgResult) }()
}
func (u *desktopUI) finish() {
	select {
	case r := <-u.results:
		u.busy = false
		if r.err != nil {
			u.message = r.err.Error()
			u.isError = true
			log.Printf("[桌面] %s", u.message)
		} else {
			if r.nextPage != 0 {
				u.setPage(r.nextPage)
			}
			u.message = r.message
			u.isError = false
		}
		if r.measured && u.current == r.measuredClient {
			u.rtt = r.ping
		}
	default:
	}
	u.refresh()
}
func (u *desktopUI) command(id int) {
	g := u.ctrl
	if id >= navHome && id <= navPrefs {
		u.setPage(id - navHome + 1)
		return
	}
	if u.busy {
		return
	}
	switch id {
	case importAction:
		u.setPage(importPage)
	case manualAction:
		u.setPage(manualPage)
	case backAction:
		u.setPage(nodesPage)
	case pasteImport:
		h := u.controls[linkEdit].h
		uc("SetFocus", h)
		uc("SendMessageW", h, 0x302, 0, 0)
	case toggleAction:
		if !u.running && u.ip == "" {
			u.setPage(importPage)
			return
		}
		running := u.running
		u.work(func() desktopResult {
			var err error
			if running {
				err = g.StopTunnel()
			} else {
				err = g.StartTunnel()
			}
			return desktopResult{err: err}
		})
	case pingAction:
		cm := u.current
		if cm == nil {
			u.feedback("请先启动连接", true)
			return
		}
		u.work(func() desktopResult {
			ms, err := MeasureTunnelLatency(cm)
			return desktopResult{measured: true, measuredClient: cm, ping: ms, err: err, message: "已收到服务端应答"}
		})
	case proxyAction:
		u.work(func() desktopResult { return desktopResult{err: g.toggleSystemProxy()} })
	case modeAction:
		u.work(func() desktopResult { return desktopResult{err: g.toggleRouteMode()} })
	case saveImport, saveManual:
		var p *ShareProfile
		var err error
		if id == saveImport {
			p, err = DecodeShareLink(getText(u.controls[linkEdit].h))
		} else {
			ip, key := strings.TrimSpace(getText(u.controls[ipEdit].h)), strings.TrimSpace(getText(u.controls[keyEdit].h))
			if _, err = NewClientMux(ip, key); err == nil {
				p = &ShareProfile{ServerIP: ip, MasterKey: key, Name: ip}
			}
		}
		if err != nil {
			u.feedback(err.Error(), true)
			return
		}
		u.work(func() desktopResult {
			return desktopResult{err: g.importProfile(p), nextPage: homePage, message: "节点已保存"}
		})
	case useNode:
		i := int32(uc("SendMessageW", u.controls[nodesList].h, 0x188, 0, 0))
		if i < 0 {
			u.feedback("请先选择节点", true)
			return
		}
		u.work(func() desktopResult {
			return desktopResult{err: g.SwitchProfile(int(i)), nextPage: homePage, message: "已切换节点"}
		})
	case bestNode:
		u.work(func() desktopResult {
			r := g.BenchAllProfiles()
			if len(r) == 0 || r[0].RTTMs < 0 {
				return desktopResult{err: fmt.Errorf("没有收到可用节点的应答")}
			}
			return desktopResult{err: g.SwitchProfile(r[0].Index), nextPage: homePage, message: fmt.Sprintf("已选择 %s · %d ms", r[0].Name, r[0].RTTMs)}
		})
	case exportNode:
		g.mu.Lock()
		p := ShareProfile{TLSNode: g.cfg.TLSNode, Name: u.nodeName, ServerIP: g.cfg.ServerIP, MasterKey: g.cfg.MasterKey}
		g.mu.Unlock()
		if p.ServerIP == "" {
			u.feedback("尚未配置节点", true)
			return
		}
		s, err := p.encode()
		if err == nil {
			err = u.copy(s)
		}
		if err != nil {
			u.feedback(err.Error(), true)
		} else {
			u.feedback("已复制节点链接，链接包含用户密钥", false)
		}
	case saveListen:
		addr := strings.TrimSpace(getText(u.controls[listenEdit].h))
		host, port, err := net.SplitHostPort(addr)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 || (host != "" && host != "localhost" && net.ParseIP(host) == nil) {
			u.feedback("监听地址无效，例如 127.0.0.1:1080", true)
			return
		}
		u.work(func() desktopResult {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.running {
				return desktopResult{err: fmt.Errorf("请先断开连接，再修改监听地址")}
			}
			cfg := *g.cfg
			cfg.Listen = addr
			return desktopResult{err: g.useConfigLocked(&cfg), message: "监听地址已保存"}
		})
	case tunAction:
		u.work(func() desktopResult {
			if GlobalTUN.IsActive() {
				GlobalTUN.StopTUN()
				return desktopResult{}
			}
			g.mu.Lock()
			defer g.mu.Unlock()
			if !g.running {
				return desktopResult{err: fmt.Errorf("请先启动连接")}
			}
			return desktopResult{err: GlobalTUN.StartTUN(g.proxyAddressLocked(), g.cfg.ServerIP, g.cm)}
		})
	case autoAction:
		u.work(func() desktopResult { return desktopResult{err: SetAutoStart(!IsAutoStartEnabled())} })
	case copyLog:
		if err := u.copy(g.BuildDiagReport()); err != nil {
			u.feedback(err.Error(), true)
		} else {
			u.feedback("已复制诊断报告", false)
		}
	}
}
func (u *desktopUI) updateNotice() {
	line := u.message
	if line == "" && u.page == homePage {
		listen := u.listen
		if listen == "" {
			listen = "127.0.0.1:1080"
		}
		line = "本地监听    " + listen
	}
	u.label(noticeControl, line)
}

func (u *desktopUI) feedback(s string, isError bool) {
	u.message = s
	u.isError = isError
	u.updateNotice()
	uc("InvalidateRect", u.hwnd, 0, 0)
}
func (u *desktopUI) copy(s string) error {
	if uc("OpenClipboard", u.hwnd) == 0 {
		return fmt.Errorf("剪贴板暂时不可用")
	}
	defer uc("CloseClipboard")
	data := append(utf16.Encode([]rune(s)), 0)
	buf := kc("GlobalAlloc", 2, uintptr(len(data)*2))
	if buf == 0 {
		return fmt.Errorf("剪贴板内存不足")
	}
	dst := kc("GlobalLock", buf)
	if dst == 0 {
		kc("GlobalFree", buf)
		return fmt.Errorf("无法写入剪贴板")
	}
	kc("RtlMoveMemory", dst, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2))
	runtime.KeepAlive(data)
	kc("GlobalUnlock", buf)
	uc("EmptyClipboard")
	if uc("SetClipboardData", 13, buf) == 0 {
		kc("GlobalFree", buf)
		return fmt.Errorf("写入剪贴板失败")
	}
	return nil
}
func (u *desktopUI) transport() string {
	if u.ip == "" {
		return "尚未配置节点"
	}
	if u.tlsPort != 0 {
		return fmt.Sprintf("TLS 1.3 / TCP · %s", net.JoinHostPort(u.ip, strconv.Itoa(u.tlsPort)))
	}
	return "UDP · " + u.ip
}
