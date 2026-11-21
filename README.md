# ChronoMorph

ChronoMorph 提供 TLS 1.3/TCP 和 UDP 两种传输。两者都支持本地 HTTP、SOCKS5 TCP 以及 SOCKS5 UDP 转发。

## Debian 一键安装或升级

下载 Release 附件 [cm.sh](https://github.com/336888/ChronoMorph/releases/latest/download/cm.sh)，在文件所在目录执行：

```bash
sudo bash ./cm.sh --install
```

脚本识别 AMD64 或 ARM64，下载相应的完整包，校验归档和文件，再安装主程序、REALITY 组件和 `cm` 管理菜单。在线安装需要访问 GitHub。私有仓库首次下载脚本需登录 GitHub。在服务器上可按提示输入仅有本仓库 Contents 读取权限的 Token，输入隐藏。如果当前账号已登录 GitHub CLI，脚本也会使用它的凭据。非交互安装支持 `GH_TOKEN` 或 `GITHUB_TOKEN`，使用 sudo 时需让安装进程取得该环境变量。

完成本次更新后，后续直接运行 `sudo cm --install`，或在 `sudo cm` 菜单选 **1**。升级会短暂重启服务，并检查已启用的入口。脚本沿用已有用户和节点密钥；新版服务验证失败时恢复上个版本。离线安装可解压同版本的 Debian 完整包，从包内运行同一条命令，系统依赖仍通过 apt 安装。

## Debian 和 Windows 的免证书 REALITY 入口

使用现有 Debian VPS 即可，无需 AWS 账号、自有域名或手动上传证书。安装时自动生成服务端密钥。公网入口接收 TLS 1.3 连接，普通浏览器访问配置的服务器名称时，由指定 HTTPS 站点完成握手并返回网页。节点客户端使用 REALITY 公钥和短 ID，并在加密通道内验证原有用户凭据和服务端指纹。目标网站域名不会改变 VPS 的公网 IP 或网络归属，流量仍可能被识别。

1. 按上面的 Debian 一键安装或升级步骤安装主程序和配套组件。
2. 输入 `sudo cm`，选 **12 → 1**。填入从 VPS 能正常访问、证书覆盖该域名且支持 TLS 1.3 的目标站，例如可尝试 `aws.amazon.com`。填写节点的空闲 TCP 端口，默认 443；目标地址默认用所填域名的 443。主机防火墙与云安全组需要放行节点端口。该模式使用一次外部目标连接来建立新的传输会话，目标不可达时新连接会失败。
3. 选 **4** 获取 `cm+reality://` 链接。Windows 下载并完整解压 [chronomorph-windows-amd64.zip](https://github.com/336888/ChronoMorph/releases/latest/download/chronomorph-windows-amd64.zip)，将 `cm-reality.exe` 与 `ChronoMorph-windows-amd64.exe` 放在同一目录。彻底退出旧客户端，打开新版，在「导入节点」粘贴链接并测试。Windows v1.4.2 起也可直接粘贴菜单 4 中带有 `[IPv4 REALITY]` 或 `[IPv6 REALITY]` 的一整行，每次选择一个节点。链接包含用户凭据，请仅交给节点使用者。
4. 用 `sudo chronomorph reality -check` 在 Debian 本机验证普通 HTTPS 的目标站证书、节点身份和隧道应答。外部 Windows 客户端连通后，可以选 **12 → 2** 关闭旧入口；需要恢复并存选 **12 → 3**。菜单重启服务并验证，失败会恢复原配置。

外层握手使用 [XTLS/Xray-core v26.11.9](https://github.com/XTLS/Xray-core/tree/676eaf66f8f05d4d558ee7a74b4736623d3feeb9) 和其锁定的 [REALITY](https://github.com/XTLS/REALITY/tree/4bb46b2afd8b)。内部传输使用 ChronoMorph，节点链接仅适用于支持此入口的 ChronoMorph 客户端。SOCKS5 UDP 数据通过 TCP 承载，线路丢包时会受到 TCP 重传影响。

## 自有域名 HTTPS 网站入口

v1.3.0 的 `cm+https://` 节点使用你自己域名的有效证书。普通浏览器打开网站时，入口将网页请求送到指定的本机 HTTP 网站。浏览器可协商 HTTP/2，无需出示客户端证书。ChronoMorph 客户端通过系统信任链和域名验证证书，再用现有用户密钥对本次 TLS 连接签名，建立 HTTP/1.1 CONNECT 隧道。认证信息无法在另一条 TLS 连接上重用。网站入口沿用服务端用户、流量计数和配额。TCP 和应用 UDP 均走 HTTPS/TCP，丢包时可能等待重传。

1. 准备能直接指向节点的域名、与之匹配的完整证书链和私钥，以及一个本机可访问、准备公开的网站，例如 `http://127.0.0.1:8080`。网站证书的 TLS 连接必须由 ChronoMorph 直接终止。把后台管理页作为源站会将它公开。端口需要可独占，已有服务占用 443 时可先用另一个空闲 TCP 端口测试。
2. 升级 Debian 和 Windows 到 v1.3.0。运行 `sudo cm`，选 **11 → 1**，输入域名、端口、证书链和私钥的绝对路径，以及 HTTP 源站。选 **4** 导出 HTTPS 链接并在 Windows 导入。旧客户端继续使用原节点。域名、证书与源站由你维护；续签证书后重启服务加载新证书，域名不变时原 HTTPS 链接继续有效。
3. 在外部浏览器访问该域名与端口，确认看到自己的网页，并在新版客户端验证连接。然后可选 **11 → 2** 停用本服务旧 TCP 和 UDP 监听。处在并存模式时，同一 IP 上的旧入口仍可访问。需要恢复旧节点时选 **11 → 3**，关闭网站入口选 **11 → 4**。修改运行中的服务会短暂重启；在线验证失败时会恢复先前配置。
4. 在服务器运行 `sudo chronomorph website-check`，可分别检查网站首页和经过认证的隧道。云安全组需要放行网站 TCP 端口。

该模式使用 Go TLS 和你的真实网站。它与 VLESS/REALITY 不互通，域名、IP、握手和流量时序等仍可被观察。现有 `cm://`、`cm+tls://`、`cm+auto://` 的行为见下文。

在 Windows「我的节点」中选中一行，点击「删除选中节点」。删除活动节点会断开连接，可在剩余节点中重新启动。删除只影响这台客户端。UDP 节点超时时，两端升级到 v1.1.2 后可在「连接日志」查看是 Debian 节点未响应，还是节点已响应但目标连接失败。

v1.2.3 客户端支持在节点握手超时时直接断开或切换节点。网页通道等待节点应答期间，游戏和语音仍可独立建立 UDP 转发。此次升级可以沿用 v1.2.2 服务端及现有节点链接。

## 游戏与并发下载

v1.1.1 客户端将 SOCKS5 UDP 游戏和语音放在独立传输连接上。第一次发送 UDP 请求时才建立这条连接。同一节点上的 TCP 下载使用另一条连接。两条连接仍共享用户配额和实际链路带宽。现有 `cm://` 与 `cm+tls://` 节点可以继续使用。先升级 Debian 服务端，再退出并更新 Windows 客户端，可同时获得端口变化、快速重启和并发流控修复。

ChronoMorph 借鉴 Hysteria 2 区分可靠流与应用数据报的思路。当前 UDP 应用数据仍通过 KCP 或 TLS 的可靠传输转发，丢包时本条连接仍可能等待重传。协议细节见 [Hysteria 2 官方规范](https://v2.hysteria.network/docs/developers/Protocol/)。

## 仅 TLS 传输

Windows 设置页可打开「仅 TLS 传输」。现有 `cm+auto://` 和 `cm+tls://` 都可直接使用，旧 `cm://` 节点需从服务端菜单 4 重新导出双协议链接。命令行客户端可在 `client.json` 中设置 `"tls_only": true`。设置只保存在本机，默认关闭，分享链接保留原始端口与证书指纹。当前使用纯 UDP 节点时，请先导入双协议链接，再打开开关。

开启后网页、游戏和节点测速到节点的连接都只用 TLS/TCP。TLS 失败时返回错误，旧 UDP 节点不会参与测速；游戏的应用 UDP 数据仍能通过 SOCKS5 转发。在丢包线路上，游戏延迟可能因 TCP 重传而升高。切换传输会重建当前隧道，已开启 TUN 时请先关闭 TUN。

此开关只控制进入 ChronoMorph 后到节点的传输。目标站点仍能看到出口 IP，网络观察者仍能看到节点 IP、流量大小和时序。系统代理、全局/智能分流与 TUN 的覆盖范围由各自设置决定。

## Debian 服务端证书迁移

v1.2.5 新安装生成的自签名服务端证书以随机序列号作为主题和签发者。已经部署的节点会沿用现有证书和指纹。需要清除旧版 `CN=ChronoMorph` 时，先解压新版离线包并运行 `sudo bash cm.sh`，选 1 升级，再选 10 迁移证书。确认后将有一次短暂的服务重启。菜单会备份原证书和私钥，并在运行中的 TLS 服务验证失败时恢复它们。已停止的服务会在下次启动时使用新证书。

迁移成功后，选 4 导出链接，让每台使用 TLS 的客户端重新导入。原有用户密钥、配额和端口沿用，无需重装客户端。在服务端运行 `sudo chronomorph tls-identity -subject` 可检查磁盘上的证书主题，新格式为 `SERIALNUMBER=…`。这项改动移除证书中的项目名称，节点 IP、证书指纹和其他流量特征仍可观察。

## TLS 握手与升级

v1.2.6 服务端停止选择旧的项目专用 ALPN，客户端证书也使用每次签发的随机序列号。先升级 Debian，再更新 Windows。v1.2.2–v1.2.5 客户端连接新版服务端仍可以使用大窗口。新版客户端连接 v1.2.5 或更早服务端时会使用兼容模式，吞吐可能降低。两端升级后恢复大窗口，已有证书指纹和节点链接可沿用。若服务端仍使用更早的具名证书，请按上节迁移。

旧式跳频 UDP 客户端会在首包或换端口时发送一个 STUN 请求。v1.2.6 客户端直接发送已认证数据，服务端丢弃旧请求并继续接收后续数据。固定端口与跳频模式均可用。UDP 报文头、TLS 握手行为、流量大小和时序仍然可观察。

## 测量吞吐

Windows 的「测速」显示往返延迟。要复现吞吐对比，可在 Linux 对编译好的客户端和服务端二进制运行以下命令。脚本在本地启动服务端、客户端和测试目标，按方向传输 8 MiB 数据三次并校验内容，输出应用层 Mbps。用 `--transport udp` 测固定端口 UDP，或用 `--transport auto` 测双协议节点实际选中的路径。

```sh
python3 tests/throughput.py ./chronomorph-linux-amd64 --transport tcp --rtt 200 --mbps 100
```

## 双协议节点

1. 下载本版 Debian 离线包，解压后进入同名目录，运行 `sudo bash cm.sh`，选 `1` 升级。安装脚本为现有 UDP 监听地址启用同端口的 TLS/TCP；已配置的 TLS 端口会沿用。如果 TCP 端口被占用，选 `9` 设置其他空闲端口。
2. 按菜单实际显示的端口放行云安全组和主机防火墙。新装默认 TCP 40000、UDP 40000。旧 `cm://` 链接仍需原来的 UDP 转发端口范围。
3. 选 `4`，复制对应公网 IP 的 `cm+auto://` 链接。Windows 从托盘彻底退出旧程序，换新版 EXE，点「导入节点」粘贴链接。旧 UDP 链接缺少 TCP 端口和证书指纹，需要导入新链接才能自动切换。
4. 网页连接优先 TLS/TCP，SOCKS5 UDP 游戏或语音优先固定端口 UDP。首选通道在 250 毫秒内未完成认证时，客户端同时尝试另一条通道，并使用先认证成功的一条。应用数据只发往选中的通道。中断后新建连接可以换路，已中断的下载和游戏会话需由应用重连。「连接日志」显示两类流量各自选中的传输。
5. 两端的 TCP/UDP 端口可不同；都保存在同一条链接里。命令行客户端可配置 `server_ip`、`master_key`、`tls_port`、`tls_pin` 和 `udp_port`。已有 `cm+tls://` 固定使用 TLS/TCP。

## 快速开始

1. **推送到 GitHub 自动编译**：在 Actions 的 Release ChronoMorph 页面手动指定版本，或推送标签 `git tag v1.4.1 && git push origin v1.4.1`，在 Releases 页面下载四端产物。
2. **Debian 服务端部署**：按菜单放行双协议节点的 TCP 和 UDP 端口，从 Release 下载 `chronomorph-debian-amd64.tar.gz`（x86_64）或 `chronomorph-debian-arm64.tar.gz`（aarch64）。解压并进入同名目录，运行 `sudo bash cm.sh`，选 1 安装或升级。离线包含服务端程序，首次安装系统依赖仍使用 apt，后续输入 `cm` 唤出管理菜单。
3. **Windows 客户端**：下载并右键「全部解压」Windows 完整包，在解压目录中双击 `ChronoMorph-windows-amd64.exe` 打开原生桌面窗口。桌面快捷方式也应指向这个文件。请保留同目录的 `cm-reality.exe`，在「导入节点」中粘贴分享链接。最小化或关闭窗口会收起到托盘，再次双击 EXE 或选择托盘「打开主窗口」即可恢复。退出请选择托盘「彻底退出」。如需 TUN 虚拟网卡，将 `wintun.dll` 放至同目录并以管理员运行。
4. **Android 客户端**：提供 ARM64 命令行程序。在可执行目录准备好 `client.json` 后，运行 `chmod +x chronomorph_android_arm64 && ./chronomorph_android_arm64 client -c client.json`，默认监听 `127.0.0.1:1080`，接入 Mihomo/Clash 的 `socks5` 节点（并在规则顶部放行 VPS IP 直连）。

Windows 客户端直接使用 Win32 桌面窗口。导入后启动本地代理，点击「测速」验证服务端应答。升级 Windows 客户端前，请先退出旧进程。每台客户端请使用独立的服务端用户密钥。
