package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	routesPage = dnsNetPage + 1 + iota
	modesPage
	rulesEditorPage
	optionsEditorPage
)
const (
	navRoutes = 400 + iota
	navModes
	routeManual
	routeFeedsEdit
	routePreset
	routeSave
	routeTarget
	routeTest
	modePick
	modeUse
	modeNew
	modeNewRoute
	modeEdit
	modeDelete
	modeRemark
	modeFile
	modeHandle
	modeBypass
	modeChooseIn
	modeScanIn
	modeChooseOut
	modeScanOut
	modeVerify
	modeSave
	modeOptions
	modeBack
	modeOptsBack
	modeTCP
	modeUDP
	modeChildren
	modeDNS
	modeOwnDNS
	modeProxyDNS
	modeEcho
	modeLANCheck
	modeLoop
	modeUpstream
	modeDelay
)

var navigation = []struct {
	id, page    int
	label, icon string
}{
	{navHome, homePage, "连接概览", "◉"}, {navLive, livePage, "实时连接", "↔"}, {navNodes, nodesPage, "我的节点", "◇"},
	{navChain, chainPage, "链式代理", "⇄"}, {navAds, adsPage, "去广告", "⊘"}, {navRoutes, routesPage, "分流规则", "⑂"},
	{navModes, modesPage, "进程模式", "▦"}, {navLogs, logsPage, "连接日志", "≡"}, {navPrefs, prefsPage, "设置", "⚙"},
}

func navItem(id int) (page int, icon string) {
	for _, n := range navigation {
		if n.id == id {
			return n.page, n.icon
		}
	}
	return 0, ""
}
func (u *desktopUI) navActive(page int) bool {
	if u.page == page {
		return true
	}
	switch u.page {
	case domainPage:
		return page == adsPage
	case manualPage, importPage, benchPage:
		return page == nodesPage
	case rulesEditorPage, optionsEditorPage:
		return page == modesPage
	case generalNetPage, processNetPage, adapterNetPage, dnsNetPage:
		return page == prefsPage
	case healthPage:
		return page == logsPage
	}
	return false
}

type modeEditor struct {
	draft      TrafficMode
	original   string
	names      []string
	generation int
}

var modeChecks = []struct {
	id    int
	label string
	value func(*TrafficMode) *bool
}{
	{modeTCP, "处理 TCP", func(m *TrafficMode) *bool { return &m.TCP }}, {modeUDP, "处理 UDP", func(m *TrafficMode) *bool { return &m.UDP }},
	{modeChildren, "包含子进程", func(m *TrafficMode) *bool { return &m.Children }}, {modeDNS, "接管 53 端口 DNS", func(m *TrafficMode) *bool { return &m.DNS }},
	{modeOwnDNS, "仅指定进程的 DNS", func(m *TrafficMode) *bool { return &m.OwnDNS }}, {modeProxyDNS, "DNS 经过 CM 节点", func(m *TrafficMode) *bool { return &m.ProxyDNS }},
	{modeEcho, "本地回应 ICMP Echo", func(m *TrafficMode) *bool { return &m.Echo }}, {modeLANCheck, "处理局域网", func(m *TrafficMode) *bool { return &m.LAN }},
	{modeLoop, "处理本地回环", func(m *TrafficMode) *bool { return &m.Loopback }},
}

