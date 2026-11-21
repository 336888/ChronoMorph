# Changelog

## [v1.0.0] - 2026-11-21
- 核心协议：RTP 拟态封装、30 秒动态端口跳频、KCP+FEC(10:3)+SMUX、64 位滑动窗口防重放、STUN 前导、MTU 探测、Full-Cone UDP。
- Debian 服务端：nftables 双栈跳频、chrony 校时、nf_conntrack 防爆表、多用户 SIGHUP 热重载、月度流量限额与交互式 `cm.sh` 菜单。
- Windows & Android 客户端：无黑框深色 GUI、系统托盘、PAC/Wintun TUN、防污染 DNS、休眠自愈、多节点测速优选与 Android ARM64 核心。
