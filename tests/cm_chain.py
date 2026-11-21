"""Prove two independent CM peers are required for application TCP and UDP."""
import contextlib
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import threading

from loopback_helpers import IP, bind, free_port, stop, tcp_request, test_udp, echo_tcp_once
from reality_loopback import wait_port

exe, origin_exe = map(lambda p: str(Path(p).resolve()), sys.argv[1:3])
peer = str(Path(sys.argv[3]).resolve()) if len(sys.argv) > 3 else exe

with tempfile.TemporaryDirectory() as tmp, contextlib.ExitStack() as clean:
    root = Path(tmp)
    origin = subprocess.Popen([origin_exe, str(root / "origin.pem")], stdin=subprocess.PIPE,
                              stdout=subprocess.PIPE, text=True)
    clean.callback(stop, origin)
    target = origin.stdout.readline().strip()
    nodes = []
    servers = []
    for name in ("entry", "exit"):
        d = root / name
        d.mkdir()
        key = os.urandom(32).hex()
        port = free_port()
        cfg = d / "server.json"
        cfg.write_text(json.dumps({"master_key": key, "listen": IP + ":0", "reality": {
            "name": "example.com", "target": target, "listen": f"{IP}:{port}", "only": True}}))
        node = json.loads(subprocess.check_output([peer, "reality", "-c", str(cfg), "-create", "-export"]))
        nodes.append(dict(node, server_ip=IP, master_key=key, name=name))
        server = subprocess.Popen([peer, "server", "-c", str(cfg)])
        servers.append(server)
        clean.callback(stop, server)
        wait_port(port, server)

    entry = nodes[0]
    identity = f"{IP}|{entry['tls_port']}|cm+reality||example.com|{entry['master_key']}"
    client_cfg = root / "client.json"
    local = free_port()
    client_cfg.write_text(json.dumps(dict(nodes[1], profiles=nodes, active_index=1,
        listen=f"{IP}:{local}", chain_entry=hashlib.sha256(identity.encode()).hexdigest())))
    def start_client():
        proc = subprocess.Popen([exe, "client", "-c", str(client_cfg)])
        clean.callback(stop, proc)
        wait_port(local, proc)
        return proc
    client = start_client()
    tcp = clean.enter_context(bind(socket.SOCK_STREAM))
    tcp.listen()
    threading.Thread(target=echo_tcp_once, args=(tcp,), daemon=True).start()
    assert tcp_request(local, tcp.getsockname()[1]) == 0
    test_udp(local, clean)
    print("PASS CM entry -> CM exit: TCP and UDP echo through two REALITY nodes", flush=True)
    stop(client)
    stop(servers[0])
    # Keep the exit online and start a fresh client, without existing sessions.
    wait_port(nodes[1]["tls_port"], servers[1])
    start_client()
    assert tcp_request(local, tcp.getsockname()[1]) != 0, "bypassed the unavailable entry"
    print("PASS CM chain fails closed while exit is independently reachable", flush=True)
