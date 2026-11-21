package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"
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
	livePage
	nodesPage
	chainPage
	adsPage
	logsPage
	prefsPage
	importPage
	manualPage
	domainPage
	benchPage
	healthPage
)
const (
	navHome = iota + 100
	navLive
	navNodes
	navChain
	navAds
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
	deleteNode
	tlsOnlyAction
	saveChainAction
	openDomainTools
	saveSubsAction
	pauseAdsAction
	checkDomainAction
	addAllowedAction
	saveAllowedAction
	addAdSource
	dropFlow
	copyFlow
	openBench
	runBench
	qualityAction
	routeCheck
	lockEntryAction
	block6Action
	recheckAction
)
const (
	nodesList = 200 + iota
	linkEdit
	ipEdit
	keyEdit
	listenEdit
	logEdit
	noticeControl
	chainList
	subscriptionEdit
	domainEdit
	allowedEdit
	flowQuery
	flowList
	flowDetail
	benchEdit
	healthEdit
)

type nativeControl struct {
	h     uintptr
	page  int
	label string
}
type desktopResult struct {
	health         string
	benchText      string
	originPage     int
	message        string
	err            error
	nextPage       int
	ping           int64
	measured       bool
	measuredClient *ClientMux
}
type desktopUI struct {
	qualityCancel                                     context.CancelFunc
	qualitySeq                                        uint64
	qualityResults                                    chan qualityResult
	liveIDs                                           []uint64
	liveRows                                          string
	owners                                            map[uint64]string
	flowInfo, flowStatus                              string
	benchText                                         string
	adStatus                                          string
	adPaused                                          bool
	entryLabel                                        string
	entryChoices                                      []string
	exitLabel                                         string
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
	running, proxy, global, tun, auto, tlsOnly        bool
	ip, listen, nodeName                              string
	webName, realityName                              string
	tlsPort, udpPort                                  int
	current                                           *ClientMux
	profiles                                          []NodeProfile
	active                                            int
	nodeRows                                          string
	rtt                                               int64
	tx, rx                                            uint64
	logs                                              string
}

type qualityResult struct {
	seq    uint64
	report string
}

var activeDesktop *desktopUI

