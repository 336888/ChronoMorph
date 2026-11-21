#!/usr/bin/env bash
# Unified BBR and kernel network tuning for Debian/Ubuntu.
set -Eeuo pipefail
export LC_ALL=C

CONF=/etc/sysctl.d/99-slacker-tune.conf
MODULES=/etc/modules-load.d/slacker-tune.conf
STATE=/var/lib/slacker-tune
LOCK=/run/lock/slacker-tune.lock
LEGACY_BBR=/etc/sysctl.d/99-slacker-bbr.conf
LEGACY_NET=/etc/sysctl.d/98-slacker-network.conf
MEMINFO=/proc/meminfo
MARKER='# Managed by slacker-tune.sh.'

KEYS=(
    net.core.default_qdisc net.ipv4.tcp_congestion_control
    net.core.rmem_max net.core.wmem_max
    net.ipv4.tcp_rmem net.ipv4.tcp_wmem net.ipv4.tcp_moderate_rcvbuf
    net.ipv4.tcp_mtu_probing net.core.somaxconn
    net.ipv4.tcp_max_syn_backlog net.ipv4.tcp_syncookies
)
# Keep these existing choices across migration; they are not general speed boosts.
LEGACY_KEEP=(
    net.core.netdev_max_backlog net.ipv4.tcp_fin_timeout
    net.ipv4.tcp_keepalive_time net.ipv4.tcp_keepalive_intvl
    net.ipv4.tcp_keepalive_probes net.ipv4.udp_rmem_min net.ipv4.udp_wmem_min
)

fail() { printf 'ERROR: %s\n' "$*" >&2; return 1; }
usage() { printf 'Usage: sudo bash %s {install|status|restore}\n' "${0##*/}"; }
require_root() { [[ $EUID -eq 0 ]] || fail 'install/restore require root.'; }

normalize() {
    local -a words=()
    read -r -a words <<< "$1"
    printf '%s\n' "${words[*]}"
}

read_value() {
    local value
    value=$(sysctl -n "$1") || return 1
    [[ -n $value && $value != *$'\n'* ]] || return 1
    normalize "$value"
}

known_value() {
    local key=$1 value=$2
    case "$key" in
        net.core.default_qdisc|net.ipv4.tcp_congestion_control)
            [[ $value =~ ^[a-zA-Z0-9_-]+$ ]] ;;
        net.ipv4.tcp_rmem|net.ipv4.tcp_wmem)
            [[ $value =~ ^[0-9]+\ [0-9]+\ [0-9]+$ ]] ;;
        *)
            [[ " ${KEYS[*]} ${LEGACY_KEEP[*]} " == *" $key "* && $value =~ ^[0-9]+$ ]] ;;
    esac
}

