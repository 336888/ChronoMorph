package main

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"
	"unsafe"
)

func (u *desktopUI) setupLiveControls() {
	u.input(flowQuery, livePage, 230, 128, 565, 34, "EDIT", 0x80)
	u.input(flowList, livePage, 230, 201, 565, 168, "LISTBOX", 0x200001|0x100)
	u.input(flowDetail, livePage, 230, 379, 565, 52, "EDIT", 0x4|0x40|0x800|0x200000)
	u.button(copyFlow, livePage, 503, 440, 142, 34, "复制目标")
	u.button(dropFlow, livePage, 653, 440, 142, 34, "断开选中连接")
}

func visibleField(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

func (u *desktopUI) selectedFlowID() uint64 {
	idx := int(int32(uc("SendMessageW", u.controls[flowList].h, 0x188, 0, 0)))
	if idx >= 0 && idx < len(u.liveIDs) {
		return u.liveIDs[idx]
	}
	return 0
}

func (u *desktopUI) refreshFlows() {
	flows := u.ctrl.router.live.flows()
	u.owners = connectionOwners(flows, u.owners)
	query := strings.ToLower(strings.TrimSpace(getText(u.controls[flowQuery].h)))
	var rows []string
	var ids []uint64
	matches := 0
	for _, f := range flows {
		owner := u.owners[f.id]
		if owner == "" {
			owner = "进程未知"
		}
		proto := "TCP / " + f.protocol
		if f.protocol == "SOCKS5 UDP" {
			proto = f.protocol
		}
		line := fmt.Sprintf("%s    %s    %s    %s", proto, f.route, visibleField(*f.target.Load()), owner)
		if query != "" && !strings.Contains(strings.ToLower(line), query) {
			continue
		}
		matches++
		if len(ids) < 200 {
			rows = append(rows, line)
			ids = append(ids, f.id)
		}
	}
	u.flowStatus = fmt.Sprintf("活动 %d   ·   匹配 %d   ·   显示 %d", len(flows), matches, len(ids))
	h := u.controls[flowList].h
	key := strings.Join(rows, "\n")
	if key != u.liveRows || !slices.Equal(u.liveIDs, ids) {
		selected := u.selectedFlowID()
		top := int(int32(uc("SendMessageW", h, 0x18e, 0, 0)))
		var topID uint64
		if top > 0 && top < len(u.liveIDs) {
			topID = u.liveIDs[top]
		}
		uc("SendMessageW", h, 0x0b, 0, 0)
		uc("SendMessageW", h, 0x184, 0, 0)
		for _, row := range rows {
			p := utf(row)
			uc("SendMessageW", h, 0x180, 0, uintptr(unsafe.Pointer(p)))
			runtime.KeepAlive(p)
		}
		if i := slices.Index(ids, selected); i >= 0 {
			uc("SendMessageW", h, 0x186, uintptr(i), 0)
		}
		if i := slices.Index(ids, topID); i >= 0 {
			uc("SendMessageW", h, 0x197, uintptr(i), 0)
		}
		uc("SendMessageW", h, 0x0b, 1, 0)
		uc("InvalidateRect", h, 0, 1)
		u.liveRows, u.liveIDs = key, ids
	}
	u.updateFlowDetail()
}

func (u *desktopUI) selectedFlow() *liveFlow {
	id := u.selectedFlowID()
	m := u.ctrl.router.live
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id]
}

func (u *desktopUI) updateFlowDetail() {
	f := u.selectedFlow()
	info := "选择连接可查看收发量和持续时间"
	enable := uintptr(0)
	if f != nil {
		enable = 1
		label := "目标"
		if f.protocol == "SOCKS5 UDP" {
			label = "最近发送目标"
		}
		info = fmt.Sprintf("%s  %s\r\n上传 %s    下载 %s    持续 %s", label, visibleField(*f.target.Load()), flowBytes(f.up.Load()), flowBytes(f.down.Load()), time.Since(f.start).Truncate(time.Second))
	}
	if info != u.flowInfo {
		u.flowInfo = info
		setText(u.controls[flowDetail].h, info)
	}
	uc("EnableWindow", u.controls[copyFlow].h, enable)
	uc("EnableWindow", u.controls[dropFlow].h, enable)
}

func flowBytes(n uint64) string {
	for _, unit := range []struct {
		size uint64
		name string
	}{{1 << 30, "GiB"}, {1 << 20, "MiB"}, {1 << 10, "KiB"}} {
		if n >= unit.size {
			return fmt.Sprintf("%.1f %s", float64(n)/float64(unit.size), unit.name)
		}
	}
	return fmt.Sprintf("%d B", n)
}

func (u *desktopUI) flowAction(action int) {
	f := u.selectedFlow()
	if f == nil {
		u.feedback("请选择仍在活动的连接", true)
		return
	}
	if action == copyFlow {
		u.localFeedback("已复制目标", u.copy(visibleField(*f.target.Load())))
		return
	}
	go u.ctrl.router.live.disconnect(f.id)
	u.feedback("已请求断开选中连接", false)
}
