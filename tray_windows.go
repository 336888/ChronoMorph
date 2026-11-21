//go:build windows

package main

import (
	_ "embed"
	"log"

	"github.com/getlantern/systray"
)

//go:embed assets/tray.ico
var trayIcon []byte

func RunTray(ctrl *GUIController, showWindow, onExit func()) {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("ChronoMorph · 桌面客户端")

		mOpen := systray.AddMenuItem("打开主窗口", "显示 ChronoMorph 桌面窗口")
		systray.AddSeparator()
		mToggle := systray.AddMenuItem("连接 / 断开隧道", "快速启停加密隧道")
		mMode := systray.AddMenuItem("切换路由模式 (分流/全局)", "实时切换智能分流与全局代理")
		mBest := systray.AddMenuItem("自动优选最快节点", "并发测速并切换至延迟最低的 VPS")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("彻底退出", "关闭隧道并还原系统代理")

		go func() {
			for {
				select {
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