max_value() {
    local current=$1 floor=$2
    [[ $current =~ ^[0-9]+$ ]] || return 1
    if (( 10#$current > floor )); then printf '%s\n' "$current"; else printf '%s\n' "$floor"; fi
}

normalized_config() {
    awk -F= '
        /^[[:space:]]*(#|$)/ { next }
        NF != 2 { print "INVALID"; next }
        { gsub(/^[ \t]+|[ \t]+$/, "", $1); gsub(/^[ \t]+|[ \t]+$/, "", $2);
          gsub(/[ \t]+/, " ", $2); print $1 " = " $2 }
    ' "$1" | sort
}

legacy_template() {
    if [[ $1 == bbr ]]; then
        cat <<'EOF'
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
net.core.rmem_max = 33554432
net.core.wmem_max = 33554432
net.ipv4.tcp_rmem = 4096 131072 33554432
net.ipv4.tcp_wmem = 4096 131072 33554432
net.ipv4.tcp_mtu_probing = 1
EOF
    else
        cat <<'EOF'
net.core.somaxconn = 4096
net.core.netdev_max_backlog = 4096
net.ipv4.tcp_max_syn_backlog = 8192
net.ipv4.tcp_syncookies = 1
net.ipv4.tcp_fin_timeout = 30
net.ipv4.tcp_keepalive_time = 600
net.ipv4.tcp_keepalive_intvl = 30
net.ipv4.tcp_keepalive_probes = 5
net.ipv4.tcp_mtu_probing = 1
net.ipv4.udp_rmem_min = 16384
net.ipv4.udp_wmem_min = 16384
EOF
    fi
}

check_paths() {
    local path
    for path in "$CONF" "$MODULES" "$LEGACY_BBR" "$LEGACY_NET" "$LOCK"; do
        [[ ! -L $path && ( ! -e $path || -f $path ) ]] || {
            fail "Refusing a symlink or non-regular file: $path"; return 1;
        }
    done
    [[ ! -L $STATE && ( ! -e $STATE || -d $STATE ) ]] || fail "Invalid backup directory: $STATE"
}

check_legacy() {
    local path kind
    for kind in bbr network; do
        path=$LEGACY_BBR
        [[ $kind != network ]] || path=$LEGACY_NET
        [[ -e $path ]] || continue
        if ! cmp -s <(normalized_config "$path") <(legacy_template "$kind" | sort); then
            fail "Old config was customized; preserve/reconcile it before migration: $path"
            return 1
        fi
    done
}

prepare_kernel() {
    local module available
    MODULE_NAMES=()
    for module in tcp_bbr sch_fq; do
        if command -v modprobe >/dev/null && modprobe "$module" 2>/dev/null; then
            MODULE_NAMES+=("$module")
        fi
    done
    available=$(sysctl -n net.ipv4.tcp_available_congestion_control) || return 1
    [[ " $available " == *' bbr '* ]] || fail 'BBR is unavailable in this kernel; no sysctl/config changes made.'
}

build_plan() {
    local work=$1 key value target mem_kib minimum initial maximum
    local -A current=() desired=()
    local -a active=("${KEYS[@]}")
    if [[ -f $LEGACY_NET ]]; then active+=("${LEGACY_KEEP[@]}"); fi
    : > "$work/runtime.conf" || return 1
    for key in "${active[@]}"; do
        value=$(read_value "$key") || { fail "Cannot read $key; no tuning applied."; return 1; }
        known_value "$key" "$value" || { fail "Unexpected value for $key"; return 1; }
        current[$key]=$value
        desired[$key]=$value
        printf '%s = %s\n' "$key" "$value" >> "$work/runtime.conf" || return 1
    done

    # Conservative per-socket limits for small VPSes; keep larger existing limits.
    mem_kib=$(awk '/^MemTotal:/ { print $2; exit }' "$MEMINFO") || return 1
    [[ $mem_kib =~ ^[0-9]+$ ]] || { fail 'Cannot determine physical memory.'; return 1; }
    target=8388608
    if (( mem_kib >= 4194304 )); then target=33554432
    elif (( mem_kib >= 1048576 )); then target=16777216; fi
    desired[net.core.default_qdisc]=fq
    desired[net.ipv4.tcp_congestion_control]=bbr
    for key in net.core.rmem_max net.core.wmem_max; do
        desired[$key]=$(max_value "${current[$key]}" "$target") || return 1
    done
    for key in net.ipv4.tcp_rmem net.ipv4.tcp_wmem; do
        read -r minimum initial maximum <<< "${current[$key]}"
        maximum=$(max_value "$maximum" "$target") || return 1
        desired[$key]="$minimum $initial $maximum"
    done
    desired[net.ipv4.tcp_moderate_rcvbuf]=1
    desired[net.ipv4.tcp_mtu_probing]=$(max_value "${current[net.ipv4.tcp_mtu_probing]}" 1) || return 1
    desired[net.ipv4.tcp_syncookies]=$(max_value "${current[net.ipv4.tcp_syncookies]}" 1) || return 1
    desired[net.core.somaxconn]=$(max_value "${current[net.core.somaxconn]}" 4096) || return 1
    desired[net.ipv4.tcp_max_syn_backlog]=$(max_value "${current[net.ipv4.tcp_max_syn_backlog]}" 8192) || return 1
    {
        printf '%s\n' "$MARKER" '# BBR + fq + TCP buffers + listen queues + PMTU black-hole detection.'
        for key in "${KEYS[@]}"; do printf '%s = %s\n' "$key" "${desired[$key]}"; done
        if [[ -f $LEGACY_NET ]]; then
            printf '\n%s\n' '# Preserved legacy choices; keepalive/FIN/UDP minima are not throughput boosts.'
            for key in "${LEGACY_KEEP[@]}"; do printf '%s = %s\n' "$key" "${current[$key]}"; done
        fi
    } > "$work/desired.conf" || return 1
    {
        printf '%s\n' "$MARKER" '# Load supported modules before boot-time sysctl settings.'
        if (( ${#MODULE_NAMES[@]} )); then printf '%s\n' "${MODULE_NAMES[@]}"; fi
    } > "$work/modules.conf" || return 1
    chmod 644 "$work/desired.conf" "$work/modules.conf" || return 1
}

snapshot_files() {
    local work=$1 i
    local -a paths=("$CONF" "$MODULES" "$LEGACY_BBR" "$LEGACY_NET")
    local -a files=(format runtime.conf desired.conf modules.conf)
    mkdir "$work/files" || return 1
    for i in "${!paths[@]}"; do
        if [[ -f ${paths[i]} ]]; then
            cp -p -- "${paths[i]}" "$work/files/$i" || return 1
            files+=("files/$i")
        else
            : > "$work/files/$i.absent" || return 1
            files+=("files/$i.absent")
        fi
    done
    printf '1\n' > "$work/format" || return 1
    printf 'pending\n' > "$work/phase" || return 1
    (cd "$work" && sha256sum -- "${files[@]}" > checksums.sha256) || return 1
}

validate_state() {
    local i file checksums
    local -a files=(format runtime.conf desired.conf modules.conf)
    [[ -f $STATE/format && ! -L $STATE/format && $(cat "$STATE/format") == 1 ]] || { fail 'Backup format is missing or unsupported.'; return 1; }
    [[ -d $STATE/files && ! -L $STATE/files ]] || { fail 'Invalid file backup directory.'; return 1; }
    for file in runtime.conf desired.conf modules.conf checksums.sha256 phase; do
        [[ -f $STATE/$file && ! -L $STATE/$file && -s $STATE/$file ]] || { fail "Incomplete backup: $file"; return 1; }
    done
    for i in 0 1 2 3; do
        if [[ -f $STATE/files/$i && ! -L $STATE/files/$i && ! -e $STATE/files/$i.absent ]]; then files+=("files/$i"); continue; fi
        if [[ -f $STATE/files/$i.absent && ! -L $STATE/files/$i.absent && ! -e $STATE/files/$i ]]; then files+=("files/$i.absent"); continue; fi
        fail "Incomplete file backup: $i"; return 1
    done
    # Compare the complete fixed file list as well as its hashes. A truncated
    # manifest or runtime snapshot must never count as a successful restore.
    checksums=$(cd "$STATE" && sha256sum -- "${files[@]}") || return 1
    [[ $checksums == "$(cat "$STATE/checksums.sha256")" ]] || { fail 'Backup checksum mismatch; backup retained, no restore attempted.'; return 1; }
}

atomic_copy() {
    local source=$1 destination=$2 temp
    temp=$(mktemp "${destination}.XXXXXX") || return 1
    if cp -p -- "$source" "$temp" && mv -fT -- "$temp" "$destination"; then return 0; fi
    rm -f -- "$temp"
    return 1
}

# All keys and values are validated data. Backup files are never sourced as shell.
apply_values() {
    local file=$1 mode=${2:-write} key value current failed=0 count=0
    while IFS='=' read -r key value; do
        [[ -n $key && $key != \#* ]] || continue
        key=$(normalize "$key"); value=$(normalize "$value")
        if ! known_value "$key" "$value"; then
            fail "Invalid sysctl record in $file"; failed=1; continue
        fi
        count=$((count + 1))
        current=$(read_value "$key") || { failed=1; continue; }
        [[ $current != "$value" ]] || continue
        if [[ $mode == check ]]; then
            printf 'Mismatch: %s (current: %s, saved: %s)\n' "$key" "$current" "$value" >&2
            failed=1; continue
        fi
        if ! sysctl -w "$key=$value" >/dev/null; then failed=1; continue; fi
        current=$(read_value "$key") || { failed=1; continue; }
        if [[ $current != "$value" ]]; then fail "Read-back verification failed: $key"; failed=1; fi
    done < "$file"
    (( count > 0 && failed == 0 ))
}

restore_snapshot() {
    local i failed=0
    local -a paths=("$CONF" "$MODULES" "$LEGACY_BBR" "$LEGACY_NET")
    validate_state || return 1
    for i in "${!paths[@]}"; do
        if [[ -f $STATE/files/$i ]]; then
            atomic_copy "$STATE/files/$i" "${paths[i]}" || failed=1
        else
            rm -f -- "${paths[i]}" || failed=1
        fi
    done
    apply_values "$STATE/runtime.conf" || failed=1
    (( failed == 0 ))
}

set_phase() {
    printf '%s\n' "$1" > "$STATE/.phase" && mv -fT -- "$STATE/.phase" "$STATE/phase"
}

install_tuning() (
    local work='' transaction=0 rc
    on_exit() {
        rc=$?
        trap - EXIT HUP INT TERM
        if (( transaction )); then
            printf 'Install did not complete; restoring the saved state.\n' >&2
            if restore_snapshot; then
                rm -rf -- "$STATE" || true
                printf 'Previous sysctl values and configuration files restored.\n' >&2
            else
                printf 'Rollback incomplete. Backup kept at %s; fix the error and run restore.\n' "$STATE" >&2
            fi
            (( rc != 0 )) || rc=1
        fi
        if [[ -n $work ]]; then rm -rf -- "$work"; fi
        exit "$rc"
    }
    trap on_exit EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM

    if [[ -d $STATE ]]; then
        validate_state || exit 1
        [[ $(cat "$STATE/phase") == installed ]] || { fail 'An unfinished operation exists; run restore first.'; exit 1; }
        if cmp -s "$CONF" "$STATE/desired.conf" && cmp -s "$MODULES" "$STATE/modules.conf" &&
           [[ ! -e $LEGACY_BBR && ! -e $LEGACY_NET ]] && apply_values "$STATE/desired.conf" check; then
            printf 'Already installed. Original backup preserved.\n'; exit 0
        fi
        fail 'Managed settings have changed. Run restore before a fresh install; original backup preserved.'
        exit 1
    fi
    [[ ! -e $CONF && ! -e $MODULES ]] || { fail 'Unified config exists without its backup; reconcile it before installing.'; exit 1; }
    check_legacy || exit 1
    prepare_kernel || exit 1
    mkdir -p -- "$(dirname "$STATE")" "$(dirname "$CONF")" "$(dirname "$MODULES")" || exit 1
    work=$(mktemp -d "${STATE}.stage.XXXXXX") || exit 1
    build_plan "$work" || exit 1
    snapshot_files "$work" || exit 1
    mv -T -- "$work" "$STATE" || exit 1
    work=''
    transaction=1
    atomic_copy "$STATE/modules.conf" "$MODULES" || exit 1
    atomic_copy "$STATE/desired.conf" "$CONF" || exit 1
    apply_values "$STATE/desired.conf" || exit 1
    # Old files leave the active sysctl directory only after the new settings work.
    rm -f -- "$LEGACY_BBR" "$LEGACY_NET" || exit 1
    set_phase installed || exit 1
    transaction=0
    printf 'Installed: %s\nBackup: %s\n' "$CONF" "$STATE"
    printf 'Existing TCP/listening sockets and interface qdiscs may keep their previous settings.\n'
    show_status
)

restore_tuning() {
    [[ -d $STATE ]] || { fail 'No unified installation backup found; nothing changed.'; return 1; }
    validate_state || return 1
    set_phase restoring || return 1
    if ! restore_snapshot; then
        fail "Restore incomplete; backup retained at $STATE. Fix the reported problem and retry restore."
        return 1
    fi
    rm -rf -- "$STATE" || return 1
    printf 'Restored the state before the first unified installation (including any old configs).\n'
    printf 'Loaded modules are left in place; no network/service restart was performed.\n'
}

show_status() {
    local key
    printf 'Kernel: %s\n' "$(uname -r)"
    if [[ -r $STATE/phase ]]; then printf 'Managed state: %s\n' "$(cat "$STATE/phase")";
    else printf 'Managed state: absent or not readable (try sudo for backup state).\n'; fi
    for key in net.ipv4.tcp_available_congestion_control "${KEYS[@]}"; do
        sysctl "$key" 2>/dev/null || printf '%s: unavailable\n' "$key"
    done
    printf '\nActual interface qdiscs (default_qdisc does not replace existing queues):\n'
    if command -v tc >/dev/null; then tc qdisc show || true; else printf 'tc is not installed.\n'; fi
    printf 'BBR affects local TCP sending; it does not control ordinary UDP/KCP congestion.\n'
}

main() {
    local command=${1:-help} dependency
    (( $# <= 1 )) || { usage; return 2; }
    case "$command" in
        status) show_status; return ;;
        help|-h|--help) usage; return ;;
        install|restore) ;;
        *) usage; return 2 ;;
    esac
    require_root || return 1
    [[ $(uname -s) == Linux ]] || { fail 'This script requires Linux.'; return 1; }
    for dependency in sysctl flock awk sort cmp mktemp cp mv chmod mkdir rm cat sha256sum; do
        command -v "$dependency" >/dev/null || { fail "Required command is missing: $dependency"; return 1; }
    done
    umask 077
    check_paths || return 1
    mkdir -p -- "$(dirname "$LOCK")" || return 1
    exec 9>"$LOCK"
    flock -n 9 || { fail 'Another tuning operation is in progress.'; return 1; }
    if [[ $command == install ]]; then install_tuning; else restore_tuning; fi
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then main "$@"; fi
