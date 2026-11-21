#!/usr/bin/env bash
set -Eeuo pipefail
CONF=/etc/sysctl.d/98-slacker-network.conf
STATE=/var/lib/slacker-network
KEYS=(
 net.core.somaxconn
 net.core.netdev_max_backlog
 net.ipv4.tcp_max_syn_backlog
 net.ipv4.tcp_syncookies
 net.ipv4.tcp_fin_timeout
 net.ipv4.tcp_keepalive_time
 net.ipv4.tcp_keepalive_intvl
 net.ipv4.tcp_keepalive_probes
 net.ipv4.tcp_mtu_probing
 net.ipv4.udp_rmem_min
 net.ipv4.udp_wmem_min
)
VALUES=(4096 4096 8192 1 30 600 30 5 1 16384 16384)
usage() { echo "Usage: sudo bash $0 {install|status|restore}"; }
[[ $EUID -eq 0 ]] || { echo "Root required" >&2; exit 1; }
status() {
  echo "Kernel: $(uname -r)"
  for key in "${KEYS[@]}"; do sysctl "$key" 2>/dev/null || true; done
  echo "Open file limit (current shell): $(ulimit -n)"
  echo "BBR: $(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null || echo unavailable)"
}
restore_runtime() {
  local i key file
  for i in "${!KEYS[@]}"; do
    key="${KEYS[i]}"
    file="$STATE/${key//./_}.before"
    [[ -s $file ]] && sysctl -w "$key=$(cat "$file")" >/dev/null 2>&1 || true
  done
}
install() {
  if [[ -e $STATE/installed ]]; then
    echo "Already installed; restore first to avoid overwriting the original backup." >&2
    exit 2
  fi
  local i key value tmp
  mkdir -p "$STATE"
  if [[ -e $CONF ]]; then cp -a "$CONF" "$STATE/original.conf"; else : > "$STATE/absent"; fi
  for i in "${!KEYS[@]}"; do
    key="${KEYS[i]}"
    sysctl -n "$key" > "$STATE/${key//./_}.before" 2>/dev/null || {
      echo "Kernel does not expose $key; aborting without changes" >&2
      rm -rf "$STATE"
      exit 3
    }
  done
  tmp=$(mktemp "${CONF}.XXXXXX")
  {
    echo "# Slacker conservative network tuning; independent of 99-slacker-bbr.conf"
    for i in "${!KEYS[@]}"; do printf '%s = %s\n' "${KEYS[i]}" "${VALUES[i]}"; done
  } > "$tmp"
  chmod 0644 "$tmp"
  mv "$tmp" "$CONF"
  if ! sysctl -p "$CONF"; then
    echo "Apply failed; restoring previous values" >&2
    if [[ -f $STATE/original.conf ]]; then cp -a "$STATE/original.conf" "$CONF"; else rm -f "$CONF"; fi
    restore_runtime
    rm -rf "$STATE"
    exit 4
  fi
  : > "$STATE/installed"
  echo "Installed; note that per-service file limits must be set in systemd separately."
  status
}
restore() {
  [[ -f $STATE/installed ]] || { echo "No managed installation found" >&2; exit 5; }
  if [[ -f $STATE/original.conf ]]; then cp -a "$STATE/original.conf" "$CONF"; else rm -f "$CONF"; fi
  restore_runtime
  rm -rf "$STATE"
  echo "Restored captured runtime settings; reboot to ensure persistent config ordering."
  status
}
case "${1:-}" in
 install) install ;;
 status) status ;;
 restore) restore ;;
 *) usage; exit 2 ;;
esac