func (u *desktopUI) setupRuleControls() {
	multi := uintptr(0x4 | 0x40 | 0x200000 | 0x1000)
	for _, v := range []struct{ id, page, x, y, w, h int }{
		{routeManual, routesPage, 231, 133, 565, 98}, {routeFeedsEdit, routesPage, 231, 280, 565, 55}, {modeHandle, rulesEditorPage, 231, 219, 267, 141}, {modeBypass, rulesEditorPage, 525, 219, 270, 141},
	} {
		u.input(v.id, v.page, v.x, v.y, v.w, v.h, "EDIT", multi)
		uc("SendMessageW", u.controls[v.id].h, 0xc5, 1<<20, 0)
	}
	u.input(routeTarget, routesPage, 231, 425, 399, 32, "EDIT", 0x80)
	u.button(routePreset, routesPage, 231, 348, 209, 34, "添加 Apple 直连订阅")
	u.button(routeSave, routesPage, 624, 348, 172, 34, "保存并更新规则")
	u.button(routeTest, routesPage, 644, 424, 152, 34, "测试规则匹配")
	u.input(modePick, modesPage, 231, 155, 565, 210, "COMBOBOX", 0x3|0x200000)
	u.button(modeUse, modesPage, 231, 215, 145, 36, "使用选中模式")
	u.button(modeEdit, modesPage, 388, 215, 140, 36, "编辑模式")
	u.button(modeDelete, modesPage, 657, 215, 139, 36, "删除模式")
	u.button(modeNew, modesPage, 231, 366, 249, 40, "新建进程模式")
	u.button(modeNewRoute, modesPage, 497, 366, 299, 40, "新建 IP 路由表")
	u.input(modeRemark, rulesEditorPage, 231, 130, 270, 32, "EDIT", 0x80)
	u.input(modeFile, rulesEditorPage, 525, 130, 270, 32, "EDIT", 0x80)
	for i, id := range []int{modeChooseIn, modeScanIn, modeChooseOut, modeScanOut} {
		label := "选择目录"
		if i%2 == 1 {
			label = "扫描 EXE"
		}
		u.button(id, rulesEditorPage, 231+(i/2)*294+(i%2)*132, 369, 123, 31, label)
	}
	for i, b := range []struct {
		id    int
		label string
	}{{modeBack, "返回"}, {modeOptions, "协议选项"}, {modeVerify, "验证"}, {modeSave, "保存模式"}} {
		u.button(b.id, rulesEditorPage, 231+i*144, 430, 132, 35, b.label)
	}
	for i, b := range modeChecks {
		u.button(b.id, optionsEditorPage, 231+i%3*191, 131+i/3*56, 180, 36, b.label)
	}
	u.input(modeUpstream, optionsEditorPage, 231, 335, 349, 32, "EDIT", 0x80)
	u.input(modeDelay, optionsEditorPage, 605, 335, 190, 32, "EDIT", 0x80)
	u.button(modeOptsBack, optionsEditorPage, 610, 431, 185, 35, "返回规则编辑")
}
func (u *desktopUI) field(id int) string { return strings.TrimSpace(getText(u.controls[id].h)) }
func (u *desktopUI) setField(id int, s string) {
	setText(u.controls[id].h, strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n"))
}
func (u *desktopUI) paintRulePage(dc uintptr) {
	txt := func(x, y, w int, s string) { u.text(dc, x, y, w, 27, 14, 600, fg, s) }
	switch u.page {
	case routesPage:
		u.card(dc, 211, 92, 607, 151, panel)
		txt(231, 100, 565, "本地规则 · 类型,匹配值,DIRECT / PROXY / REJECT")
		u.card(dc, 211, 247, 607, 148, panel)
		txt(231, 249, 565, "订阅 · 每行填写动作、空格和 HTTPS 规则直链")
		u.card(dc, 211, 403, 607, 75, panel)
	case modesPage:
		u.card(dc, 211, 92, 607, 193, panel)
		txt(231, 108, 565, "选择已保存的模式")
		u.card(dc, 211, 308, 607, 162, panel)
		txt(231, 317, 565, "创建模式")
		u.text(dc, 231, 421, 565, 25, 13, 400, muted, "进程模式匹配程序。路由表匹配目标 IP 或 CIDR。")
	case rulesEditorPage:
		u.card(dc, 211, 92, 607, 389, panel)
		txt(231, 98, 270, "备注")
		txt(525, 98, 270, "文件名")
		txt(231, 185, 270, "处理规则 · 代理")
		txt(525, 185, 270, "分流规则 · 直连优先")
		hint := "每行一个 EXE 名、完整路径、目录或 re: 表达式。"
		if u.editor.draft.Kind == routeMode {
			hint = "每行一个 IPv4、IPv6 或 CIDR。"
		}
		u.text(dc, 231, 401, 565, 24, 12, 400, muted, hint)
	case optionsEditorPage:
		u.card(dc, 211, 92, 607, 389, panel)
		txt(231, 101, 558, "进程接管选项")
		txt(231, 303, 345, "DNS 上游 · IP:端口")
		txt(605, 303, 190, "ICMP 延迟（毫秒）")
		u.text(dc, 231, 378, 565, 32, 12, 400, muted, "回环选项适用于经本地代理的 TCP。原生回环连接由 Windows 处理。")
	}
}
func (u *desktopUI) loadModes(selected string) {
	g := u.ctrl
	g.mu.Lock()
	name := g.cfg.ActiveMode
	g.mu.Unlock()
	list, e := listModes(g.cfgPath)
	if e != nil {
		u.feedback(e.Error(), true)
		return
	}
	u.editor.names = append([]string{""}, list...)
	labels := []string{u.tr("使用常规网络设置")}
	idx := 0
	if selected == "" {
		selected = name
	}
	for i, s := range u.editor.names {
		if i == 0 {
			continue
		}
		label := s
		if m, e := readMode(g.cfgPath, s); e == nil {
			label = m.Remark + "  ·  " + s
		}
		if s == name {
			label = "✓  " + label
		}
		labels = append(labels, label)
		if s == selected {
			idx = i
		}
	}
	u.combo(modePick, labels, idx)
}
func (u *desktopUI) beginMode(m TrafficMode, name string) {
	u.editor.generation++
	u.editor.draft = m
	u.editor.original = name
	u.setField(modeRemark, m.Remark)
	u.setField(modeFile, name)
	u.setField(modeHandle, strings.Join(m.Handle, "\n"))
	u.setField(modeBypass, strings.Join(m.Bypass, "\n"))
	uc("SendMessageW", u.controls[modeFile].h, 0xcf, boolWord(name != ""), 0)
	u.setField(modeUpstream, m.DNSHost)
	u.setField(modeDelay, strconv.Itoa(m.Delay))
	u.setModeChecks()
	u.setPage(rulesEditorPage)
}
func boolWord(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}
func (u *desktopUI) setModeChecks() {
	for _, b := range modeChecks {
		prefix := "□  "
		if *b.value(&u.editor.draft) {
			prefix = "✓  "
		}
		u.label(b.id, prefix+u.tr(b.label))
	}
	for _, v := range []struct {
		id int
		on bool
	}{{modeDelay, u.editor.draft.Echo}, {modeUpstream, u.editor.draft.DNS}, {modeOwnDNS, u.editor.draft.DNS}, {modeProxyDNS, u.editor.draft.DNS}} {
		uc("EnableWindow", u.controls[v.id].h, boolWord(v.on))
	}
}
func (u *desktopUI) collectMode() (TrafficMode, string, error) {
	m := u.editor.draft
	m.Remark = u.field(modeRemark)
	m.Handle = editorLines(u.field(modeHandle))
	m.Bypass = editorLines(u.field(modeBypass))
	if m.Kind == processMode {
		m.DNSHost = u.field(modeUpstream)
		if m.Echo {
			n, e := strconv.Atoi(u.field(modeDelay))
			if e != nil {
				return m, "", fmt.Errorf("ICMP 延迟无效")
			}
			m.Delay = n
		}
	}
	name := u.field(modeFile)
	if name == "" {
		name = m.Remark
	}
	n, e := modeFilename(name)
	if e != nil {
		return m, "", e
	}
	_, e = compileMode(m)
	return m, n, e
}
func editorLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			lines = append(lines, v)
		}
	}
	return lines
}
func chooseModeDir(hwnd uintptr) (string, error) {
	shell := windows.NewLazySystemDLL("shell32.dll")
	title := utf("选择程序目录")
	var display, path [windows.MAX_PATH]uint16
	info := struct {
		owner, root     uintptr
		display, title  *uint16
		flags           uint32
		callback, param uintptr
		image           int32
	}{owner: hwnd, display: &display[0], title: title, flags: 0x1 | 0x40}
	e := windows.CoInitializeEx(0, 2)
	if e == nil || e == windows.Errno(1) {
		defer windows.CoUninitialize()
	}
	ptr, _, _ := shell.NewProc("SHBrowseForFolderW").Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(info)
	if ptr == 0 {
		return "", nil
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(ptr))
	ok, _, _ := shell.NewProc("SHGetPathFromIDListW").Call(ptr, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return "", fmt.Errorf("无法读取目录路径")
	}
	return windows.UTF16ToString(path[:]), nil
}
func (u *desktopUI) ruleCommand(id int) bool {
	if id < navRoutes || id > modeDelay {
		return false
	}
	g := u.ctrl
	for _, b := range modeChecks {
		if b.id == id {
			v := b.value(&u.editor.draft)
			*v = !*v
			u.setModeChecks()
			return true
		}
	}
	switch id {
	case routePreset:
		u.setField(routeFeedsEdit, mergeRuleLines(u.field(routeFeedsEdit), []string{"DIRECT " + appleRouteURL}))
	case routeSave:
		s := RouteSettings{Manual: u.field(routeManual), Sources: u.field(routeFeedsEdit)}
		u.work(func() desktopResult {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			idx, hash, e := buildRouting(ctx, g.cfgPath, s, true, routeHTTPClient())
			count := 0
			if e == nil {
				s.Cache = hash
				g.mu.Lock()
				cfg := *g.cfg
				cfg.Routing = s
				e = g.installConfigLocked(&cfg, nil)
				if e == nil {
					g.router.routes.Store(idx)
					count = idx.count
				}
				g.mu.Unlock()
			}
			msg := fmt.Sprintf("已应用 %d 条分流规则", count)
			if idx != nil && idx.skipped > 0 {
				msg += fmt.Sprintf("，跳过 %d 条不支持的订阅规则", idx.skipped)
			}
			return desktopResult{originPage: routesPage, message: msg, err: e}
		})
	case routeTest:
		target := u.field(routeTarget)
		if target == "" {
			u.feedback("请输入域名或 IP", true)
			break
		}
		if strings.Contains(target, "://") {
			v, e := inputDomain(target)
			if e != nil {
				u.feedback(e.Error(), true)
				break
			}
			target = v
		}
		h := g.router.routes.Load().match(target)
		if d := g.router.filter(target); d.blocked {
			u.feedback(d.String(), false)
		} else if h.order != 0 {
			msg := h.action + "  ·  " + h.rule
			g.mu.Lock()
			chain := g.cfg.ChainEntry != ""
			g.mu.Unlock()
			if h.action == "DIRECT" && (g.router.IsGlobalMode() || chain) {
				msg += "（当前全局或链式连接优先）"
			}
			u.feedback(msg, false)
		} else if g.router.ShouldDirect(target) {
			u.feedback("DIRECT · 默认直连规则", false)
		} else {
			u.feedback("PROXY · 默认代理", false)
		}
	case modeNew, modeNewRoute:
		g.mu.Lock()
		o := g.cfg.networkOptions()
		g.mu.Unlock()
		kind := processMode
		if id == modeNewRoute {
			kind = routeMode
		}
		u.beginMode(newTrafficMode(kind, o), "")
	case modeUse, modeEdit, modeDelete:
		i := u.index(modePick)
		if i < 0 || i >= len(u.editor.names) {
			u.feedback("请选择模式", true)
			break
		}
		name := u.editor.names[i]
		if id == modeUse {
			if name != "" {
				m, e := readMode(g.cfgPath, name)
				if e == nil {
					_, e = compileMode(m)
				}
				if e != nil {
					u.feedback(e.Error(), true)
					break
				}
			}
			g.mu.Lock()
			cfg := *g.cfg
			cfg.ActiveMode = name
			e := g.installConfigLocked(&cfg, nil)
			g.mu.Unlock()
			if e == nil {
				u.loadModes(name)
			}
			u.localFeedback("已选择，下次开启 TUN 时生效", e)
			break
		}
		if name == "" {
			u.feedback("请选择一个已保存的模式", true)
			break
		}
		if id == modeEdit {
			m, e := readMode(g.cfgPath, name)
			if e != nil {
				u.feedback(e.Error(), true)
				break
			}
			u.beginMode(m, name)
			break
		}
		g.mu.Lock()
		active := g.cfg.ActiveMode
		g.mu.Unlock()
		if name == active {
			u.feedback("请先切换到其他模式", true)
			break
		}
		if uc("MessageBoxW", u.hwnd, uintptr(unsafe.Pointer(utf("删除 "+name+"？"))), uintptr(unsafe.Pointer(utf("删除模式"))), 0x124) == 6 {
			e := os.Remove(filepath.Join(modeDir(g.cfgPath), name))
			u.loadModes("")
			u.localFeedback("模式已删除", e)
		}
	case modeOptions:
		u.setPage(optionsEditorPage)
	case modeBack:
		u.setPage(modesPage)
	case modeOptsBack:
		u.setPage(rulesEditorPage)
	case modeVerify, modeSave:
		m, name, e := u.collectMode()
		if e != nil {
			u.feedback(e.Error(), true)
			break
		}
		if id == modeVerify {
			u.feedback("规则验证通过", false)
			break
		}
		n, e := saveMode(g.cfgPath, name, m, u.editor.original == "")
		if e == nil {
			u.setPage(modesPage)
			u.loadModes(n)
		}
		u.localFeedback("模式已保存", e)
	case modeChooseIn, modeChooseOut, modeScanIn, modeScanOut:
		dir, e := chooseModeDir(u.hwnd)
		if e != nil || dir == "" {
			if e != nil {
				u.feedback(e.Error(), true)
			}
			break
		}
		field := modeHandle
		if id == modeChooseOut || id == modeScanOut {
			field = modeBypass
		}
		if id == modeChooseIn || id == modeChooseOut {
			u.setField(field, mergeRuleLines(u.field(field), []string{strings.TrimRight(dir, "\\/") + string(os.PathSeparator)}))
			break
		}
		gen := u.editor.generation
		u.work(func() desktopResult {
			paths, e := scanExecutables(dir)
			return desktopResult{originPage: rulesEditorPage, scanField: field, scanGen: gen, scanLines: paths, message: fmt.Sprintf("扫描到 %d 个 EXE", len(paths)), err: e}
		})
	}
	return true
}
