# ChronoMorph

基于 RTP 音视频拟态、30 秒动态端口跳频（UDP 20000–50000）与 KCP+FEC 的多平台安全隧道。

## 快速开始

1. **推送到 GitHub 自动编译**：在 Actions 的 Release ChronoMorph 页面手动指定版本，或推送标签 `git tag v1.0.2 && git push origin v1.0.2`，在 Releases 页面下载四端产物。
2. **Debian 服务端部署**：放行云厂商安全组 **入方向 UDP 20000–50000**，从 Release 下载 `chronomorph-debian-amd64.tar.gz`（x86_64）或 `chronomorph-debian-arm64.tar.gz`（aarch64）。解压并进入同名目录，运行 `sudo bash cm.sh`，选 1 安装或升级。离线包含服务端程序，首次安装系统依赖仍使用 apt，后续输入 `cm` 唤出管理菜单。
3. **Windows 客户端**：双击 Release 中的 `ChronoMorph-windows-amd64.exe`，在控制面板导入 `cm://` 链接即可。如需 TUN 虚拟网卡，将 `wintun.dll` 放至同目录并以管理员运行。
4. **Android 客户端**：提供 ARM64 命令行程序。在可执行目录准备好 `client.json` 后，运行 `chmod +x chronomorph_android_arm64 && ./chronomorph_android_arm64 client -c client.json`，默认监听 `127.0.0.1:1080`，接入 Mihomo/Clash 的 `socks5` 节点（并在规则顶部放行 VPS IP 直连）。

Windows 控制面板位于 `http://127.0.0.1:18080`。导入后启动本地代理，点击「测速」验证服务端应答。升级 Windows 客户端前，请先退出旧进程。每台客户端请使用独立的服务端用户密钥。
