# ChronoMorph

基于 RTP 音视频拟态、30 秒动态端口跳频（UDP 20000–50000）与 KCP+FEC 的多平台安全隧道。

## 快速开始

1. **推送到 GitHub 自动编译**：推送代码并打标签 `git tag v1.0.0 && git push origin v1.0.0`，在 Releases 页面下载四端产物。
2. **Debian 服务端部署**：放行云厂商安全组 **入方向 UDP 20000–50000**，运行 `sudo ./cm.sh` 一键部署，后续输入 `cm` 唤出管理菜单。
3. **Windows 客户端**：首次右键管理员运行 `setup_win.ps1`，双击 `ChronoMorph.exe` 导入 `cm://` 链接即可。如需 TUN 虚拟网卡，将 `wintun.dll` 放至同目录并以管理员运行。
4. **Android 客户端**：使用 `chronomorph_android_arm64` 监听 `127.0.0.1:1080`，接入 Mihomo/Clash 的 `socks5` 节点（并在规则顶部放行 VPS IP 直连）。
