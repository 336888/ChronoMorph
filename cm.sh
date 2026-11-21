#!/usr/bin/env bash
set -e

GITHUB_REPO="336888/ChronoMorph"
GITHUB_TOKEN=""

BIN_PATH="/usr/local/bin/chronomorph"
MGR_PATH="/usr/local/bin/cm"
CFG_DIR="/etc/chronomorph"
CFG_FILE="${CFG_DIR}/server.json"
NFT_FILE="/etc/nftables.d/chronomorph.nft"
SERVICE_FILE="/etc/systemd/system/chronomorph.service"
LISTEN_PORT=40000

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; PLAIN='\033[0m'

check_root() {
    if [[ $EUID -ne 0 ]]; then
        echo -e "${RED}请使用 root 权限运行！${PLAIN}"
        exit 1
    fi
}
install_shortcut() {
    if [[ "$(realpath "$0")" != "$MGR_PATH" ]]; then
        install -m 755 "$0" "$MGR_PATH"
    fi
}

get_status_badge() {
    if systemctl is-active --quiet chronomorph 2>/dev/null; then echo -e "${GREEN}运行中${PLAIN}"
    elif [[ -f "$BIN_PATH" ]]; then echo -e "${YELLOW}已停止${PLAIN}"
    else echo -e "${RED}未安装${PLAIN}"; fi
}

fetch_latest_from_github() {
    local arch bin_name api_url auth_hdr=()
    case "$(uname -m)" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) echo -e "${RED}不支持的架构: $(uname -m)${PLAIN}"; return 1 ;;
    esac
    bin_name="chronomorph-linux-${arch}"
    api_url="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"
    [[ -n "$GITHUB_TOKEN" ]] && auth_hdr=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
    local rel_json bin_url sum_url tag
    rel_json=$(curl -fsSL "${auth_hdr[@]}" "$api_url") || return 1
    tag=$(echo "$rel_json" | jq -r '.tag_name')
    bin_url=$(echo "$rel_json" | jq -r --arg n "$bin_name" '.assets[] | select(.name == $n) | .browser_download_url')
    sum_url=$(echo "$rel_json" | jq -r '.assets[] | select(.name == "SHA256SUMS.txt") | .browser_download_url')
    [[ -z "$bin_url" || "$bin_url" == "null" ]] && return 1
    local workdir="/tmp/cm_release"
    rm -rf "$workdir" && mkdir -p "$workdir"
    curl -fsSL "${auth_hdr[@]}" "$bin_url" -o "${workdir}/${bin_name}"
    if [[ -n "$sum_url" && "$sum_url" != "null" ]]; then
        curl -fsSL "${auth_hdr[@]}" "$sum_url" -o "${workdir}/SHA256SUMS.txt"
        (cd "$workdir" && grep " ${bin_name}$" SHA256SUMS.txt | sha256sum -c -) || return 1
    fi
    chmod 755 "${workdir}/${bin_name}"
    mv -f "${workdir}/${bin_name}" /tmp/chronomorph_new
    echo -e "${GREEN}已下载并校验 ${tag}${PLAIN}"
}

