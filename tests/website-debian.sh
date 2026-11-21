#!/usr/bin/env bash
set -eu
source "$(dirname "$0")/../cm.sh"
BIN_PATH=$(realpath "$1")
box=$(mktemp -d)
CFG_DIR="$box/config"
CFG_FILE="$CFG_DIR/server.json"
DEPLOY_LOCK="$box/deploy.lock"
pidfile="$box/service.pid"
origin_pid=''
stop_service() {
    if [[ -s $pidfile ]]; then
        kill "$(cat "$pidfile")" 2>/dev/null || true
        for _ in {1..60}; do
            kill -0 "$(cat "$pidfile")" 2>/dev/null || break
            # A subprocess that has exited may remain a zombie until reaped.
            if [[ $(ps -o stat= -p "$(cat "$pidfile")") == Z* ]]; then break; fi
            sleep 0.05
        done
        wait "$(cat "$pidfile")" 2>/dev/null || true
        rm -f "$pidfile"
    fi
}
cleanup() {
    stop_service
    if [[ -n $origin_pid ]]; then kill "$origin_pid" 2>/dev/null || true; wait "$origin_pid" 2>/dev/null || true; fi
    rm -rf "$box"
}
trap cleanup EXIT
systemctl() {
    case "$1" in
        is-active) [[ -s $pidfile ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null ;;
        restart) stop_service; "$BIN_PATH" server -c "$CFG_FILE" >> "$box/service.log" 2>&1 9>&- < /dev/null & echo "$!" > "$pidfile" ;;
        *) return 1 ;;
    esac
}
mkdir -p "$CFG_DIR" "$box/www"
export SSL_CERT_FILE="$box/cert.pem" CM_PUBLIC_IP=127.0.0.1
openssl req -new -newkey rsa:2048 -x509 -nodes -days 1 -subj '/CN=site.example' \
    -addext 'subjectAltName=DNS:site.example' -keyout "$box/key.pem" -out "$SSL_CERT_FILE" >/dev/null 2>&1
echo 'real website content' > "$box/www/index.html"
mapfile -t ports < <(python3 - <<'PY'
import socket
sockets = []
for i in range(4):
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    sockets.append(s)
    print(s.getsockname()[1])
PY
)
legacy=${ports[0]}
public=${ports[1]}
origin=${ports[2]}
unused=${ports[3]}
python3 -m http.server "$origin" --bind 127.0.0.1 --directory "$box/www" > "$box/origin.log" 2>&1 &
origin_pid=$!
for _ in {1..40}; do curl -fsS --noproxy '*' "http://127.0.0.1:$origin/" >/dev/null 2>&1 && break; sleep 0.05; done
key=$(printf 'ab%.0s' {1..32})
jq -n --arg addr "127.0.0.1:$legacy" --arg k "$key" '{listen:$addr,tls_listen:$addr,master_key:$k}' > "$CFG_FILE"
"$BIN_PATH" tls-identity -dir "$CFG_DIR" -create >/dev/null
systemctl restart chronomorph
check_tcp_entry
configure_website <<< "$(printf '1\nsite.example\n%s\n%s\n%s\nhttp://127.0.0.1:%s\n' "$public" "$SSL_CERT_FILE" "$box/key.pem" "$origin")"
links=$(show_links)
[[ $links == *cm+https://* && $links == *cm+auto://* ]]
configure_website <<< $'2\ny'
links=$(show_links)
[[ $links == *cm+https://* && $links != *cm+auto://* && $links != *' cm://'* ]]
LINKS="$links" python3 - "$legacy" "$public" <<'PY'
import base64, json, os, socket, sys
legacy, public = map(int, sys.argv[1:])
link = os.environ['LINKS'].split('cm+https://')[1].split()[0]
profile = json.loads(base64.urlsafe_b64decode(link + '==='))
assert profile['tls_port'] == public and profile['web_name'] == 'site.example'
assert 'tls_pin' not in profile and 'udp_port' not in profile
for kind in (socket.SOCK_STREAM, socket.SOCK_DGRAM):
    with socket.socket(type=kind) as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        sock.bind(('127.0.0.1', legacy))
        if kind == socket.SOCK_STREAM:
            sock.listen(1)
PY
curl -fsS --noproxy '*' --cacert "$SSL_CERT_FILE" --resolve "site.example:$public:127.0.0.1" "https://site.example:$public/" | grep -Fxq 'real website content'
cp "$CFG_FILE" "$box/expected"
# The candidate can bind, but its website has no HTTP origin. Restore both disk and service.
if configure_website <<< "$(printf '1\nsite.example\n%s\n%s\n%s\nhttp://127.0.0.1:%s\n' "$public" "$SSL_CERT_FILE" "$box/key.pem" "$unused")"; then exit 1; fi
cmp "$box/expected" "$CFG_FILE"
check_service_entries
configure_website <<< '3'
check_tcp_entry
configure_website <<< '4'
jq -e '.website == null' "$CFG_FILE" >/dev/null
echo 'PASS real website, authenticated tunnel, links, exclusive listeners, rollback and legacy restore'
