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
DEPLOY_LOCK=/run/lock/chronomorph-install.lock
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
    local key="$1" name="${2:-ChronoMorph}" tls_port=${3:-0} tls_pin=${4:-} udp_port=${5:-0} web_name=${6:-} web_port=${7:-0} only=${8:-false} reality_node=${9:-null} ip4 ip6
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
        if [[ $reality_node != null ]]; then
            printf '[IPv%s REALITY] cm+reality://%s\n' "$family" "$(jq -cn --argjson node "$reality_node" --arg s "$ip" --arg k "$key" --arg n "$name-REALITY-v$family" '$node + {s:$s,k:$k,n:$n}' | base64 -w0 | tr '+/' '-_' | tr -d '=')"
        fi
        if [[ -n $web_name ]]; then
            printf '[IPv%s HTTPS] cm+https://%s\n' "$family" "$(jq -cn --arg s "$ip" --arg k "$key" --arg n "$name-HTTPS-v$family" --arg web "$web_name" --argjson port "$web_port" '{s:$s,k:$k,n:$n,web_name:$web,tls_port:$port}' | base64 -w0 | tr '+/' '-_' | tr -d '=')"
        fi
        if [[ $only == true ]]; then continue; fi
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

check_service_entries() {
    if ! jq -e '.website.only or .reality.only' "$CFG_FILE" >/dev/null; then
        check_tcp_entry || return 1
    fi
    local mode attempt ok
    for mode in website reality; do
        jq -e --arg m "$mode" '.[$m] != null and (if $m=="website" then .reality.only else .website.only end | not)' "$CFG_FILE" >/dev/null || continue
        local cmd=(website-check)
        if [[ $mode == reality ]]; then cmd=(reality -check); fi
        ok=0
        for attempt in 1 2 3; do
            if "$BIN_PATH" "${cmd[@]}" -c "$CFG_FILE"; then ok=1; break; fi
            sleep 0.3
        done
        ((ok)) || return 1
    done
}