func startDesktop(ctrl *GUIController) (*desktopUI, error) {
	u := &desktopUI{ctrl: ctrl, qualityResults: make(chan qualityResult, 2), fonts: map[[2]int]uintptr{}, controls: map[int]*nativeControl{}, results: make(chan desktopResult, 1), rtt: -1, active: -1}
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
	for i, name := range []string{"连接概览", "实时连接", "我的节点", "链式代理", "去广告", "连接日志", "设置"} {
		u.button(navHome+i, 0, 14, 114+i*49, 158, 41, name)
	}
	u.setupLiveControls()
	u.button(importAction, 0, 704, 27, 113, 37, "导入节点")
	u.button(toggleAction, homePage, 650, 130, 139, 43, "启动连接")
	u.button(pingAction, homePage, 721, 239, 69, 29, "测速")
	u.button(proxyAction, homePage, 686, 356, 102, 37, "开启")
	u.button(modeAction, homePage, 669, 430, 119, 37, "智能分流")
	u.input(nodesList, nodesPage, 223, 134, 568, 188, "LISTBOX", 0x200001|0x100) // notify, scroll, no integral height
	u.button(useNode, nodesPage, 223, 346, 134, 39, "使用选中节点")
	u.button(bestNode, nodesPage, 370, 346, 111, 39, "测速优选")
	u.button(exportNode, nodesPage, 493, 346, 128, 39, "复制当前链接")
	u.button(deleteNode, nodesPage, 651, 346, 139, 39, "删除选中节点")
	u.button(manualAction, nodesPage, 223, 415, 140, 36, "手动添加 UDP")
	u.button(openBench, nodesPage, 648, 415, 142, 36, "节点延迟测试")
	u.input(benchEdit, benchPage, 230, 141, 563, 240, "EDIT", 0x200000|0x4|0x40|0x800)
	u.button(qualityAction, benchPage, 230, 399, 215, 37, "60 秒线路质量测试")
	u.input(healthEdit, healthPage, 230, 135, 565, 220, "EDIT", 0x200000|0x4|0x40|0x800)
	u.button(recheckAction, healthPage, 650, 395, 142, 37, "重新检查")
	u.button(block6Action, healthPage, 230, 395, 285, 37, "IPv6 经隧道")
	u.button(routeCheck, logsPage, 225, 440, 160, 37, "检查路由与 DNS")
	u.button(lockEntryAction, prefsPage, 530, 398, 146, 36, "锁定备用类型")
	u.button(runBench, benchPage, 646, 399, 147, 37, "重新测试")
	u.input(logEdit, logsPage, 220, 102, 581, 320, "EDIT", 0x200000|0x4|0x40|0x800)
	u.button(copyLog, logsPage, 672, 440, 129, 37, "复制诊断报告")
	u.input(listenEdit, prefsPage, 223, 140, 369, 35, "EDIT", 0x80)
	u.button(saveListen, prefsPage, 644, 138, 144, 39, "保存监听地址")
	u.button(tunAction, prefsPage, 685, 224, 104, 36, "开启 TUN")
	u.button(autoAction, prefsPage, 685, 302, 104, 36, "开启")
	u.button(tlsOnlyAction, prefsPage, 685, 398, 104, 36, "开启")
	u.input(chainList, chainPage, 231, 145, 565, 195, "LISTBOX", 0x200001|0x100)
	u.button(saveChainAction, chainPage, 231, 391, 146, 40, "保存链路")
	u.input(subscriptionEdit, adsPage, 231, 215, 565, 91, "EDIT", 0x4|0x40|0x200000|0x1000)
	u.button(addAdSource, adsPage, 231, 320, 187, 36, "添加 217heidai 订阅")
	u.button(saveSubsAction, adsPage, 620, 320, 177, 36, "保存并更新订阅")
	u.button(pauseAdsAction, adsPage, 670, 110, 127, 36, "暂停过滤")
	u.button(openDomainTools, adsPage, 616, 403, 181, 37, "域名检查与放行")
	u.input(domainEdit, domainPage, 231, 137, 345, 35, "EDIT", 0x80)
	u.button(checkDomainAction, domainPage, 590, 137, 89, 35, "检查")
	u.button(addAllowedAction, domainPage, 687, 137, 110, 35, "放行此域名")
	u.input(allowedEdit, domainPage, 231, 261, 565, 119, "EDIT", 0x4|0x40|0x200000|0x1000)
	u.button(saveAllowedAction, domainPage, 642, 397, 155, 37, "保存放行名单")
	u.input(linkEdit, importPage, 224, 151, 563, 85, "EDIT", 0x4|0x40|0x200000)
	u.button(saveImport, importPage, 224, 262, 130, 40, "导入并使用")
	u.button(pasteImport, importPage, 365, 262, 140, 40, "从剪贴板粘贴")
	u.button(backAction, -1, 690, 262, 96, 40, "返回")
	u.input(ipEdit, manualPage, 224, 130, 560, 36, "EDIT", 0x80)
	u.input(keyEdit, manualPage, 224, 214, 560, 36, "EDIT", 0x80|0x20)
	u.button(saveManual, manualPage, 224, 284, 132, 40, "保存节点")
}

