#!/usr/bin/env bash
set -e

GITHUB_REPO="336888/ChronoMorph"
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)

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

release_binary_name() {
    case "$(uname -m)" in
        x86_64|amd64) echo chronomorph-linux-amd64 ;;
        aarch64|arm64) echo chronomorph-linux-arm64 ;;
        *) echo "不支持的架构 $(uname -m)" >&2; return 1 ;;
    esac
}

verify_binary() {
    local dir=$1 name=$2 sumfile=$3 line
    line=$(awk -v name="$name" 'length($1)==64 && $1 ~ /^[[:xdigit:]]+$/ && $2==name {print $1 "  " name; n++} END{if(n!=1)exit 1}' "$sumfile") || return 1
    (cd "$dir" && sha256sum -c - <<< "$line")
}

fetch_latest_from_github() {
    local work=$1 name=$2 api="https://api.github.com/repos/${GITHUB_REPO}/releases"
    local auth=() file id
    if [[ -n ${GITHUB_TOKEN:-} ]]; then auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}"); fi
    curl --proto '=https' --proto-redir '=https' -fsSL --retry 2 "${auth[@]}" "$api/latest" -o "$work/release.json" || return 1
    for file in "$name" SHA256SUMS.txt; do
        id=$(jq -er --arg f "$file" '.assets[] | select(.name == $f) | .id | numbers' "$work/release.json") || return 1
        [[ $id =~ ^[0-9]+$ ]] || return 1
        curl --proto '=https' --proto-redir '=https' -fsSL --retry 2 "${auth[@]}" -H 'Accept: application/octet-stream' "$api/assets/$id" -o "$work/$file" || return 1
    done
    verify_binary "$work" "$name" "$work/SHA256SUMS.txt"
}

stage_binary() {
    local work=$1 name=$2 dir file
    for file in "$name" chronomorph; do
        for dir in "$SCRIPT_DIR" "$PWD"; do
            [[ -f "$dir/$file" && ! "$dir/$file" -ef "$BIN_PATH" ]] || continue
            install -m 700 "$dir/$file" "$work/$name" || return 1
            if [[ -f "$dir/SHA256SUMS.txt" ]]; then
                verify_binary "$work" "$name" "$dir/SHA256SUMS.txt" || return 1
            fi
            return 0
        done
    done
    if [[ -f "$BIN_PATH" && -z ${GITHUB_TOKEN:-} ]]; then
        install -m 700 "$BIN_PATH" "$work/$name"
    else
        fetch_latest_from_github "$work" "$name" || {
            echo "无法下载或校验服务端。私有仓库可直接下载并解压本架构的 Debian 离线包。" >&2
            return 1
        }
        chmod 700 "$work/$name"
    fi
}

prepare_config_dir() {
    local candidate=$1 backup
    if [[ -d "$CFG_DIR" ]]; then return 0; fi
    if [[ -e "$CFG_DIR" || -L "$CFG_DIR" ]]; then
        if [[ ! -L "$CFG_DIR" && -f "$CFG_DIR" ]] &&
           { cmp -s "$CFG_DIR" "$candidate" || { [[ -f "$BIN_PATH" ]] && cmp -s "$CFG_DIR" "$BIN_PATH"; }; }; then
            backup=$(mktemp -d "${CFG_DIR}.backup.XXXXXXXX") || return 1
            mv -T -- "$CFG_DIR" "$backup/chronomorph" || return 1
            echo "已备份放错位置的二进制到 $backup/chronomorph"
        else
            echo "$CFG_DIR 已被其他文件占用，请先检查该路径。" >&2
            return 1
        fi
    fi
    install -d -m 700 "$CFG_DIR"
}