do_install() (
    set -e
    export DEBIAN_FRONTEND=noninteractive
    local name work mk listen version previous=0 was_active=0
    exec 9>"$DEPLOY_LOCK"
    flock -n 9 || { echo "已有安装任务在运行" >&2; exit 1; }
    name=$(release_binary_name)
    mkdir -p "$(dirname "$BIN_PATH")"
    work=$(mktemp -d "$(dirname "$BIN_PATH")/.chronomorph-install.XXXXXXXX")
    trap 'rm -rf -- "$work"' EXIT
    apt-get update -qq
    apt-get install -y -qq nftables chrony curl jq >/dev/null
    stage_binary "$work" "$name"
    local helper="$(dirname "$BIN_PATH")/cm-reality" has_helper=0
    if [[ -f "$SCRIPT_DIR/cm-reality" ]]; then
        install -m 755 "$SCRIPT_DIR/cm-reality" "$work/cm-reality"
        verify_binary "$work" cm-reality "$SCRIPT_DIR/SHA256SUMS.txt"
        "$work/cm-reality" version
        has_helper=1
    fi
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
    if ((has_helper)); then
        if [[ -f "$helper" ]]; then cp -p "$helper" "$work/previous-helper"; fi
        mv -fT -- "$work/cm-reality" "$helper"
    fi
    mv -fT -- "$work/$name" "$BIN_PATH"
    if ! systemctl restart chronomorph || ! check_service_entries; then
        if [[ -f "$work/pre-dual.json" ]]; then
            install -m 600 "$work/pre-dual.json" "$CFG_DIR/.dual-config.tmp"
            mv -fT "$CFG_DIR/.dual-config.tmp" "$CFG_FILE"
            if ! jq -e '(.reality != null) or (.website != null)' "$CFG_FILE" >/dev/null && systemctl restart chronomorph && systemctl is-active --quiet chronomorph; then
                echo "TCP $LISTEN_PORT 启用失败，已恢复 UDP 配置。请选 9 指定其他空闲 TCP 端口，再导入双协议链接。" >&2
                return 0
            fi
        fi
        if ((has_helper)); then
            if [[ -f "$work/previous-helper" ]]; then mv -fT "$work/previous-helper" "$helper"; else rm -f -- "$helper"; fi
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
    local row name key listen port=0 udp_port pin='' web_name web_port only reality_node=null
    web_name=$(jq -r '.website.name // empty' "$CFG_FILE")
    web_port=$(jq -r '(.website.listen // ":0") | split(":") | last' "$CFG_FILE")
    only=$(jq -r '.website.only or .reality.only' "$CFG_FILE")
    if jq -e '.reality.only' "$CFG_FILE" >/dev/null; then web_name=''; fi
    if jq -e '.reality != null and (.website.only | not)' "$CFG_FILE" >/dev/null; then
        reality_node=$("$BIN_PATH" reality -export -c "$CFG_FILE") || return 1
    fi
    udp_port=$(jq -er '.listen | capture(":(?<port>[0-9]+)$").port | tonumber' "$CFG_FILE")
    listen=$(jq -er '.tls_listen // ""' "$CFG_FILE")
    if [[ $only != true && -n $listen ]]; then
        port=${listen##*:}
        pin=$("$BIN_PATH" tls-identity -dir "$CFG_DIR") || return 1
    fi
    if [[ $port != 0 ]]; then echo "双协议节点：请在云安全组和主机防火墙放行 TCP $port 与 UDP $udp_port。"; fi
    while IFS= read -r row; do
        name=$(jq -r '.name' <<< "$row")
        key=$(jq -r '.key' <<< "$row")
        printf '\n用户 %s\n' "$name"
        gen_share_link "$key" "$name" "$port" "$pin" "$udp_port" "$web_name" "$web_port" "$only" "$reality_node"
    done < <(jq -c 'if (.users // [] | length)>0 then .users[] else {name:"default",key:.master_key} end' "$CFG_FILE")
}

enable_tls() (
    set -e
    [[ -f "$CFG_FILE" && -x "$BIN_PATH" ]] || { echo '请先选 1 安装或升级服务端'; return 1; }
    local current port work pin
    if jq -e '.website.only or .reality.only' "$CFG_FILE" >/dev/null; then echo '先到对应入口菜单恢复并存，再调整旧入口。'; return 1; fi
    exec 9>"$DEPLOY_LOCK"
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

migrate_tls_identity() (
    local current="$CFG_DIR/transport.pem" subject before after backup answer state active=0
    if [[ -f "$CFG_FILE" ]] && jq -e '.reality != null' "$CFG_FILE" >/dev/null; then echo "迁移会改变 REALITY 节点的内部指纹，请先关闭该入口，再迁移并重新导出链接。"; return 1; fi
    if [[ -f "$CFG_FILE" ]] && jq -e '.website.only == true'  "$CFG_FILE" >/dev/null; then echo "当前仅启用 HTTPS，网站使用你自己的证书。"; return 0; fi
    exec 9>"$DEPLOY_LOCK" || return 1
    flock -n 9 || { echo '已有部署任务在运行' >&2; return 1; }
    [[ -x "$BIN_PATH" && -f "$CFG_FILE" && -f "$current" && ! -L "$current" ]] || {
        echo '需要已安装的服务端及本地 TLS 证书。' >&2; return 1;
    }
    subject=$("$BIN_PATH" tls-identity -dir "$CFG_DIR" -subject) || {
        echo '无法读取证书，请先用 v1.2.5 或更新的离线包选 1 升级。' >&2; return 1;
    }
    if [[ $subject != 'CN=ChronoMorph' ]]; then
        echo "当前证书主题为 $subject，无需迁移旧版默认名称。"
        return 0
    fi
    [[ -n $(jq -er '.tls_listen // empty' "$CFG_FILE") ]] || {
        echo '请先选 9 设置 TLS 监听端口。' >&2; return 1;
    }
    before=$("$BIN_PATH" tls-identity -dir "$CFG_DIR") || return 1
    state=$(systemctl show --property=ActiveState --value chronomorph) || return 1
    case "$state" in
        active|reloading) active=1 ;;
        inactive|failed) ;;
        *) echo "当前服务状态为 $state，请等待状态稳定后重试。" >&2; return 1 ;;
    esac
    echo "当前 TLS 指纹：$before"
    echo '迁移会更换证书及指纹，运行中的服务将短暂重启，所有使用 TLS 的客户端都需要重新导入链接。'
    read -rp '现在迁移？[y/N] ' answer || return 1
    [[ $answer == y || $answer == Y ]] || return 0

    umask 077
    backup=$(mktemp -d "$CFG_DIR/tls-backup.XXXXXXXX") || return 1
    install -m 600 -- "$current" "$backup/original.pem" || return 1
    after=$("$BIN_PATH" tls-identity -create -dir "$backup") || return 1
    [[ $after =~ ^[0-9a-f]{64}$ && $after != "$before" ]] || return 1
    subject=$("$BIN_PATH" tls-identity -dir "$backup" -subject) || return 1
    [[ $subject =~ ^SERIALNUMBER=[0-9a-f]+$ ]] || { echo '候选证书的主题不符合预期，已保留当前证书。' >&2; return 1; }
    echo "旧证书与私钥已备份到 $backup/original.pem"

    # The saved pair and the replacement are on the same filesystem. Roll
    # back both disk and the running listener if verification is interrupted.
    (
        restore_on_failure() {
            local rc=$?
            trap - EXIT HUP INT TERM
            if ((rc != 0)) && [[ ! -e "$backup/transport.pem" ]]; then
                if ln -- "$backup/original.pem" "$backup/restore.pem" && mv -fT -- "$backup/restore.pem" "$current"; then
                    if ((active == 0)) || { systemctl restart chronomorph && check_tcp_entry; }; then
                        echo '迁移未完成，旧证书已恢复。' >&2
                    else
                        echo '旧证书已恢复到磁盘，请检查 chronomorph 服务状态。' >&2
                    fi
                else
                    echo "自动恢复失败，旧证书仍在 $backup/original.pem" >&2
                fi
            fi
            exit "$rc"
        }
        trap restore_on_failure EXIT
        trap 'exit 1' HUP INT TERM
        mv -fT -- "$backup/transport.pem" "$current" || exit 1
        if ((active)); then
            systemctl restart chronomorph && check_tcp_entry || exit 1
        fi
    ) || return 1
    echo "新 TLS 指纹：$after"
    if ((active == 0)); then echo '服务当前已停止，新证书将在下次启动时使用。'; fi
    echo '请选择 4 导出新链接，在各客户端重新导入。'
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
    rm -rf "$BIN_PATH" "$(dirname "$BIN_PATH")/cm-reality" "$CFG_DIR" "$MGR_PATH"
    echo -e "${GREEN}已卸载干净。${PLAIN}"; exit 0
}