gen_share_link() {
    local key="$1" name="${2:-ChronoMorph}" ip4 ip6
    ip4=$(curl -4 -s --max-time 4 https://ifconfig.me || true)
    ip6=$(curl -6 -s --max-time 4 https://ifconfig.me || true)
    [[ -n "$ip4" ]] && echo -e "${CYAN}[IPv4]${PLAIN} cm://$(printf '{"s":"%s","k":"%s","n":"%s-v4"}' "$ip4" "$key" "$name" | base64 -w0 | tr '+/' '-_' | tr -d '=')"
    [[ -n "$ip6" ]] && echo -e "${CYAN}[IPv6]${PLAIN} cm://$(printf '{"s":"%s","k":"%s","n":"%s-v6"}' "$ip6" "$key" "$name" | base64 -w0 | tr '+/' '-_' | tr -d '=')"
    return 0
}

do_install() {
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq && apt-get install -y -qq nftables chrony curl jq >/dev/null
    systemctl enable --now chrony
    if [[ ! -f "./chronomorph" && ! -f "$BIN_PATH" ]]; then
        fetch_latest_from_github || { echo -e "${RED}请先上传 chronomorph 二进制或配置 GITHUB_REPO${PLAIN}"; return 1; }
        mv -f /tmp/chronomorph_new ./chronomorph
    fi
    [[ -f "./chronomorph" ]] && install -m 755 ./chronomorph "$BIN_PATH"
    mkdir -p "$CFG_DIR"
    if [[ ! -f "$CFG_FILE" ]]; then
        local mk; mk=$($BIN_PATH keygen | awk '/MasterKey:/ {print $2}')
        cat > "$CFG_FILE" <<EOF
{"listen":":${LISTEN_PORT}","master_key":"${mk}","users":[{"name":"default","key":"${mk}"}]}
EOF
        chmod 600 "$CFG_FILE"
    fi
    cat > /etc/sysctl.d/99-chronomorph.conf <<EOF
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.netfilter.nf_conntrack_max = 131072
net.netfilter.nf_conntrack_udp_timeout = 15
net.netfilter.nf_conntrack_udp_timeout_stream = 45
EOF
    sysctl --system >/dev/null
    mkdir -p /etc/nftables.d
    cat > "$NFT_FILE" <<EOF
table inet chronomorph {
    chain prerouting {
        type nat hook prerouting priority dstnat; policy accept;
        udp dport 20000-50000 counter redirect to :${LISTEN_PORT}
    }
}
EOF
    grep -q 'include "/etc/nftables.d/\*.nft"' /etc/nftables.conf || echo 'include "/etc/nftables.d/*.nft"' >> /etc/nftables.conf
    nft delete table inet chronomorph 2>/dev/null || true
    nft -f "$NFT_FILE" && systemctl enable --now nftables
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=ChronoMorph RTP-Mimicry Hopping Server
After=network-online.target nftables.service chrony.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=${BIN_PATH} server -c ${CFG_FILE}
ExecReload=/bin/kill -HUP \$MAINPID
Restart=always
RestartSec=3
LimitNOFILE=65535
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/etc/chronomorph
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
MemoryMax=512M
TasksMax=4096

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload && systemctl enable --now chronomorph
    echo -e "${GREEN}部署完成！随时输入 cm 打开管理菜单。${PLAIN}"
    show_links
}

show_status() {
    systemctl status chronomorph --no-pager -l | head -n 15
    nft list table inet chronomorph 2>/dev/null || echo -e "${RED}未检测到跳频规则${PLAIN}"
    chronyc tracking | grep -E "System time|Leap status" || true
}

show_links() {
    [[ ! -f "$CFG_FILE" ]] && return
    jq -r '.users[] | "\(.name) \(.key)"' "$CFG_FILE" | while read -r n k; do
        echo -e "\n${GREEN}用户: ${n}${PLAIN}"; gen_share_link "$k" "$n"
    done
}

show_traffic_stats() {
    curl -s --max-time 2 http://127.0.0.1:40001/stats | jq . || echo -e "${RED}服务未运行${PLAIN}"
}

do_backup() {
    local out="/root/chronomorph_backup_$(date +%Y%m%d_%H%M%S).tar.gz"
    tar -czf "$out" -C /etc chronomorph
    echo -e "${GREEN}已备份至: ${out}${PLAIN}"
}

do_uninstall() {
    read -rp "确认彻底卸载？[y/N]: " c; [[ "$c" != "y" && "$c" != "Y" ]] && return
    systemctl disable --now chronomorph 2>/dev/null || true
    rm -f "$SERVICE_FILE" "$NFT_FILE" /etc/sysctl.d/99-chronomorph.conf
    systemctl daemon-reload
    nft delete table inet chronomorph 2>/dev/null || true
    rm -rf "$BIN_PATH" "$CFG_DIR" "$MGR_PATH"
    echo -e "${GREEN}已卸载干净。${PLAIN}"; exit 0
}

main_menu() {
    check_root; install_shortcut
    while true; do
        clear
        echo -e "${CYAN}=== ChronoMorph Debian 管理面板 [$(get_status_badge)] ===${PLAIN}"
        echo "1. 一键安装部署    2. 状态与跳频排障  3. 实时日志"
        echo "4. 导出分享链接    5. 实时流量看板    6. 热重载配置"
        echo "7. 打包备份配置    8. 彻底无痕卸载    0. 退出"
        read -rp "请选择 [0-8]: " ch
        case "$ch" in
            1) do_install ;; 2) show_status ;; 3) journalctl -u chronomorph -f -n 50 || true ;;
            4) show_links ;; 5) show_traffic_stats ;; 6) systemctl reload-or-restart chronomorph ;;
            7) do_backup ;; 8) do_uninstall ;; 0) exit 0 ;;
        esac
        read -rp "按回车返回..." _
    done
}
main_menu