gen_share_link() {
    local key="$1" name="${2:-ChronoMorph}" tls_port=${3:-0} tls_pin=${4:-} udp_port=${5:-0} ip4 ip6
    if [[ -n ${CM_PUBLIC_IP:-} ]]; then
        ip4=$CM_PUBLIC_IP; ip6=$CM_PUBLIC_IP
    else
    ip4=$(curl -4 -fsS --max-time 4 https://ifconfig.me/ip 2>/dev/null || true)
    ip6=$(curl -6 -fsS --max-time 4 https://ifconfig.me/ip 2>/dev/null || true)
    fi
    local ip family count=0
    for family in 4 6; do
        if [[ $family == 4 ]]; then
            ip=$ip4
            [[ $ip =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || continue
        else
            ip=$ip6
            [[ $ip == *:* && $ip =~ ^[[:xdigit:]:.]+$ ]] || continue
        fi
        count=$((count+1))
        if [[ $tls_port != 0 ]]; then
            printf '[IPv%s TCP + UDP] cm+auto://%s\n' "$family" "$(jq -cn --arg s "$ip" --arg k "$key" --arg n "$name-v$family" --argjson tcp "$tls_port" --argjson udp "$udp_port" --arg pin "$tls_pin" '{s:$s,k:$k,n:$n,tls_port:$tcp,udp_port:$udp,tls_pin:$pin}' | base64 -w0 | tr '+/' '-_' | tr -d '=')"
        else
            printf '[IPv%s UDP] cm://%s\n' "$family" "$(jq -cn --arg s "$ip" --arg k "$key" --arg n "$name-v$family" '{s:$s,k:$k,n:$n}' | base64 -w0 | tr '+/' '-_' | tr -d '=')"
        fi

    done
    if ((count==0)); then echo "未获取到公网 IP。可运行 sudo env CM_PUBLIC_IP=你的公网IP cm，再选 4 导出链接。" >&2; fi
    return 0
}

# Reuse a configured TLS address. For UDP-only installations, bind TCP on
# the same address and port and keep the original file for rollback.
prepare_dual_config() {
    local candidate=$1 work=$2 addr
    addr=$(jq -er '.listen' "$CFG_FILE") || return 1
    "$candidate" tls-identity -create -dir "$CFG_DIR" >/dev/null || return 1
    if [[ -z $(jq -r '.tls_listen // ""' "$CFG_FILE") ]]; then
        cp -p "$CFG_FILE" "$work/pre-dual.json" || return 1
        (umask 077; jq --arg addr "$addr" '.tls_listen=$addr' "$CFG_FILE" > "$work/dual.json") || return 1
        install -m 600 "$work/dual.json" "$CFG_DIR/.dual-config.tmp" || return 1
        mv -fT "$CFG_DIR/.dual-config.tmp" "$CFG_FILE" || return 1
    fi
}

check_tcp_entry() {
    local attempt
    for attempt in 1 2 3 4 5; do
        if "$BIN_PATH" tls-check -c "$CFG_FILE"; then return 0; fi
        sleep 0.3
    done
    return 1
}

do_install() (
    set -e
    export DEBIAN_FRONTEND=noninteractive
    local name work mk listen version previous=0 was_active=0
    exec 9>/run/lock/chronomorph-install.lock
    flock -n 9 || { echo "已有安装任务在运行" >&2; exit 1; }
    name=$(release_binary_name)
    mkdir -p "$(dirname "$BIN_PATH")"
    work=$(mktemp -d "$(dirname "$BIN_PATH")/.chronomorph-install.XXXXXXXX")
    trap 'rm -rf -- "$work"' EXIT
    apt-get update -qq
    apt-get install -y -qq nftables chrony curl jq >/dev/null
    stage_binary "$work" "$name"
    version=$("$work/$name" version)
    [[ $version == "ChronoMorph v"* ]] || { echo "程序版本信息无效" >&2; exit 1; }
    echo "$version"
    prepare_config_dir "$work/$name"
    if [[ -L "$CFG_FILE" && ! -f "$CFG_FILE" ]]; then echo "配置符号链接无效" >&2; exit 1; fi
    if [[ ! -e "$CFG_FILE" ]]; then
        mk=$("$work/$name" keygen | awk '/^MasterKey:/ {print $2}')
        [[ $mk =~ ^[[:xdigit:]]{64}$ ]]
        (umask 077; jq -n --arg key "$mk" --arg listen ":${LISTEN_PORT}" '{listen:$listen, master_key:$key, users:[{name:"default",key:$key}]}' > "$work/server.json")
        install -m 600 "$work/server.json" "$CFG_FILE"
    fi
    listen=$(jq -er '.listen | capture(":(?<port>[0-9]+)$").port' "$CFG_FILE")
    [[ $listen =~ ^[0-9]{1,5}$ ]] && ((10#$listen >= 1 && 10#$listen <= 65535)) || { echo "服务端配置的监听端口无效" >&2; exit 1; }
    LISTEN_PORT=$((10#$listen))
    systemctl enable --now chrony
    cat > /etc/sysctl.d/99-chronomorph.conf <<EOF
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
net.netfilter.nf_conntrack_max = 131072
net.netfilter.nf_conntrack_udp_timeout = 15
net.netfilter.nf_conntrack_udp_timeout_stream = 45
EOF
    sysctl -e -p /etc/sysctl.d/99-chronomorph.conf >/dev/null || echo "部分可选内核参数未生效" >&2
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
    # Replace our own table in a single nft transaction.
    if nft list table inet chronomorph >/dev/null 2>&1; then
        echo 'delete table inet chronomorph' > "$work/apply.nft"
    fi
    cat "$NFT_FILE" >> "$work/apply.nft"
    if nft -f "$work/apply.nft"; then
        systemctl enable nftables
    else
        echo "旧 UDP 链接的转发规则未能加载。双协议链接使用固定 UDP 端口。" >&2
    fi
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=ChronoMorph RTP-Mimicry Hopping Server
After=network-online.target nftables.service chrony.service
Wants=network-online.target

[Service]
Type=exec
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
    systemctl daemon-reload
    systemctl enable chronomorph
    if [[ -f "$BIN_PATH" ]]; then cp -p "$BIN_PATH" "$work/previous"; previous=1; fi
    if systemctl is-active --quiet chronomorph; then was_active=1; fi
    prepare_dual_config "$work/$name" "$work"
    chmod 755 "$work/$name"
    mv -fT -- "$work/$name" "$BIN_PATH"
    if ! systemctl restart chronomorph || ! check_tcp_entry; then
        if [[ -f "$work/pre-dual.json" ]]; then
            install -m 600 "$work/pre-dual.json" "$CFG_DIR/.dual-config.tmp"
            mv -fT "$CFG_DIR/.dual-config.tmp" "$CFG_FILE"
            if systemctl restart chronomorph && systemctl is-active --quiet chronomorph; then
                echo "TCP $LISTEN_PORT 启用失败，已恢复 UDP 配置。请选 9 指定其他空闲 TCP 端口，再导入双协议链接。" >&2
                return 0
            fi
        fi
        if ((previous)); then
            mv -fT "$work/previous" "$BIN_PATH"
            if ((was_active)); then systemctl restart chronomorph; fi
            echo "启动失败，已恢复先前的服务端程序。" >&2
        else
            echo "服务启动失败，请查看 journalctl -u chronomorph。" >&2
        fi
        exit 1
    fi
    echo -e "${GREEN}部署完成！随时输入 cm 打开管理菜单。${PLAIN}"
    show_links
)

show_status() {
    systemctl status chronomorph --no-pager -l | head -n 15
    nft list table inet chronomorph 2>/dev/null || echo -e "${RED}未检测到跳频规则${PLAIN}"
    chronyc tracking | grep -E "System time|Leap status" || true
}

show_links() {
    if [[ ! -f "$CFG_FILE" ]]; then echo "尚未安装服务端"; return 0; fi
    local row name key listen port=0 udp_port pin=''
    udp_port=$(jq -er '.listen | capture(":(?<port>[0-9]+)$").port | tonumber' "$CFG_FILE")
    listen=$(jq -er '.tls_listen // ""' "$CFG_FILE")
    if [[ -n $listen ]]; then
        port=${listen##*:}
        pin=$("$BIN_PATH" tls-identity -dir "$CFG_DIR") || return 1
    fi
    if [[ $port != 0 ]]; then echo "双协议节点：请在云安全组和主机防火墙放行 TCP $port 与 UDP $udp_port。"; fi
    while IFS= read -r row; do
        name=$(jq -r '.name' <<< "$row")
        key=$(jq -r '.key' <<< "$row")
        printf '\n用户 %s\n' "$name"
        gen_share_link "$key" "$name" "$port" "$pin" "$udp_port"
    done < <(jq -c 'if (.users // [] | length)>0 then .users[] else {name:"default",key:.master_key} end' "$CFG_FILE")
}

enable_tls() (
    set -e
    [[ -f "$CFG_FILE" && -x "$BIN_PATH" ]] || { echo '请先选 1 安装或升级服务端'; return 1; }
    local current port work pin
    exec 9>/run/lock/chronomorph-install.lock
    flock -n 9 || { echo '已有部署任务在运行' >&2; return 1; }
    current=$(jq -r '.tls_listen // ""' "$CFG_FILE")
    current=${current##*:}
    read -rp "TLS/TCP 监听端口 [${current:-443}]: " port
    port=${port:-${current:-443}}
    [[ $port =~ ^[1-9][0-9]{0,4}$ ]] && ((port<=65535)) || { echo '端口无效'; return 1; }
    pin=$("$BIN_PATH" tls-identity -create -dir "$CFG_DIR")
    [[ $pin =~ ^[0-9a-f]{64}$ ]] || { echo '请先升级服务端程序'; return 1; }
    work=$(mktemp -d "$CFG_DIR/.tls-config.XXXXXXXX")
    trap 'rm -rf -- "$work"' EXIT
    cp -p "$CFG_FILE" "$work/original"
    (umask 077; jq --arg addr ":$port" '.tls_listen=$addr' "$CFG_FILE" > "$work/new")
    mv -fT "$work/new" "$CFG_FILE"
    if systemctl restart chronomorph; then
        for _ in 1 2 3; do
            if "$BIN_PATH" tls-check -c "$CFG_FILE"; then
                echo "请在云安全组和主机防火墙放行 TCP $port。客户端导入下面的 cm+auto:// 双协议链接。"
                show_links
                return 0
            fi
            sleep 1
        done
    fi
    mv -fT "$work/original" "$CFG_FILE"
    systemctl restart chronomorph
    echo 'TCP 入口未通过验证，已恢复原配置。请检查端口是否被占用。' >&2
    return 1
)

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
        echo "7. 打包备份配置    8. 彻底卸载    9. 设置双协议 TCP 端口    0. 退出"
        read -rp "请选择 [0-9]: " ch
        case "$ch" in
            1) do_install ;; 2) show_status ;; 3) journalctl -u chronomorph -f -n 50 || true ;;
            4) show_links ;; 5) show_traffic_stats ;; 6) systemctl reload-or-restart chronomorph ;;
            7) do_backup ;; 8) do_uninstall ;; 9) enable_tls ;; 0) exit 0 ;;
        esac
        read -rp "按回车返回..." _
    done
}
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main_menu; fi
