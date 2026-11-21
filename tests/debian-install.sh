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
