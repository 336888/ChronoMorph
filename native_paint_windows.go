package main

import (
	"fmt"
	"strings"
)

func (u *desktopUI) card(dc uintptr, x, y, w, h, fill int) {
	rounded(dc, u.rect(x, y, w, h), u.px(13), fill, edge)
}
func (u *desktopUI) line(dc uintptr, x, y, x2, y2, width, color int) {
	drawLine(dc, u.px(layoutX(x)), u.px(y), u.px(layoutX(x2)), u.px(y2), u.px(width), color)
}
func (u *desktopUI) paint(dc uintptr) {
	fillRect(dc, u.rect(0, 0, screenW, screenH), bg)
	fillRect(dc, u.rect(0, 0, 187, screenH), side)
	u.line(dc, 186, 0, 186, screenH, 1, edge)
	if u.icon != 0 {
		uc("DrawIconEx", dc, uintptr(u.px(17)), uintptr(u.px(27)), u.icon, uintptr(u.px(43)), uintptr(u.px(43)), 0, 0, 3)
	}
	u.text(dc, 75, 26, 117, 26, 16, 700, fg, "Slacker")
	u.text(dc, 68, 52, 105, 18, 11, 400, muted, AppVersion)
	u.line(dc, 20, 90, 164, 90, 1, edge)
	u.line(dc, 20, 484, 164, 484, 1, edge)
	dot := muted
	state := "本地代理已停止"
	if u.running {
		dot = mint
		state = "本地代理运行中"
	}
	rounded(dc, u.rect(22, 510, 8, 8), u.px(4), dot, dot)
	u.text(dc, 38, 498, 140, 31, 12, 500, muted, state)
	title := map[int]string{routesPage: "分流规则", modesPage: "进程模式", rulesEditorPage: "编辑模式", optionsEditorPage: "协议选项", generalNetPage: "网络设置", processNetPage: "进程模式", adapterNetPage: "WinTUN 设置", dnsNetPage: "DNS 分流", homePage: "加速器", resourcesPage: "资源", resourceEditorPage: "编辑资源", livePage: "实时连接", benchPage: "节点延迟测试", healthPage: "路由与 DNS 检查", nodesPage: "我的节点", logsPage: "连接日志", prefsPage: "设置", importPage: "导入节点", manualPage: "手动添加节点", chainPage: "链式代理", adsPage: "去广告", domainPage: "域名检查与放行"}[u.page]
	if u.page == resourceEditorPage {
		title = u.tr(title) + "  /  " + resourceCards[u.resourceEdit].name
	}
	u.text(dc, 212, 18, 488, 51, 29, 700, fg, title)
	switch u.page {
	case routesPage, modesPage, rulesEditorPage, optionsEditorPage:
		u.paintRulePage(dc)
	case generalNetPage, processNetPage, adapterNetPage, dnsNetPage:
		u.paintNetwork(dc)
	case homePage:
		u.paintAccelerator(dc)
	case resourcesPage, resourceEditorPage:
		u.paintResources(dc)
	case livePage:
		u.card(dc, 211, 90, 607, 397, panel)
		u.text(dc, 230, 96, 205, 28, 14, 600, fg, "筛选目标、进程或协议")
		u.text(dc, 230, 173, 565, 25, 13, 500, muted, u.flowStatus)
	case healthPage:
		u.card(dc, 211, 92, 607, 369, panel)
		u.text(dc, 230, 98, 563, 31, 15, 600, fg, "检查当前路由、节点应答与 DNS 解析")
	case benchPage:
		u.card(dc, 211, 92, 607, 369, panel)
		u.text(dc, 230, 104, 563, 30, 15, 600, fg, "节点往返延迟 · 按延迟排序，保留当前节点")
	case nodesPage:
		u.card(dc, 211, 92, 607, 384, panel)
		u.text(dc, 229, 100, 500, 26, 14, 600, muted, fmt.Sprintf("已保存的节点  ·  %d", len(u.profiles)))
		if len(u.profiles) == 0 {
			u.text(dc, 380, 416, 406, 37, 13, 400, muted, "可导入服务端分享链接，或手动填写 UDP 节点")
		}
	case logsPage:
		u.card(dc, 211, 92, 607, 398, panel)
	case prefsPage:
		u.card(dc, 211, 92, 607, 105, panel)
		u.text(dc, 233, 102, 450, 31, 16, 600, fg, "本地 HTTP / SOCKS4 / SOCKS5 监听地址")
		u.card(dc, 211, 211, 607, 148, panel)
		u.text(dc, 233, 221, 385, 28, 17, 600, fg, "TUN 虚拟网卡")
		u.text(dc, 233, 248, 440, 20, 12, 400, muted, "接管 IPv4 与公网 IPv6。路由检查位于连接日志。")
		u.line(dc, 232, 284, 794, 284, 1, edge)
		u.text(dc, 233, 299, 395, 27, 17, 600, fg, "登录 Windows 后启动")
		u.text(dc, 233, 327, 418, 21, 12, 400, muted, "对当前 Windows 用户生效")
		u.card(dc, 211, 375, 607, 85, panel)
		u.text(dc, 233, 388, 440, 26, 17, 600, fg, "仅 TLS 传输")
		u.text(dc, 233, 419, 286, 23, 12, 400, muted, "限制到节点的传输及自动切换。")
	case chainPage:
		u.card(dc, 211, 92, 607, 366, panel)
		u.text(dc, 231, 106, 565, 30, 17, 600, fg, "前置 CM 节点")
		exit := u.exitLabel
		if exit == "" {
			exit = "请先在「我的节点」选择出口"
		}
		u.text(dc, 231, 352, 565, 28, 14, 500, muted, "当前出口 · "+exit)
	case adsPage:
		u.card(dc, 211, 92, 607, 67, panel)
		u.text(dc, 231, 109, 430, 35, 15, 600, fg, u.adStatus)
		u.card(dc, 211, 174, 607, 199, panel)
		u.text(dc, 231, 179, 565, 31, 15, 600, fg, "订阅地址 · 每行一个 HTTPS 链接")
		u.card(dc, 211, 388, 607, 72, panel)
		u.text(dc, 231, 401, 366, 39, 14, 500, muted, "查看匹配的规则，处理误拦截")
	case domainPage:
		u.card(dc, 211, 92, 607, 108, panel)
		u.text(dc, 231, 99, 552, 31, 15, 600, fg, "输入域名或粘贴网页地址")
		u.card(dc, 211, 212, 607, 246, panel)
		u.text(dc, 231, 220, 565, 31, 15, 600, fg, "我的放行名单 · 每行一个域名，包含子域名")
	case importPage:
		u.card(dc, 211, 92, 607, 245, panel)
		u.text(dc, 230, 107, 540, 32, 15, 500, fg, "粘贴一个节点链接，可直接复制 Debian 菜单 4 中的一整行")
	case manualPage:
		u.card(dc, 211, 85, 607, 270, panel)
		u.text(dc, 227, 95, 490, 27, 14, 500, muted, "服务端 IP")
		u.text(dc, 227, 180, 490, 27, 14, 500, muted, "MasterKey · 64 位十六进制")
	}

}
func formatRate(v uint64) string {
	if v >= 1024*1024 {
		return fmt.Sprintf("%.1f MB/s", float64(v)/(1024*1024))
	}
	return fmt.Sprintf("%.1f KB/s", float64(v)/1024)
}

