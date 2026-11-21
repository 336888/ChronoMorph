# ChronoMorph

ChronoMorph 提供 TLS 1.3/TCP 和 UDP 两种传输。两者都支持本地 HTTP、SOCKS5 TCP 以及 SOCKS5 UDP 转发。

## 游戏与并发下载

v1.1.1 客户端将 SOCKS5 UDP 游戏和语音放在独立传输连接上。第一次发送 UDP 请求时才建立这条连接。同一节点上的 TCP 下载使用另一条连接。两条连接仍共享用户配额和实际链路带宽。现有 `cm://` 与 `cm+tls://` 节点可以继续使用。先升级 Debian 服务端，再退出并更新 Windows 客户端，可同时获得端口变化、快速重启和并发流控修复。

ChronoMorph 借鉴 Hysteria 2 区分可靠流与应用数据报的思路。当前 UDP 应用数据仍通过 KCP 或 TLS 的可靠传输转发，丢包时本条连接仍可能等待重传。协议细节见 [Hysteria 2 官方规范](https://v2.hysteria.network/docs/developers/Protocol/)。

## UDP 受限网络

1. 从本版本 Release 下载并解压对应架构的 Debian 安装包，进入目录，运行 `sudo bash cm.sh`，选 `1` 升级。
2. 在 `cm` 菜单选 `9`。默认监听 TCP 443。服务器已有 HTTPS 站点时选其他空闲 TCP 端口，在主机防火墙和云安全组放行所选端口。菜单会实际认证并检查服务端应答，再导出 `cm+tls://` 链接。若无法获取公网 IP，可运行 `sudo env CM_PUBLIC_IP=你的公网IP cm`，选 `4`。
3. 退出旧 Windows 客户端，启动 v1.1.0 或更新的 EXE，点「导入」粘贴 TLS 链接，启动本地代理，再点「测速」。面板显示所用的 TLS/TCP 端口。TLS 节点只连接指定 TCP 端口，并验证链接内的服务端证书指纹。旧版 UDP 节点仍使用 `cm://`。
4. 需要系统代理的应用可在面板启用「Windows 系统代理」。PAC 将公网请求交给本地代理，由代理在远端解析走隧道的域名。明确配置的直连规则仍可在本地解析。应用自行解析域名或自行建立连接时，其流量由应用自己的代理设置决定。当前 Windows TUN 只接管 IPv4。

分享链接含用户密钥，请通过可信渠道传递。服务端证书身份保存在配置目录的 `transport.pem`，备份时连同 `server.json` 一起保存。更换证书后请重新导入新链接。Android 命令行可在 `client.json` 填写链接对应的 `server_ip`、`master_key`、`tls_port` 和 `tls_pin`。

## 快速开始

1. **推送到 GitHub 自动编译**：在 Actions 的 Release ChronoMorph 页面手动指定版本，或推送标签 `git tag v1.1.1 && git push origin v1.1.1`，在 Releases 页面下载四端产物。
2. **Debian 服务端部署**：放行云厂商安全组 **入方向 UDP 20000–50000**，从 Release 下载 `chronomorph-debian-amd64.tar.gz`（x86_64）或 `chronomorph-debian-arm64.tar.gz`（aarch64）。解压并进入同名目录，运行 `sudo bash cm.sh`，选 1 安装或升级。离线包含服务端程序，首次安装系统依赖仍使用 apt，后续输入 `cm` 唤出管理菜单。
3. **Windows 客户端**：双击 Release 中的 `ChronoMorph-windows-amd64.exe` 打开原生桌面窗口，在「导入节点」中粘贴分享链接。最小化或关闭窗口会收起到托盘，再次双击 EXE 或选择托盘「打开主窗口」即可恢复。退出请选择托盘「彻底退出」。如需 TUN 虚拟网卡，将 `wintun.dll` 放至同目录并以管理员运行。
4. **Android 客户端**：提供 ARM64 命令行程序。在可执行目录准备好 `client.json` 后，运行 `chmod +x chronomorph_android_arm64 && ./chronomorph_android_arm64 client -c client.json`，默认监听 `127.0.0.1:1080`，接入 Mihomo/Clash 的 `socks5` 节点（并在规则顶部放行 VPS IP 直连）。

Windows 客户端直接使用 Win32 桌面窗口。导入后启动本地代理，点击「测速」验证服务端应答。升级 Windows 客户端前，请先退出旧进程。每台客户端请使用独立的服务端用户密钥。