func (u *desktopUI) setPage(page int) {
	if u.qualityCancel != nil {
		u.qualityCancel()
		u.qualityCancel = nil
		u.qualitySeq++
		u.label(qualityAction, "60 秒线路质量测试")
	}
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
	case benchPage:
		setText(u.controls[benchEdit].h, u.benchText)
	case chainPage:
		g := u.ctrl
		g.mu.Lock()
		cfg := *g.cfg
		g.mu.Unlock()
		h := u.controls[chainList].h
		uc("SendMessageW", h, 0x184, 0, 0)
		u.entryChoices = nil
		appendChoice := func(id, name string) {
			u.entryChoices = append(u.entryChoices, id)
			p := utf(name)
			uc("SendMessageW", h, 0x180, 0, uintptr(unsafe.Pointer(p)))
			runtime.KeepAlive(p)
		}
		appendChoice("", "关闭链式代理")
		idx := 0
		u.exitLabel = cfg.ServerIP
		for _, p := range cfg.Profiles {
			if p.selectedBy(&cfg) {
				u.exitLabel = p.Name
				continue
			}
			appendChoice(p.entryID(), p.Name+"  ·  "+p.ServerIP)
			if p.entryID() == cfg.ChainEntry {
				idx = len(u.entryChoices) - 1
			}
		}
		uc("SendMessageW", h, 0x186, uintptr(idx), 0)
	case adsPage, domainPage:
		g := u.ctrl
		g.mu.Lock()
		src, allowed := strings.Join(g.cfg.AdSubscriptions, "\r\n"), strings.Join(g.cfg.AdAllowDomains, "\r\n")
		g.mu.Unlock()
		if page == adsPage {
			setText(u.controls[subscriptionEdit].h, src)
		} else {
			setText(u.controls[allowedEdit].h, allowed)
		}
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
		u.nodeRows = "\x00"
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
	if u.page == adsPage {
		u.adPaused = g.cfg.AdPaused
		r := g.router.ads.Load()
		switch {
		case len(g.cfg.AdSubscriptions) == 0:
			u.adStatus = "尚未添加订阅"
		case r == nil:
			u.adStatus = "等待载入规则"
		default:
			u.adStatus = fmt.Sprintf("%d 个订阅 · %d 条拦截规则 · %d 条例外", len(g.cfg.AdSubscriptions), len(r.blocks), len(r.exceptions))
		}
		if u.adPaused {
			u.adStatus = "已暂停 · " + u.adStatus
		}
	}
	u.running, u.proxy, u.global = g.running, g.sysProxyOn, g.router.IsGlobalMode()
	u.ip, u.listen, u.tlsPort, u.udpPort = g.cfg.ServerIP, g.cfg.Listen, g.cfg.TLSPort, g.cfg.UDPPort
	u.profiles = append(u.profiles[:0], g.cfg.Profiles...)
	u.webName, u.realityName = g.cfg.WebName, g.cfg.RealityName
	block6 := g.cfg.TUNBlockIPv6
	locked := g.cfg.LockEntryType
	u.tlsOnly = g.cfg.TLSOnly
	u.entryLabel = ""
	if entry, ok := g.cfg.entryProfile(); ok {
		u.entryLabel = entry.Name
	}
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
	lockLabel := "锁定备用类型"
	if locked {
		lockLabel = "备用类型已锁定"
	}
	u.label(lockEntryAction, lockLabel)
	v6 := "IPv6 经隧道 · 点击切换"
	if block6 {
		v6 = "阻止 TUN 内 IPv6 · 点击切换"
	}
	u.label(block6Action, v6)
	u.label(tlsOnlyAction, onLabel(u.tlsOnly))
	pauseLabel := "暂停过滤"
	if u.adPaused {
		pauseLabel = "恢复过滤"
	}
	u.label(pauseAdsAction, pauseLabel)
	if u.page == nodesPage {
		enable := uintptr(0)
		if len(u.profiles) > 0 {
			enable = 1
		}
		uc("EnableWindow", u.controls[deleteNode].h, enable)
		var rows []string
		for i, p := range u.profiles {
			name := p.Name
			if name == "" {
				name = p.ServerIP
			}
			t := "UDP"
			if p.RealityName != "" {
				t = "REALITY · " + p.RealityName
			} else if p.WebName != "" {
				t = "HTTPS · " + p.WebName
			} else if u.tlsOnly && p.TLSPort == 0 {
				t = "仅 UDP · 此模式不可用"
			} else if p.UDPPort != 0 && !u.tlsOnly {
				t = fmt.Sprintf("自动 · TCP %d / UDP %d", p.TLSPort, p.UDPPort)
			} else if p.TLSPort != 0 {
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
	if u.page == livePage {
		u.refreshFlows()
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
	case result := <-u.qualityResults:
		if u.page == benchPage && result.seq == u.qualitySeq {
			u.qualityCancel()
			u.qualityCancel = nil
			u.benchText = result.report
			setText(u.controls[benchEdit].h, result.report)
			u.label(qualityAction, "60 秒线路质量测试")
		}
		return
	default:
	}
	select {
	case r := <-u.results:
		u.busy = false
		if r.health != "" && u.page == healthPage {
			setText(u.controls[healthEdit].h, strings.ReplaceAll(r.health, "\n", "\r\n"))
		}
		if r.benchText != "" {
			u.benchText = r.benchText
			setText(u.controls[benchEdit].h, r.benchText)
		}
		if r.originPage != 0 && r.originPage != u.page {
			if r.err != nil {
				log.Printf("[去广告] %v", r.err)
			}
			u.refresh()
			return
		}
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
	// Local policy edits may proceed while an unrelated subscription downloads.
	switch id {
	case copyFlow, dropFlow, checkDomainAction, openDomainTools, saveChainAction, pauseAdsAction, addAllowedAction, saveAllowedAction:
	default:
		if u.busy {
			return
		}
	}
	switch id {
	case copyFlow, dropFlow:
		u.flowAction(id)
	case lockEntryAction:
		u.localFeedback("已保存备用入口策略", g.toggleLockEntry())
	case block6Action:
		g.mu.Lock()
		if GlobalTUN.IsActive() {
			g.mu.Unlock()
			u.feedback("先关闭 TUN，再更改 IPv6 策略", true)
			return
		}
		cfg := *g.cfg
		cfg.TUNBlockIPv6 = !cfg.TUNBlockIPv6
		err := g.installConfigLocked(&cfg, nil)
		g.mu.Unlock()
		u.localFeedback("IPv6 策略已保存", err)
	case routeCheck, recheckAction:
		u.setPage(healthPage)
		setText(u.controls[healthEdit].h, "正在检查系统路由、节点应答和本地 DNS…")
		u.work(func() desktopResult { return desktopResult{originPage: healthPage, health: g.networkCheck()} })
	case qualityAction:
		if u.qualityCancel != nil {
			u.qualityCancel()
			return
		}
		g.mu.Lock()
		cm := g.cm
		g.mu.Unlock()
		if cm == nil {
			u.feedback("请先启动连接", true)
			return
		}
		setText(u.controls[benchEdit].h, "测量本机到出口的延迟，前 30 秒不产生下载，后 30 秒最多下载 128 MiB…")
		ctx, cancel := context.WithCancel(context.Background())
		u.qualityCancel = cancel
		u.qualitySeq++
		seq := u.qualitySeq
		u.label(qualityAction, "停止质量测试")
		go func() {
			report := measureQuality(ctx, cm, 30*time.Second, 128<<20)
			if ctx.Err() != nil {
				report += "\r\n测试已停止"
			}
			u.qualityResults <- qualityResult{seq, report}
			notifyWindow(u.hwnd, msgResult)
		}()
	case openBench, runBench:
		u.setPage(benchPage)
		setText(u.controls[benchEdit].h, "正在测试已保存的节点…")
		u.work(func() desktopResult {
			results := g.BenchAllProfiles()
			var lines []string
			for _, r := range results {
				status := "无应答"
				if r.RTTMs >= 0 {
					status = fmt.Sprintf("%d ms", r.RTTMs)
				}
				lines = append(lines, fmt.Sprintf("%s     %s     %s", status, r.Name, r.IP))
			}
			if len(lines) == 0 {
				lines = []string{"尚未添加节点"}
			}
			return desktopResult{originPage: benchPage, benchText: strings.Join(lines, "\r\n"), message: "延迟测试完成"}
		})
	case openDomainTools:
		u.setPage(domainPage)
	case addAdSource:
		h := u.controls[subscriptionEdit].h
		lines := strings.Fields(getText(h))
		for _, line := range lines {
			if line == defaultAdSource {
				return
			}
		}
		setText(h, strings.Join(append(lines, defaultAdSource), "\r\n"))
	case saveChainAction:
		idx := int(int32(uc("SendMessageW", u.controls[chainList].h, 0x188, 0, 0)))
		if idx < 0 || idx >= len(u.entryChoices) {
			u.feedback("请选择前置节点，或选择关闭链式代理", true)
			return
		}
		entry := u.entryChoices[idx]
		u.localFeedback("链路已保存", g.saveChain(entry))
	case saveSubsAction:
		sources := getText(u.controls[subscriptionEdit].h)
		u.work(func() desktopResult {
			n, err := g.saveSubscriptions(sources)
			return desktopResult{originPage: adsPage, message: fmt.Sprintf("订阅已保存 · %d 条拦截规则", n), err: err}
		})
	case pauseAdsAction:
		u.localFeedback("", g.toggleAdPause())
	case checkDomainAction:
		d, err := inputDomain(getText(u.controls[domainEdit].h))
		if err != nil {
			u.feedback(err.Error(), true)
			return
		}
		u.feedback(g.router.filter(d).String(), false)
	case addAllowedAction, saveAllowedAction:
		list := getText(u.controls[allowedEdit].h)
		if id == addAllowedAction {
			d, err := inputDomain(getText(u.controls[domainEdit].h))
			if err != nil {
				u.feedback(err.Error(), true)
				return
			}
			list += "\n" + d
		}
		err := g.saveAllowList(list)
		if err == nil {
			u.setPage(domainPage)
		}
		u.localFeedback("放行名单已保存", err)
	case importAction:
		u.setPage(importPage)
	case manualAction:
		u.setPage(manualPage)
	case backAction:
		u.setPage(nodesPage)
	case pasteImport:
		h := u.controls[linkEdit].h
		uc("SetFocus", h)
		uc("SendMessageW", h, 0x00b1, 0, ^uintptr(0)) // EM_SETSEL: replace the previous paste.
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
	case deleteNode:
		idx := int(int32(uc("SendMessageW", u.controls[nodesList].h, 0x188, 0, 0)))
		if idx < 0 || idx >= len(u.profiles) {
			u.feedback("请先选择节点", true)
			return
		}
		p := u.profiles[idx]
		name := p.Name
		if name == "" {
			name = p.ServerIP
		}
		msg := "从此电脑删除节点「" + name + "」？"
		if idx == u.active && u.running {
			msg += " 当前连接将断开。"
		}
		if uc("MessageBoxW", u.hwnd, uintptr(unsafe.Pointer(utf(msg))), uintptr(unsafe.Pointer(utf("删除选中节点"))), 0x124) != 6 {
			return
		}
		u.work(func() desktopResult {
			return desktopResult{err: g.deleteProfile(idx, p), nextPage: nodesPage, message: "已删除选中节点"}
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
			return desktopResult{err: g.toggleTUN()}
		})
	case tlsOnlyAction:
		enable := !u.tlsOnly
		u.work(func() desktopResult { return desktopResult{err: g.setTLSOnly(enable)} })
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

func (u *desktopUI) localFeedback(success string, err error) {
	if err != nil {
		u.feedback(err.Error(), true)
	} else {
		u.feedback(success, false)
	}
	u.refresh()
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
	if u.current != nil {
		return u.current.routeLabel()
	}
	if u.entryLabel != "" {
		return "配置 · " + u.entryLabel + " → " + u.nodeName
	}
	if u.ip == "" {
		return "尚未配置节点"
	}
	if u.realityName != "" {
		return "REALITY · " + net.JoinHostPort(u.realityName, strconv.Itoa(u.tlsPort))
	}
	if u.webName != "" {
		return "HTTPS · " + net.JoinHostPort(u.webName, strconv.Itoa(u.tlsPort))
	}
	if u.tlsOnly {
		if u.tlsPort == 0 {
			return "仅 TLS · 请导入支持 TLS 的节点"
		}
		return fmt.Sprintf("仅 TLS 1.3 / TCP · %s", net.JoinHostPort(u.ip, strconv.Itoa(u.tlsPort)))
	}
	if u.udpPort != 0 {
		return "TCP + UDP 自动 · 网页通道 " + u.current.transportName()
	}
	if u.tlsPort != 0 {
		return fmt.Sprintf("TLS 1.3 / TCP · %s", net.JoinHostPort(u.ip, strconv.Itoa(u.tlsPort)))
	}
	return "UDP · " + u.ip
}
