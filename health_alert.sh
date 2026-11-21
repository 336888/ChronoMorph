#!/usr/bin/env bash
set -e
BOT_TOKEN="YOUR_TG_BOT_TOKEN"
CHAT_ID="YOUR_TG_CHAT_ID"
HOST_TAG=$(hostname)

notify() {
    [[ "$BOT_TOKEN" == "YOUR_TG_BOT_TOKEN" ]] && return
    curl -s -X POST "https://api.telegram.org/bot${BOT_TOKEN}/sendMessage" \
        -d "chat_id=${CHAT_ID}" -d "text=[Slacker | ${HOST_TAG}] $1" >/dev/null
}

if ! systemctl is-active --quiet chronomorph; then
    systemctl restart chronomorph
    notify "检测到服务异常退出，已自动拉起。"
fi
if ! nft list table inet chronomorph >/dev/null 2>&1; then
    nft -f /etc/nftables.d/chronomorph.nft
    notify "检测到 nftables 跳频规则丢失，已自动恢复。"
fi
