//go:build windows

package main

import (
	_ "embed"
	"log"
	"os/exec"
	"syscall"

	"github.com/getlantern/systray"
)

//go:embed assets/tray.ico
var trayIcon []byte

func openBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}

func RunTray(ctrl *GUIController, onExit func()) {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("ChronoMorph RTP-Mimicry Tunnel")

		mOpen := systray.AddMenuItem("打开控制面板", "打开 ChronoMorph Web 面板")
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
					openBrowser("http://127.0.0.1:18080")
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
							openBrowser("http://127.0.0.1:18080")
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
