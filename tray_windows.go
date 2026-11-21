//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/getlantern/systray"
)

//go:embed assets/tray.ico
var trayIcon []byte

func RunTray(ctrl *GUIController, showWindow, onExit func()) {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("Slacker · 桌面客户端")

		mOpen := systray.AddMenuItem("打开主窗口", "显示 Slacker 桌面窗口")
		systray.AddSeparator()
		mToggle := systray.AddMenuItem("连接 / 断开隧道", "快速启停加密隧道")
		mMode := systray.AddMenuItem("切换路由模式 (分流/全局)", "实时切换智能分流与全局代理")
		mBest := systray.AddMenuItem("自动优选最快节点", "并发测速并切换至延迟最低的 VPS")
		systray.AddSeparator()
		quickMenu := systray.AddMenuItem("快捷节点", "切换到已保存的节点")
		type slot struct {
			item    *systray.MenuItem
			profile NodeProfile
		}
		slots := make([]slot, 12)
		clicked := make(chan int, 12)
		done := make(chan struct{})
		for i := range slots {
			item := quickMenu.AddSubMenuItem(fmt.Sprint(i+1), "")
			item.Hide()
			slots[i].item = item
			go func() {
				for {
					select {
					case <-done:
						return
					case <-item.ClickedCh:
						select {
						case clicked <- i:
						case <-done:
							return
						}
					}
				}
			}()
		}
		mQuit := systray.AddMenuItem("彻底退出", "关闭隧道并还原系统代理")
		lastLanguage := ""
		refreshQuick := func() {
			ctrl.mu.Lock()
			cfg := ctrl.cfg
			ctrl.mu.Unlock()
			lang := uiLanguage(cfg.networkOptions().Language)
			if lang != lastLanguage {
				lastLanguage = lang
				for m, s := range map[*systray.MenuItem]string{mOpen: "打开主窗口", mToggle: "连接 / 断开隧道", mMode: "切换路由模式 (分流/全局)", mBest: "自动优选最快节点", quickMenu: "快捷节点", mQuit: "彻底退出"} {
					if lang == "en" {
						s = englishLabels[s]
					}
					m.SetTitle(s)
				}
			}
			n := min(len(cfg.Profiles), max(0, cfg.networkOptions().Quick), len(slots))
			for i := range slots {
				s := &slots[i]
				if i >= n {
					if s.profile.ServerIP != "" {
						s.item.Hide()
						s.profile = NodeProfile{}
					}
					continue
				}
				p := cfg.Profiles[i]
				if s.profile == p {
					continue
				}
				s.profile = p
				label := p.Name
				if label == "" {
					label = p.ServerIP
				}
				s.item.SetTitle(label)
				s.item.Show()
			}
		}
		refreshQuick()

		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			defer close(done)
			for {
				select {
				case <-ticker.C:
					refreshQuick()
				case i := <-clicked:
					if err := ctrl.switchQuick(slots[i].profile); err != nil {
						log.Print(err)
					}
				case <-mOpen.ClickedCh:
					showWindow()
				case <-mToggle.ClickedCh:
					ctrl.mu.Lock()
					running := ctrl.running
					ctrl.mu.Unlock()
					if running {
						if err := ctrl.StopTunnel(); err != nil {
							log.Print(err)
						}
					} else {
						if err := ctrl.StartTunnel(); err != nil {
							log.Printf("[GUI] 启动本地代理失败: %v", err)
							showWindow()
						}
					}
				case <-mMode.ClickedCh:
					if err := ctrl.toggleRouteMode(); err != nil {
						log.Print(err)
					}
				case <-mBest.ClickedCh:
					if res := ctrl.BenchAllProfiles(); len(res) > 0 && res[0].RTTMs >= 0 {
						_ = ctrl.SwitchProfile(res[0].Index)
					}
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}, onExit)
}
