#!/usr/bin/env bash
set -eu
source "$(dirname "$0")/../cm.sh"
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
SCRIPT_DIR="$root/package"
BIN_PATH="$root/bin/chronomorph"
CFG_DIR="$root/etc/chronomorph"
mkdir -p "$SCRIPT_DIR" "$root/bin" "$root/etc" "$root/work" "$root/cwd"
name=$(release_binary_name)
printf 'fixture program\n' > "$SCRIPT_DIR/$name"
(cd "$SCRIPT_DIR" && sha256sum "$name" > SHA256SUMS.txt)
cd "$root/cwd"
stage_binary "$root/work" "$name"
cmp "$root/work/$name" "$SCRIPT_DIR/$name"
cp "$root/work/$name" "$CFG_DIR"
prepare_config_dir "$root/work/$name"
[[ -d "$CFG_DIR" ]]
cmp "$root/work/$name" "$root/etc"/chronomorph.backup.*/chronomorph
printf 'existing config' > "$CFG_DIR/server.json"
prepare_config_dir "$root/work/$name"
[[ $(cat "$CFG_DIR/server.json") == 'existing config' ]]
CFG_DIR="$root/etc/other"
echo unrelated > "$CFG_DIR"
if prepare_config_dir "$root/work/$name"; then exit 1; fi
[[ $(cat "$CFG_DIR") == unrelated ]]
echo tampered >> "$SCRIPT_DIR/$name"
if stage_binary "$root/work" "$name"; then exit 1; fi
echo 'PASS offline binary lookup, checksum rejection and config path handling'

CFG_DIR="$root/hybrid"
CFG_FILE="$CFG_DIR/server.json"
mkdir -p "$CFG_DIR"
cat > "$root/check-identity" <<'SH'
#!/bin/sh
test "$1" = tls-identity && test "$2" = -create && test "$3" = -dir && test -d "$4"
SH
chmod +x "$root/check-identity"
echo '{"listen":":40000","users":[{"name":"test","key":"fixture"}]}' > "$CFG_FILE"
prepare_dual_config "$root/check-identity" "$root/work"
jq -e '.tls_listen == ":40000" and .users[0].key == "fixture"' "$CFG_FILE" >/dev/null
jq -e 'has("tls_listen") | not' "$root/work/pre-dual.json" >/dev/null
echo '{"listen":":40000","tls_listen":":8443","users":[]}' > "$CFG_FILE"
cp "$CFG_FILE" "$root/expected"
prepare_dual_config "$root/check-identity" "$root/work"
cmp "$CFG_FILE" "$root/expected"
link=$(CM_PUBLIC_IP=127.0.0.1 gen_share_link "$(printf 'aa%.0s' {1..32})" test 8443 "$(printf 'bb%.0s' {1..32})" 40000)
LINK="$link" python3 - <<'PY'
import base64, json, os
line, = os.environ["LINK"].splitlines()
prefix, payload = line.split("cm+auto://")
node = json.loads(base64.urlsafe_b64decode(payload + "==="))
assert node["udp_port"] == 40000 and node["tls_port"] == 8443
assert node["tls_pin"] == "bb" * 32 and node["k"] == "aa" * 32
PY
echo 'PASS same-port migration, existing TLS port retention, single IPv4 dual link'
