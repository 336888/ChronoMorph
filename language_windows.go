package main

import "golang.org/x/sys/windows"

var englishLabels = map[string]string{
	"打开主窗口": "Open window", "连接 / 断开隧道": "Connect / disconnect", "切换路由模式 (分流/全局)": "Switch routing mode", "自动优选最快节点": "Select fastest node", "快捷节点": "Quick profiles", "彻底退出": "Quit",
	"尚未配置节点": "No node configured", "停止质量测试": "Stop quality test", "请先启动连接": "Connect first", "请先选择节点": "Select a node first", "已切换节点": "Node switched", "请选择快捷节点": "Select a quick profile", "MasterKey · 64 位十六进制": "MasterKey · 64 hex characters",
	"粘贴一个节点链接，可直接复制 Debian 菜单 4 中的一整行": "Paste a share link from Debian menu 4", "可导入服务端分享链接，或手动填写 UDP 节点": "Import a share link or add a UDP node manually", "查看匹配的规则，处理误拦截": "Inspect matching rules and exceptions",
	"连接概览":    "Overview",
	"实时连接":    "Connections",
	"我的节点":    "Nodes",
	"链式代理":    "Proxy chain",
	"去广告":     "Ad blocking",
	"连接日志":    "Logs",
	"设置":      "Settings",
	"导入节点":    "Import node",
	"启动连接":    "Connect",
	"断开连接":    "Disconnect",
	"添加第一个节点": "Add first node",
	"本地代理已停止": "Proxy stopped",
	"本地代理运行中": "Proxy running",
	"本地代理已启动": "Proxy running",
	"准备连接":    "Ready to connect",
	"服务端可达":   "Server reachable",
	"发送速率":    "Upload",
	"接收速率":    "Download",
	"服务端延迟":   "Latency",
	"测速":      "Test",
	"系统代理":    "System proxy",
	"供使用 Windows 代理设置的应用连接": "For apps using the Windows proxy",
	"路由模式":        "Routing",
	"智能分流":        "Rule-based",
	"全局代理":        "Global",
	"选择智能分流或全局代理": "Use rules or proxy all connections",
	"已开启":         "Enabled",
	"未开启":         "Disabled",
	"开启":          "Enable",
	"本地 HTTP / SOCKS4 / SOCKS5 监听地址": "Local HTTP / SOCKS4 / SOCKS5 listener",
	"保存监听地址":                         "Save listener",
	"TUN 虚拟网卡":                       "TUN adapter",
	"接管 IPv4 与公网 IPv6。路由检查位于连接日志。": "IPv4 and public IPv6. Diagnostics are under Logs.",
	"登录 Windows 后启动":               "Launch at sign-in",
	"对当前 Windows 用户生效":             "Applies to the current Windows user",
	"仅 TLS 传输":                     "TLS transport only",
	"限制到节点的传输及自动切换。":               "Limit transports and failover to TLS.",
	"锁定备用类型":                       "Lock fallback type",
	"备用类型已锁定":                      "Fallback locked",
	"高级网络设置":                       "Advanced networking",
	"网络设置":                         "Network settings",
	"进程模式":                         "Processes",
	"WinTUN 设置":                    "WinTUN settings",
	"DNS 分流":                       "Split DNS",
	"常规":                           "General",
	"本地代理端口":                       "Local proxy port",
	"允许局域网设备接入":                    "Allow LAN devices",
	"快捷节点数":                        "Quick nodes",
	"延迟测试协议":                       "Latency protocol",
	"检测心跳（秒）":                      "Heartbeat (s)",
	"连接后测速（秒，-1 关闭）":               "Probe delay (s, -1 disables)",
	"STUN 服务器":                     "STUN server",
	"界面语言":                         "Language",
	"快捷切换节点":                       "Quick profiles",
	"检测 UDP 映射":                    "Test UDP mapping",
	"使用选中的快捷节点":                    "Use selected profile",
	"保存本页":                         "Save this page",
	"接管的进程 · 每行一个 EXE，留空接管全部": "Process names, one EXE per line. Empty matches all.",
	"处理 TCP":                    "Handle TCP",
	"处理 UDP":                    "Handle UDP",
	"包含子进程":                     "Include children",
	"本地回应 ICMP Echo":            "Local ICMP echo",
	"模拟延迟（毫秒）":                  "Echo delay (ms)",
	"接管 53 端口 DNS":              "Redirect DNS :53",
	"仅指定进程的 DNS":                "Selected apps only",
	"DNS 经过 CM 节点":              "DNS via CM",
	"虚拟网卡 IPv4":                 "Adapter IPv4",
	"子网掩码":                      "Subnet mask",
	"网关（空为链路直达）":                "Gateway (optional)",
	"TUN 全局直连 IP / CIDR · 每行一条": "Bypass IPs and CIDRs, one per line",
	"国内 DNS（可留空）":               "China DNS (optional)",
	"其他 DNS":                    "Other DNS",
	"国内域名后缀 · 每行一个，自动包含 .cn":            "China domain suffixes, one per line (.cn included)",
	"统一使用自定义 DNS":                       "Override both groups",
	"自定义 DNS 地址":                        "Custom DNS address",
	"CM 测完整隧道；TCP 和 ICMP 测物理入口。":        "CM tests the tunnel. TCP and ICMP test the entry.",
	"独立 DNS 可按进程识别。系统共享 DNS 无法还原原始进程。":  "Windows shared DNS does not identify the original app.",
	"直连 IP 经物理网卡出站。更改后重新开启 TUN。":        "Bypass uses the uplink. Restart TUN after changes.",
	"多个上游以逗号分隔，支持 IP、IP:端口或 tcp:// 地址。": "Separate servers with commas. IP, IP:port and tcp:// are supported.",
	"已保存；TUN 设置在下次开启时生效":                "Saved. TUN changes apply at next start.",
	"节点延迟测试":                            "Node latency",
	"路由与 DNS 检查":                        "Route and DNS diagnostics",
	"手动添加节点":                            "Add node manually",
	"域名检查与放行":                           "Domain exceptions",
	"使用选中节点":                            "Use selected node",
	"测速优选":                              "Find fastest",
	"复制当前链接":                            "Copy link",
	"删除选中节点":                            "Delete node",
	"手动添加 UDP":                          "Add UDP node",
	"重新测试":                              "Test again",
	"复制诊断报告":                            "Copy diagnostics",
	"检查路由与 DNS":                         "Check route and DNS",
	"60 秒线路质量测试":                        "60-second quality test",
	"重新检查":                              "Recheck",
	"保存链路":                              "Save chain",
	"添加 217heidai 订阅":                   "Add 217heidai feed",
	"保存并更新订阅":                           "Save and update",
	"暂停过滤":                              "Pause filtering",
	"恢复过滤":                              "Resume filtering",
	"检查":                                "Check",
	"放行此域名":                             "Allow domain",
	"保存放行名单":                            "Save exceptions",
	"导入并使用":                             "Import and use",
	"从剪贴板粘贴":                            "Paste",
	"返回":                                "Back",
	"保存节点":                              "Save node",
	"复制目标":                              "Copy target",
	"断开选中连接":                            "Disconnect",
	"筛选目标、进程或协议":                        "Filter destination, process or protocol",
	"前置 CM 节点":                          "CM entry node",
	"订阅地址 · 每行一个 HTTPS 链接":              "Feed URLs, one HTTPS link per line",
	"输入域名或粘贴网页地址":                       "Enter a domain or paste a URL",
	"我的放行名单 · 每行一个域名，包含子域名": "Exceptions, one domain per line, including subdomains",
	"节点往返延迟 · 按延迟排序，保留当前节点": "Nodes sorted by round-trip time",
	"检查当前路由、节点应答与 DNS 解析":   "Check system routes, node reachability and DNS",
	"服务端 IP":               "Server IP",
	"处理中…":                 "Working…",
	"暂无连接日志":               "No logs",
	"尚未添加订阅":               "No subscriptions",
	"IPv6 经隧道 · 点击切换":      "IPv6 via tunnel",
	"阻止 TUN 内 IPv6 · 点击切换": "Block TUN IPv6",
}

func (u *desktopUI) tr(s string) string {
	if u.language == "en" {
		if text, ok := englishLabels[s]; ok {
			return text
		}
	}
	return s
}
func uiLanguage(lang string) string {
	if lang == "system" {
		v, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage").Call()
		lang = "en"
		if v&0x3ff == 4 {
			lang = "zh"
		}
	}
	return lang
}
func (u *desktopUI) setLanguage(lang string) {
	lang = uiLanguage(lang)
	if u.language == lang {
		return
	}
	u.language = lang
	for _, c := range u.controls {
		if c.label != "" {
			setText(c.h, u.tr(c.label))
			uc("InvalidateRect", c.h, 0, 1)
		}
	}
}
