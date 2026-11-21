#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/../cm.sh"
BIN_PATH=$(realpath "$1")
root=$(mktemp -d)
trap 'rm -rf -- "$root"' EXIT
umask 077
CFG_DIR="$root/current"
CFG_FILE="$CFG_DIR/server.json"
DEPLOY_LOCK="$root/deploy.lock"
mkdir "$CFG_DIR" "$root/old"
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
jq -n --arg addr "127.0.0.1:$port" --arg key "$(printf 'aa%.0s' {1..32})" \
    '{listen:"127.0.0.1:0",tls_listen:$addr,master_key:$key}' > "$CFG_FILE"
cp "$CFG_FILE" "$root/old/"
openssl req -new -x509 -newkey ed25519 -nodes -subj /CN=ChronoMorph -days 1 \
    -addext extendedKeyUsage=serverAuth -keyout "$root/key.pem" -out "$root/cert.pem" 2>/dev/null
cat "$root/cert.pem" "$root/key.pem" > "$CFG_DIR/transport.pem"
cp "$CFG_DIR/transport.pem" "$root/old/"
pin() { "$BIN_PATH" tls-identity -dir "$CFG_DIR"; }
original=$(pin)
[[ $("$BIN_PATH" tls-identity -dir "$CFG_DIR" -create) == "$original" ]]
# Simulate the service manager only. The wire check below runs the release binary.
systemctl() {
    case "$1" in
        show) if [[ -f "$root/active" ]]; then echo active; else echo inactive; fi ;;
        restart) echo restart >> "$root/restarts" ;;
        *) return 1 ;;
    esac
}
check_tcp_entry() {
    [[ ! -f "$root/reject-new" || $(pin) == "$original" ]]
}
touch "$root/active"
migrate_tls_identity <<< n
[[ $(pin) == "$original" && ! -e "$root/restarts" ]]
touch "$root/reject-new"
if migrate_tls_identity <<< y; then echo 'accepted failed verification' >&2; exit 1; fi
cmp "$CFG_DIR/transport.pem" "$root/old/transport.pem"
[[ $(wc -l < "$root/restarts") -eq 2 ]]
rm "$root/reject-new"
migrate_tls_identity <<< y
updated=$(pin)
[[ $updated != "$original" && $(wc -l < "$root/restarts") -eq 3 ]]
for backup in "$CFG_DIR"/tls-backup.*/original.pem; do
    [[ $(stat -c %a "$backup") == 600 ]]
    cmp "$backup" "$root/old/transport.pem"
done
cmp "$CFG_FILE" "$root/old/server.json"
migrate_tls_identity </dev/null
[[ $(pin) == "$updated" && $(wc -l < "$root/restarts") -eq 3 ]]
cp "$root/old/transport.pem" "$CFG_DIR/transport.pem"
rm "$root/active"
migrate_tls_identity <<< Y
[[ $(pin) != "$original" && $(wc -l < "$root/restarts") -eq 3 ]]
echo 'PASS cancel, rollback, backup, repeat migration, stopped service and user configuration'

python3 - "$BIN_PATH" "$root" "$port" <<'PY'
import hashlib
import socket
import ssl
import subprocess
import sys
import time
from pathlib import Path

binary, root, port = sys.argv[1:]
root = Path(root)
current, old = (root / name / "server.json" for name in ("current", "old"))
proc = subprocess.Popen([binary, "server", "-c", current], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
try:
    for _ in range(50):
        if subprocess.run([binary, "tls-check", "-c", current], capture_output=True).returncode == 0:
            break
        time.sleep(0.1)
    else:
        raise RuntimeError("new pin did not authenticate")
    rejected = subprocess.run([binary, "tls-check", "-c", old], capture_output=True)
    assert rejected.returncode and "指纹不匹配" in rejected.stderr.decode(), rejected.stderr
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    with socket.create_connection(("127.0.0.1", int(port)), timeout=4) as raw, ctx.wrap_socket(raw) as tls:
        der = tls.getpeercert(binary_form=True)
    expected = subprocess.check_output([binary, "tls-identity", "-dir", current.parent], text=True).strip()
    assert hashlib.sha256(der).hexdigest() == expected and b"chronomorph" not in der.lower()
    names = subprocess.check_output(["openssl", "x509", "-inform", "DER", "-noout", "-subject", "-issuer", "-nameopt", "RFC2253"], input=der)
    assert names.lower().count(b"serialnumber=") == 2 and b"cn=" not in names.lower(), names
    print("PASS unauthenticated TLS peer sees unbranded subject and issuer; only the new TLS pin connects")
finally:
    proc.terminate()
    proc.wait(timeout=8)
PY
