package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	resourcesPage = optionsEditorPage + 1 + iota
	resourceEditorPage
)
const (
	navResources = 500 + iota
	syncResources
	syncSite
	syncCountry
	syncASN
	editSite
	editCountry
	editASN
	resourceURL
	resourceOverrides
	saveResource
	cancelResource
	defaultResourceURL
)

var resourceCards = [...]struct {
	name, title, kind, hint string
	color                   int
}{
	{geoSiteFile, "域名分类", "GEOSITE", "分类,域名  ·  cn,example.com 加入，cn,!example.com 排除", mint},
	{geoIPFile, "IP 地区", "GEOIP", "IP 或 CIDR,标签  ·  203.0.113.0/24,cn  或  203.0.113.1,-", blue},
	{geoASNFile, "自治系统", "IP-ASN", "IP 或 CIDR,ASN  ·  203.0.113.0/24,13335  或  203.0.113.1,-", 0xc6a8ff},
}

func (u *desktopUI) setupResourceControls() {
	u.button(syncResources, resourcesPage, 651, 89, 145, 34, "同步全部")
	for i := range resourceCards {
		y := 155 + i*109
		u.button(syncSite+i, resourcesPage, 625, y, 78, 34, "同步")
		u.button(editSite+i, resourcesPage, 715, y, 80, 34, "编辑")
	}
	u.input(resourceURL, resourceEditorPage, 231, 150, 565, 32, "EDIT", 0x80)
	uc("SendMessageW", u.controls[resourceURL].h, 0xc5, 8192, 0)
	u.button(defaultResourceURL, resourceEditorPage, 642, 104, 154, 31, "恢复默认地址")
	u.input(resourceOverrides, resourceEditorPage, 231, 271, 565, 132, "EDIT", 0x4|0x40|0x200000|0x1000)
	uc("SendMessageW", u.controls[resourceOverrides].h, 0xc5, 1<<20, 0)
	u.button(cancelResource, resourceEditorPage, 231, 438, 95, 37, "返回资源")
	u.button(saveResource, resourceEditorPage, 647, 438, 149, 37, "保存编辑")
}

func (u *desktopUI) paintResources(dc uintptr) {
	if u.page == resourceEditorPage {
		card := resourceCards[u.resourceEdit]
		u.card(dc, 211, 86, 607, 112, panel)
		u.text(dc, 231, 103, 390, 32, 16, 600, fg, "同步地址")
		u.card(dc, 211, 210, 607, 209, panel)
		u.text(dc, 231, 216, 550, 29, 16, 600, fg, "本地修订")
		u.text(dc, 231, 244, 565, 24, 13, 400, muted, card.hint)
		return
	}
	u.text(dc, 212, 87, 417, 38, 14, 500, muted, "分流数据库")
	for i, card := range resourceCards {
		y := 133 + i*109
		u.card(dc, 211, y, 607, 98, panel)
		rounded(dc, u.rect(227, y+19, 5, 55), u.px(2), card.color, card.color)
		u.text(dc, 247, y+11, 170, 31, 20, 700, fg, card.title)
		u.text(dc, 414, y+13, 177, 27, 13, 600, card.color, card.kind)
		u.text(dc, 247, y+42, 340, 22, 13, 500, muted, card.name)
		res := u.resourceState.Resources[card.name]
		state := u.tr("未同步")
		if h := u.resourceState.GeoFiles[card.name]; len(h) == 64 {
			state = u.tr("已缓存") + "  ·  " + h[:8]
			if res.SyncedAt != 0 {
				state = u.tr("上次同步") + "  " + time.Unix(res.SyncedAt, 0).Local().Format("01-02 15:04") + "  ·  " + h[:8]
			}
			source, _ := res.source(card.name)
			lastURL := res.SyncedURL
			if lastURL == "" {
				lastURL = geoRelease + card.name
			}
			if source != lastURL {
				state += "  ·  " + u.tr("新地址待同步")
			}
		}
		u.text(dc, 247, y+66, 545, 21, 12, 400, muted, state)
	}
}

func (g *GUIController) applyRouting(s RouteSettings, refresh bool, selected []string) (*routeIndex, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	idx, hash, err := buildResourceRouting(ctx, g.cfgPath, s, refresh, selected, routeHTTPClient())
	if err != nil {
		return nil, err
	}
	s = s.withIndex(idx, hash)
	g.mu.Lock()
	defer g.mu.Unlock()
	cfg := *g.cfg
	cfg.Routing = s
	if err = g.installConfigLocked(&cfg, nil); err != nil {
		return nil, err
	}
	if idx.missing == nil {
		g.router.routes.Store(idx)
		g.routeLoadErr = nil
	} else if g.router.routes.Load() == nil {
		g.routeLoadErr = idx.missing
	}
	return idx, nil
}

func (u *desktopUI) resourceCommand(id int) bool {
	g := u.ctrl
	switch {
	case id >= editSite && id <= editASN:
		u.resourceEdit = id - editSite
		u.setPage(resourceEditorPage)
	case id == defaultResourceURL:
		u.setField(resourceURL, geoRelease+resourceCards[u.resourceEdit].name)
	case id == cancelResource:
		u.setPage(resourcesPage)
	case id == syncResources || id >= syncSite && id <= syncASN:
		names := geoFilenames[:]
		if id != syncResources {
			names = []string{resourceCards[id-syncSite].name}
		}
		format := u.tr("已同步 %d 个资源")
		u.work(func() desktopResult {
			g.mu.Lock()
			s := g.cfg.Routing
			g.mu.Unlock()
			idx, err := g.applyRouting(s, true, names)
			if err == nil && idx.missing != nil {
				return desktopResult{originPage: resourcesPage, message: fmt.Sprintf(format, len(names)) + " · " + idx.missing.Error()}
			}
			return desktopResult{originPage: resourcesPage, err: err, message: fmt.Sprintf(format, len(names))}
		})
		u.feedback(u.tr("正在同步")+" · "+strings.Join(names, ", "), false)
	case id == saveResource:
		name := resourceCards[u.resourceEdit].name
		address, edits := u.field(resourceURL), strings.ReplaceAll(u.field(resourceOverrides), "\r\n", "\n")
		if address == geoRelease+name {
			address = ""
		}
		message := name + " · " + u.tr("编辑已保存")
		u.work(func() desktopResult {
			g.mu.Lock()
			s := g.cfg.Routing
			g.mu.Unlock()
			res := s.Resources[name]
			res.URL, res.Overrides = address, edits
			idx, err := g.applyRouting(s.withResource(name, res), false, []string{})
			if err == nil && idx.missing != nil {
				return desktopResult{originPage: resourceEditorPage, message: message + " · " + idx.missing.Error()}
			}
			return desktopResult{originPage: resourceEditorPage, err: err, message: message}
		})
	default:
		return false
	}
	return true
}
