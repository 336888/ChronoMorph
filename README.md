# ChronoMorph

基于 RTP 音视频拟态、30 秒动态端口跳频（UDP 20000–50000）与 KCP+FEC 的多平台安全隧道。

## 快速开始

1. **推送到 GitHub 自动编译**：在 Actions 的 Release ChronoMorph 页面手动指定版本，或推送标签 `git tag v1.0.0 && git push origin v1.0.0`，在 Releases 页面下载四端产物。
2. **Debian 服务端部署**：放行云厂商安全组 **入方向 UDP 20000–50000**，从 Release 下载与服务器架构对应的 `chronomorph-linux-amd64` 或 `chronomorph-linux-arm64`，重命名为 `chronomorph`，与 `cm.sh` 放入同一目录。进入该目录运行 `sudo bash cm.sh`，选择菜单 1 部署，后续输入 `cm` 唤出管理菜单。
3. **Windows 客户端**：双击 Release 中的 `ChronoMorph-windows-amd64.exe`，在控制面板 导入 `cm://` 链接即可。如需 TUN 虚拟网卡，将 `wintun.dll` 放至同目录并以管理员运行。
4. **Android 客户端**：提供 ARM64 命令行程序。在可执行目录准备好 `client.json` 后，运行 `chmod +x chronomorph_android_arm64 && ./chronomorph_android_arm64 client -c client.json`，默认监听 `127.0.0.1:1080`，接入 Mihomo/Clash 的 `socks5` 节点（并在规则顶部放行 VPS IP 直连）。
