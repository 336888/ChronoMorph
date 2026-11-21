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
printf 'fixture helper\n' > "$SCRIPT_DIR/cm-reality"
printf 'fixture menu\n' > "$SCRIPT_DIR/cm.sh"
(cd "$SCRIPT_DIR" && sha256sum "$name" cm-reality cm.sh > SHA256SUMS.txt)
cd "$root/cwd"
verify_package "$SCRIPT_DIR" "$name"
cp "$SCRIPT_DIR/$name" "$root/work/$name"
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
if verify_package "$SCRIPT_DIR" "$name"; then exit 1; fi
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

# Exercise release lookup and package integrity before any running file is touched.
remote="$root/remote"
dest="$root/download"
pkg="chronomorph-debian-${name##*-}"
mkdir -p "$remote/$pkg" "$dest"
cp "$SCRIPT_DIR/"{cm.sh,cm-reality} "$remote/$pkg/"
cp "$SCRIPT_DIR/$name" "$remote/$pkg/"
pack_fixture() {
    (cd "$remote/$pkg" && sha256sum cm.sh cm-reality "$name" > SHA256SUMS.txt)
    (cd "$remote" && tar -czf "$pkg.tar.gz" "$pkg" && sha256sum "$pkg.tar.gz" > SHA256SUMS.txt)
}
pack_fixture
jq -n --arg n "$pkg.tar.gz" '{assets:[{name:$n,id:81},{name:"SHA256SUMS.txt",id:82}]}' > "$remote/latest"
cp "$root/work/$name" "$BIN_PATH"
export GH_TOKEN=fixture-only
github_download() {
    [[ $4 == fixture-only && -z ${GH_TOKEN:-} && -z ${GITHUB_TOKEN:-} ]] || return 1
    case "$1" in
        */latest) cp "$remote/latest" "$2" ;;
        */assets/81) cp "$remote/$pkg.tar.gz" "$2" ;;
        */assets/82) cp "$remote/SHA256SUMS.txt" "$2" ;;
        *) return 1 ;;
    esac
}
fetch_release_package "$dest" "$name"
for f in cm.sh cm-reality "$name"; do cmp "$remote/$pkg/$f" "$dest/$pkg/$f"; done
echo 'PASS same-release archive, both binaries and matching menu retrieved with scoped credentials'
echo bad >> "$remote/$pkg.tar.gz"
mkdir "$root/bad"
if fetch_release_package "$root/bad" "$name"; then exit 1; fi
[[ ! -e "$root/bad/$pkg" ]]
pack_fixture
echo altered >> "$remote/$pkg/cm-reality"
(cd "$remote" && tar -czf "$pkg.tar.gz" "$pkg" && sha256sum "$pkg.tar.gz" > SHA256SUMS.txt)
mkdir "$root/bad-inner"
if fetch_release_package "$root/bad-inner" "$name"; then exit 1; fi
rm "$remote/$pkg/cm-reality"
(cd "$remote" && tar -czf "$pkg.tar.gz" "$pkg" && sha256sum "$pkg.tar.gz" > SHA256SUMS.txt)
mkdir "$root/missing"
if fetch_release_package "$root/missing" "$name"; then exit 1; fi
echo 'PASS corrupt outer archive, altered helper and incomplete package rejected'

# Isolate systemd while exercising the actual three-file activation and rollback.
MGR_PATH="$root/bin/cm"
helper="$root/bin/cm-reality"
echo 'old server' > "$BIN_PATH"
echo 'old helper' > "$helper"
echo 'old manager' > "$MGR_PATH"
echo 'existing private identity' > "$CFG_DIR/reality.key"
echo '{"master_key":"original","listen":":40000","tls_listen":":443","reality":{"only":true}}' > "$CFG_FILE"
cp "$CFG_FILE" "$root/before-config"
prepare_dual_config() { :; }
systemctl() { echo "$*" >> "$root/systemctl.log"; }
check_service_entries() { return 1; }
stage_fixture() {
    mkdir "$1"
    echo 'new server' > "$1/$name"
    echo 'new helper' > "$1/cm-reality"
    echo 'new manager' > "$1/cm.sh"
}
stage_fixture "$root/rollback"
if activate_package "$root/rollback" "$name"; then exit 1; fi
[[ $(cat "$BIN_PATH") == 'old server' && $(cat "$helper") == 'old helper' && $(cat "$MGR_PATH") == 'old manager' ]]
check_service_entries() { return 0; }
stage_fixture "$root/activate"
activate_package "$root/activate" "$name"
[[ $(cat "$BIN_PATH") == 'new server' && $(cat "$helper") == 'new helper' && $(cat "$MGR_PATH") == 'new manager' ]]
cmp "$root/before-config" "$CFG_FILE"
[[ $(cat "$CFG_DIR/reality.key") == 'existing private identity' ]]
echo 'PASS server, helper and menu upgrade or roll back together; saved node identity preserved'