func (u *desktopUI) paintButton(item *drawItem) {
	id := int(item.id)
	c := u.controls[id]
	if c == nil {
		return
	}
	if id == toggleAction {
		u.powerButton(item, c.label)
		return
	}
	dc, r := item.dc, item.rect
	pressed := item.state&1 != 0
	focused := item.state&16 != 0
	fill, stroke, ink := 0x243341, 0x394b5a, fg
	align := uintptr(0x8825)
	page, symbol := navItem(id)
	nav := page != 0
	if nav {
		fill, stroke, ink = side, side, muted
		active := u.navActive(page)
		if active {
			fill, stroke, ink = 0x1c3938, 0x29524c, mint
		}
	} else if id == syncResources || id == saveResource || id == saveImport || id == saveManual || id == useNode || id == saveChainAction || id == saveSubsAction || id == saveAllowedAction || id == routeSave || id == modeSave || id == modeUse {
		fill, stroke, ink = mint, mint, 0x102922
	} else if id == pingAction {
		fill, stroke, ink = panel, panel, blue
	} else if (id == proxyAction && u.proxy) || (id == autoAction && u.auto) || (id == tlsOnlyAction && u.tlsOnly) || (id == tunAction && u.tun) {
		fill, stroke, ink = 0x1c3938, 0x29524c, mint
	}
	if pressed {
		fill = 0x385367
		ink = fg
	}
	if focused {
		stroke = blue
	}
	// Child controls paint their own corner pixels against the surface below.
	background := panel
	switch {
	case id == chooseLine:
		background = hero
	case nav:
		background = side
	case id == importAction || id == syncResources || id == saveResource || id == cancelResource:
		background = bg
	}
	fillRect(dc, r, background)
	rounded(dc, r, u.px(8), fill, stroke)
	if nav {
		a := r
		a.left += int32(u.px(12))
		a.right = a.left + int32(u.px(27))
		drawText(dc, a, u.font(18, 500), ink, symbol, 0x8825)
		r.left += int32(u.px(35))
		align = 0x8824
	}
	size := 14
	if id == pingAction {
		size = 12
	}
	drawText(dc, r, u.font(size, 600), ink, u.tr(strings.TrimSpace(c.label)), align)
}
