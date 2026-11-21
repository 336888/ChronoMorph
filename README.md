# ChronoMorph

ChronoMorph 提供 TLS 1.3/TCP 和 UDP 两种传输。两者都支持本地 HTTP、SOCKS5 TCP 以及 SOCKS5 UDP 转发。

在 Windows「我的节点」中选中一行，点击「删除选中节点」。删除活动节点会断开连接，可在剩余节点中重新启动。删除只影响这台客户端。UDP 节点超时时，两端升级到 v1.1.2 后可在「连接日志」查看是 Debian 节点未响应，还是节点已响应但目标连接失败。

## 游戏与并发下载

v1.1.1 客户端将 SOCKS5 UDP 游戏和语音放在独立传输连接上。第一次发送 UDP 请求时才建立这条连接。同一节点上的 TCP 下载使用另一条连接。两条连接仍共享用户配额和实际链路带宽。现有 `cm://` 与 `cm+tls://` 节点可以继续使用。先升级 Debian 服务端，再退出并更新 Windows 客户端，可同时获得端口变化、快速重启和并发流控修复。

ChronoMorph 借鉴 Hysteria 2 区分可靠流与应用数据报的思路。当前 UDP 应用数据仍通过 KCP 或 TLS 的可靠传输转发，丢包时本条连接仍可能等待重传。协议细节见 [Hysteria 2 官方规范](https://v2.hysteria.network/docs/developers/Protocol/)。

## 双协议节点

1. 下载本版 Debian 离线包，解压后进入同名目录，运行 `sudo bash cm.sh`，选 `1` 升级。安装脚本为现有 UDP 监听地址启用同端口的 TLS/TCP；已配置的 TLS 端口会沿用。如果 TCP 端口被占用，选 `9` 设置其他空闲端口。
2. 按菜单实际显示的端口放行云安全组和主机防火墙。新装默认 TCP 40000、UDP 40000。旧 `cm://` 链接仍需原来的 UDP 转发端口范围。
3. 选 `4`，复制对应公网 IP 的 `cm+auto://` 链接。Windows 从托盘彻底退出旧程序，换新版 EXE，点「导入节点」粘贴链接。旧 UDP 链接缺少 TCP 端口和证书指纹，需要导入新链接才能自动切换。
4. 网页连接优先 TLS/TCP，SOCKS5 UDP 游戏或语音优先固定端口 UDP。首选通道在 250 毫秒内未完成认证时，客户端同时尝试另一条通道，并使用先认证成功的一条。应用数据只发往选中的通道。中断后新建连接可以换路，已中断的下载和游戏会话需由应用重连。「连接日志」显示两类流量各自选中的传输。
5. 两端的 TCP/UDP 端口可不同；都保存在同一条链接里。命令行客户端可配置 `server_ip`、`master_key`、`tls_port`、`tls_pin` 和 `udp_port`。已有 `cm+tls://` 固定使用 TLS/TCP。

## 快速开始

1. **推送到 GitHub 自动编译**：在 Actions 的 Release ChronoMorph 页面手动指定版本，或推送标签 `git tag v1.2.0 && git push origin v1.2.0`，在 Releases 页面下载四端产物。
2. **Debian 服务端部署**：按菜单放行双协议节点的 TCP 和 UDP 端口，从 Release 下载 `chronomorph-debian-amd64.tar.gz`（x86_64）或 `chronomorph-debian-arm64.tar.gz`（aarch64）。解压并进入同名目录，运行 `sudo bash cm.sh`，选 1 安装或升级。离线包含服务端程序，首次安装系统依赖仍使用 apt，后续输入 `cm` 唤出管理菜单。
3. **Windows 客户端**：双击 Release 中的 `ChronoMorph-windows-amd64.exe` 打开原生桌面窗口，在「导入节点」中粘贴分享链接。最小化或关闭窗口会收起到托盘，再次双击 EXE 或选择托盘「打开主窗口」即可恢复。退出请选择托盘「彻底退出」。如需 TUN 虚拟网卡，将 `wintun.dll` 放至同目录并以管理员运行。
4. **Android 客户端**：提供 ARM64 命令行程序。在可执行目录准备好 `client.json` 后，运行 `chmod +x chronomorph_android_arm64 && ./chronomorph_android_arm64 client -c client.json`，默认监听 `127.0.0.1:1080`，接入 Mihomo/Clash 的 `socks5` 节点（并在规则顶部放行 VPS IP 直连）。

Windows 客户端直接使用 Win32 桌面窗口。导入后启动本地代理，点击「测速」验证服务端应答。升级 Windows 客户端前，请先退出旧进程。每台客户端请使用独立的服务端用户密钥。