configure_website() { configure_entry website; }
configure_entry() (
    local mode=$1 work action name port cert key origin target answer active=0 changed=0
    local other=reality
    if [[ $mode == reality ]]; then other=website; fi
    [[ -f "$CFG_FILE" ]] || { echo "请先选 1 安装或升级"; return 1; }
    exec 9>"$DEPLOY_LOCK" || return 1
    flock -n 9 || { echo "已有部署任务在运行" >&2; return 1; }
    umask 077
    work=$(mktemp -d "$CFG_DIR/.website.XXXXXXXX") || return 1
    finish_website() {
        local rc=$?
        trap - EXIT HUP INT TERM
        if ((rc != 0 && changed)); then
            if mv -fT -- "$work/original" "$CFG_FILE"; then
                if ((active)); then systemctl restart chronomorph || echo "请检查服务状态" >&2; fi
                echo "入口配置验证失败，已恢复先前配置。" >&2
            else
                echo "无法自动恢复，请从 $work/original 恢复配置。" >&2
                exit 1
            fi
        fi
        rm -rf -- "$work"
        exit "$rc"
    }
    trap finish_website EXIT
    trap 'exit 1' HUP INT TERM
    cp -p "$CFG_FILE" "$work/original" || return 1
    echo "1 配置 $mode 入口并保留现有连接方式    2 仅启用 $mode    3 恢复并存    4 关闭此入口    0 返回"
    read -rp "请选择: " action || return 1
    case $action in
        1)
            if jq -e --arg other "$other" '.[$other].only' "$CFG_FILE" >/dev/null; then
                echo "请先在 $other 菜单中恢复并存"; return 1
            fi
            if [[ $mode == reality ]]; then
                [[ -x "$(dirname "$BIN_PATH")/cm-reality" ]] || { echo "请解压本版本 Debian 完整包，从包内运行 cm.sh 并选 1"; return 1; }
                read -rp "目标 HTTPS 网站域名（例如 aws.amazon.com）: " name || return 1
            else
                read -rp "你控制的 HTTPS 域名: " name || return 1
            fi
            read -rp "此程序独占的 TCP 端口 [443]: " port || return 1
            port=${port:-443}
            [[ $port =~ ^[1-9][0-9]{0,4}$ ]] && ((port <= 65535)) || { echo "端口无效"; return 1; }
            if [[ $mode == reality ]]; then
                read -rp "目标 TLS 地址 [$name:443]: " target || return 1
                jq --arg name "${name,,}" --arg listen ":$port" --arg target "${target:-$name:443}" '.reality={name:$name,listen:$listen,target:$target,only:false}' "$CFG_FILE" > "$work/new" || return 1
            else
            read -rp "网站证书链 PEM 绝对路径: " cert || return 1
            read -rp "网站私钥 PEM 绝对路径: " key || return 1
            [[ $cert == /* && $key == /* ]] || { echo "请输入绝对路径"; return 1; }
            read -rp "本机对外发布的网站源站 [http://127.0.0.1:8080]: " origin || return 1
            jq --arg name "${name,,}" --arg listen ":$port" --arg cert "$cert" --arg key "$key" --arg origin "${origin:-http://127.0.0.1:8080}"               '.website={name:$name,listen:$listen,cert_file:$cert,key_file:$key,origin:$origin,only:false}' "$CFG_FILE" > "$work/new" || return 1
            fi ;;
        2|3)
            jq -e --arg mode "$mode" '.[$mode] != null' "$CFG_FILE" >/dev/null || { echo "请先配置入口"; return 1; }
            if [[ $action == 2 ]]; then
                read -rp "将停用本服务的其他入口。已验证新链接，继续？[y/N] " answer || return 1
                [[ $answer == y || $answer == Y ]] || return 0
            fi
            jq --arg a "$action" --arg mode "$mode" --arg other "$other" '.[$mode].only=($a=="2") | if .[$other]!=null and $a=="2" then .[$other].only=false else . end'  "$CFG_FILE" > "$work/new" || return 1 ;;
        4) jq --arg mode "$mode" 'del(.[$mode])'  "$CFG_FILE" > "$work/new" || return 1 ;;
        0) return 0 ;;
        *) echo "选项无效"; return 1 ;;
    esac
    if [[ $mode == website ]] && jq -e '.website != null' "$work/new" >/dev/null; then
        "$BIN_PATH" website-check -config-only -c "$work/new" || return 1
    fi
    if systemctl is-active --quiet chronomorph; then active=1; fi
    changed=1
    mv -fT "$work/new" "$CFG_FILE" || return 1
    if [[ $mode == reality ]] && jq -e '.reality != null' "$CFG_FILE" >/dev/null; then
        "$BIN_PATH" reality -c "$CFG_FILE" -create || return 1
    fi
    if ((active)); then
        systemctl restart chronomorph && check_service_entries || return 1
        echo "配置已启用，可选 4 导出链接。"
    else
        echo "服务当前已停止。启动后可选 4 导出链接。"
    fi
)
main_menu() {
    check_root; install_shortcut
    while true; do
        clear
        echo -e "${CYAN}=== ChronoMorph Debian 管理面板 [$(get_status_badge)] ===${PLAIN}"
        echo "1. 一键安装部署    2. 状态与跳频排障  3. 实时日志"
        echo "4. 导出分享链接    5. 实时流量看板    6. 热重载配置"
        echo "7. 打包备份配置    8. 彻底卸载    9. 设置双协议 TCP 端口"
        echo "10. 迁移旧版 TLS 证书    11. 自有域名 HTTPS 网站入口    12. REALITY 免证书入口    0. 退出"
        read -rp "请选择 [0-12]: " ch
        case "$ch" in
            1) do_install ;; 2) show_status ;; 3) journalctl -u chronomorph -f -n 50 || true ;;
            4) show_links ;; 5) show_traffic_stats ;; 6) systemctl reload-or-restart chronomorph ;;
            7) do_backup ;; 8) do_uninstall ;; 9) enable_tls ;; 10) migrate_tls_identity || echo "证书迁移未完成" >&2 ;; 12) configure_entry reality || echo "REALITY 操作未完成" >&2 ;; 11) configure_website || echo "网站操作未完成" >&2 ;; 0) exit 0 ;;
        esac
        read -rp "按回车返回..." _
    done
}
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main_menu; fi
