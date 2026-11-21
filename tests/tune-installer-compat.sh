#!/usr/bin/env bash
set -eu

source "$(dirname "$0")/../cm.sh"
root=$(mktemp -d)
trap 'rm -rf -- "$root"' EXIT
conf="$root/99-chronomorph.conf"
tune="$root/99-slacker-tune.conf"

# Applying only the generated service file must preserve tuner-owned values.
declare -A runtime=(
    [net.core.default_qdisc]=fq
    [net.ipv4.tcp_congestion_control]=bbr
    [net.core.rmem_max]=33554432
    [net.core.wmem_max]=33554432
)
sysctl() {
    local key equals value
    if [[ $# == 2 && $1 == -w ]]; then
        key=${2%%=*}
        value=${2#*=}
        runtime[$key]=$value
        printf '%s\n' "$key" >> "$root/applied"
    elif [[ $# == 3 && $1 == -e && $2 == -p && $3 == "$conf" ]]; then
        while read -r key equals value; do
            [[ $equals == '=' ]] || return 1
            runtime[$key]=$value
            printf '%s\n' "$key" >> "$root/applied"
        done < "$3"
    else
        return 1
    fi
}
check_conntrack() {
    [[ ${runtime[net.netfilter.nf_conntrack_max]} == 131072 ]]
    [[ ${runtime[net.netfilter.nf_conntrack_udp_timeout]} == 15 ]]
    [[ ${runtime[net.netfilter.nf_conntrack_udp_timeout_stream]} == 45 ]]
}

: > "$root/applied"
configure_server_network "$conf" "$tune"
[[ $(wc -l < "$conf") == 7 ]]
[[ $(wc -l < "$root/applied") == 7 ]]
[[ ${runtime[net.core.rmem_max]} == 16777216 && ${runtime[net.core.wmem_max]} == 16777216 ]]
check_conntrack
echo 'PASS legacy service network settings retained without a unified tuner'

printf '%s\n' '# Managed by slacker-tune.sh.' 'net.core.rmem_max = 33554432' > "$tune"
cp "$tune" "$root/expected-tune"
runtime[net.core.rmem_max]=33554432
runtime[net.core.wmem_max]=33554432
runtime[net.core.default_qdisc]=fq_codel
runtime[net.ipv4.tcp_congestion_control]=cubic
runtime[net.netfilter.nf_conntrack_max]=1
runtime[net.netfilter.nf_conntrack_udp_timeout]=1
runtime[net.netfilter.nf_conntrack_udp_timeout_stream]=1
: > "$root/applied"
configure_server_network "$conf" "$tune"
[[ $(wc -l < "$conf") == 7 ]]
[[ $(wc -l < "$root/applied") == 3 ]]
[[ $(awk '$0 !~ /^net\.netfilter\./ {n++} END {print n+0}' "$root/applied") == 0 ]]
[[ ${runtime[net.core.rmem_max]} == 33554432 && ${runtime[net.core.wmem_max]} == 33554432 ]]
[[ ${runtime[net.core.default_qdisc]} == fq_codel && ${runtime[net.ipv4.tcp_congestion_control]} == cubic ]]
check_conntrack
cmp "$tune" "$root/expected-tune"
configure_server_network "$conf" "$tune"
[[ $(wc -l < "$conf") == 7 && ${runtime[net.core.rmem_max]} == 33554432 ]]
[[ $(wc -l < "$root/applied") == 6 ]]
echo 'PASS repeated service upgrades preserve unified tuner values and configuration'

printf '%s\n' '# Local configuration' '# Managed by slacker-tune.sh.' > "$tune"
: > "$root/applied"
configure_server_network "$conf" "$tune"
[[ $(wc -l < "$conf") == 7 && $(wc -l < "$root/applied") == 7 ]]
rm "$tune"
ln -s "$root/expected-tune" "$tune"
: > "$root/applied"
configure_server_network "$conf" "$tune"
[[ $(wc -l < "$conf") == 7 && $(wc -l < "$root/applied") == 7 ]]
echo 'PASS an unrecognized file or symlink does not claim unified tuner ownership'
