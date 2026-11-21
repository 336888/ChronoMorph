package main

import (
	"fmt"
	"time"
)

type rateSample struct{ down, up uint64 }
type speedHistory struct {
	last    int64
	samples []rateSample
}

func (h *speedHistory) record(now time.Time, down, up uint64) {
	sec := now.Unix()
	if sec == h.last {
		return
	}
	if sec != h.last+1 {
		h.samples = h.samples[:0]
	}
	if len(h.samples) == 32 {
		h.samples = append(h.samples[:0], h.samples[1:]...)
	}
	h.samples = append(h.samples, rateSample{down, up})
	h.last = sec
}

func (u *desktopUI) spark(dc uintptr, x, y, w, h int, download bool, ink int) {
	a := u.history.samples
	if len(a) < 2 || !u.running {
		return
	}
	value := func(v rateSample) uint64 {
		if download {
			return v.down
		}
		return v.up
	}
	var peak uint64 = 1024
	for _, p := range a {
		peak = max(peak, value(p))
	}
	for i := 1; i < len(a); i++ {
		x1 := x + w - (len(a)-i)*w/31
		x2 := x + w - (len(a)-1-i)*w/31
		u.line(dc, x1, y+h-int(float64(value(a[i-1]))/float64(peak)*float64(h)), x2, y+h-int(float64(value(a[i]))/float64(peak)*float64(h)), 2, ink)
	}
}

func (u *desktopUI) paintAccelerator(dc uintptr) {
	rounded(dc, u.rect(211, 86, 607, 170), u.px(19), hero, 0x33495e)
	state := "待机"
	dot := muted
	if u.running {
		state = "本地代理已启动"
		dot = mint
		if u.rtt >= 0 {
			state = "服务端可达"
		}
	}
	rounded(dc, u.rect(236, 109, 8, 8), u.px(4), dot, dot)
	u.text(dc, 254, 99, 255, 27, 13, 600, dot, state)
	node := u.nodeName
	if node == "" {
		node = u.ip
	}
	if node == "" {
		node = u.tr("尚未选择线路")
	}
	u.text(dc, 234, 131, 384, 48, 30, 700, fg, node)
	u.text(dc, 237, 184, 386, 28, 14, 400, muted, u.transport())
	type metric struct {
		x            int
		title, value string
		ink          int
	}
	ping, down, up := "—", "—", "—"
	if u.running {
		down, up = formatRate(u.rx), formatRate(u.tx)
		if u.rtt >= 0 {
			ping = fmt.Sprintf("%d ms", u.rtt)
		}
	}
	for i, m := range []metric{
		{211, "服务端延迟", ping, fg}, {417, "接收速率", down, blue}, {623, "发送速率", up, mint},
	} {
		u.card(dc, m.x, 271, 195, 100, panel)
		u.text(dc, m.x+16, 280, 125, 23, 13, 500, muted, m.title)
		u.text(dc, m.x+15, 303, 166, 40, 24, 700, m.ink, m.value)
		if i > 0 {
			u.spark(dc, m.x+17, 348, 161, 13, i == 1, m.ink)
		}
	}
	u.card(dc, 211, 390, 607, 95, panel)
	u.text(dc, 230, 395, 220, 36, 15, 600, fg, "系统代理")
	u.line(dc, 230, 437, 798, 437, 1, edge)
	u.text(dc, 230, 440, 220, 36, 15, 600, fg, "路由模式")
}

func (u *desktopUI) powerButton(item *drawItem, label string) {
	r, dc := item.rect, item.dc
	fillRect(dc, r, hero)
	cx, cy := int((r.left+r.right)/2), int((r.top+r.bottom)/2)
	ring := func(radius int, fill, edge int) {
		rad := u.px(radius)
		rounded(dc, winRect{int32(cx - rad), int32(cy - rad), int32(cx + rad), int32(cy + rad)}, rad, fill, edge)
	}
	ink := mint
	if item.state&1 != 0 {
		ink = fg
	}
	ring(65, hero, 0x35565a)
	ring(59, 0x264644, ink)
	ring(54, 0x152d32, 0x395351)
	if item.state&16 != 0 {
		ring(51, 0x152d32, ink)
	}
	// Draw a power symbol without depending on symbol-font coverage.
	pen := gc("CreatePen", 0, uintptr(u.px(3)), colorRef(ink))
	old := gc("SelectObject", dc, pen)
	x, y, rad := cx, cy-u.px(14), u.px(13)
	gc("Arc", dc, uintptr(x-rad), uintptr(y-rad), uintptr(x+rad), uintptr(y+rad), uintptr(x-u.px(5)), uintptr(y-rad), uintptr(x+u.px(5)), uintptr(y-rad))
	gc("SelectObject", dc, old)
	gc("DeleteObject", pen)
	drawLine(dc, x, y-u.px(16), x, y-u.px(2), u.px(3), ink)
	r.top = int32(cy + u.px(6))
	r.bottom = int32(cy + u.px(32))
	drawText(dc, r, u.font(15, 700), fg, u.tr(label), 0x8825)
}
