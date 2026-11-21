#!/usr/bin/env bash
set -Eeuo pipefail

CONF=/etc/sysctl.d/99-slacker-bbr.conf
BACKUP=/var/lib/slacker-bbr

if [[ $EUID -ne 0 ]]; then
  echo 'Run as root: sudo bash slacker-bbr.sh install|status|restore' >&2
  exit 1
fi

usage() { echo 'Usage: sudo bash slacker-bbr.sh {install|status|restore}'; }

keys=(
  net.ipv4.tcp_congestion_control
  net.core.default_qdisc
  net.core.rmem_max
  net.core.wmem_max
  net.ipv4.tcp_rmem
  net.ipv4.tcp_wmem
  net.ipv4.tcp_mtu_probing
)

status() {
  echo "Kernel: $(uname -r)"
  echo "Available congestion algorithms: $(sysctl -n net.ipv4.tcp_available_congestion_control 2>/dev/null || true)"
  echo "Current congestion algorithm: $(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || true)"
  echo "Default qdisc: $(sysctl -n net.core.default_qdisc 2>/dev/null || true)"
  echo 'Interface qdisc:'
  if command -v tc >/dev/null; then tc qdisc show; fi
}

restore_values() {
  local k f
  for k in "${keys[@]}"; do
    f="$BACKUP/${k//./_}.before"
    if [[ -s $f ]]; then
      sysctl -w "$k=$(cat "$f")" >/dev/null 2>&1 || true
    fi
  done
}

install() {
  if command -v modprobe >/dev/null; then modprobe tcp_bbr 2>/dev/null || true; fi
  if ! sysctl -n net.ipv4.tcp_available_congestion_control | grep -qw bbr; then
    echo 'ERROR: BBR unavailable in this kernel; no changes made.' >&2
    exit 2
  fi
  mkdir -p "$BACKUP"
  if [[ -f $CONF ]]; then
    cp -a "$CONF" "$BACKUP/previous.conf"
    rm -f "$BACKUP/no-previous-conf"
  else
    : > "$BACKUP/no-previous-conf"
    rm -f "$BACKUP/previous.conf"
  fi
  local k
  for k in "${keys[@]}"; do
    sysctl -n "$k" > "$BACKUP/${k//./_}.before" 2>/dev/null || true
  done

  cat > "$CONF" <<'CFG'
# Slacker TCP BBR tuning. Managed by slacker-bbr.sh.
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 33554432
net.core.wmem_max = 33554432
net.ipv4.tcp_rmem = 4096 131072 33554432
net.ipv4.tcp_wmem = 4096 131072 33554432
net.ipv4.tcp_mtu_probing = 1
CFG

  if ! sysctl -p "$CONF"; then
    echo 'Apply failed; restoring previous configuration.' >&2
    if [[ -f $BACKUP/previous.conf ]]; then
      cp -a "$BACKUP/previous.conf" "$CONF"
    else
      rm -f "$CONF"
    fi
    restore_values
    exit 3
  fi
  echo 'Applied. Existing TCP connections and interface qdiscs may retain previous settings.'
  status
}

restore() {
  if [[ ! -d $BACKUP || ( ! -f $BACKUP/previous.conf && ! -f $BACKUP/no-previous-conf ) ]]; then
    echo 'No previous installation backup found; refusing to change settings.' >&2
    exit 4
  fi
  if [[ -f $BACKUP/previous.conf ]]; then
    cp -a "$BACKUP/previous.conf" "$CONF"
  else
    rm -f "$CONF"
  fi
  restore_values
  echo 'Restored captured runtime values. Reboot or restart networking if interface qdiscs need resetting.'
  status
}

case "${1:-}" in
  install) install ;;
  status) status ;;
  restore) restore ;;
  *) usage; exit 2 ;;
esac
