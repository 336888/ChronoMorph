"""Keep a single proxied TCP connection open while a UDP NAT changes its mapping."""
import contextlib
import hmac
import json
from pathlib import Path
import secrets
import selectors
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time


IP = "127.0.0.1"


def bound(kind):
    sock = socket.socket(socket.AF_INET, kind)
    sock.bind((IP, 0))
    return sock


def exact(sock, length):
    buf = bytearray()
    while len(buf) < length:
        chunk = sock.recv(length - len(buf))
        if not chunk:
            raise EOFError("proxy closed during NAT rebinding")
        buf.extend(chunk)
    return bytes(buf)


def run_nat(selector, origin, upstreams, stopped, mapping, block_tcp):
    lanes, clients = {}, {}
    while not stopped.is_set():
        for event, _ in selector.select(0.1):
            sock = event.fileobj
            packet, sender = sock.recvfrom(4096)
            if event.data == "front":
                lane = lanes.setdefault(sender, len(lanes))
                if lane >= len(upstreams) or (lane == 0 and block_tcp.is_set()):
                    continue
                current = upstreams[lane][mapping[0]]
                clients[current] = sock, sender
                current.sendto(packet, origin)
            elif sock in clients:
                lane, index = event.data
                if index == mapping[0] and not (lane == 0 and block_tcp.is_set()):
                    front, client = clients[sock]
                    front.sendto(packet, client)


def test_game_traffic(proxy, block_tcp):
    with socket.create_connection(proxy, 5) as ctl, bound(socket.SOCK_DGRAM) as local, bound(socket.SOCK_DGRAM) as echo:
        local.settimeout(4)
        echo.settimeout(4)
        ctl.sendall(b"\x05\x01\x00")
        assert exact(ctl, 2) == b"\x05\x00"
        ctl.sendall(b"\x05\x03\x00\x01" + bytes(6))
        reply = exact(ctl, 10)
        assert reply[:2] == b"\x05\x00"
        relay = socket.inet_ntoa(reply[4:8]), int.from_bytes(reply[8:10], "big")
        prefix = b"\0\0\0\x01" + socket.inet_aton(IP) + struct.pack(">H", echo.getsockname()[1])
        block_tcp.set()
        try:
            for index in range(32):
                msg = bytes([index]) * 128
                local.sendto(prefix + msg, relay)
                data, sender = echo.recvfrom(2048)
                assert data == msg
                echo.sendto(data, sender)
                assert local.recvfrom(2048)[0] == prefix + msg
        finally:
            block_tcp.clear()
    print("PASS 32 game datagrams while the bulk TCP transport loses all packets", flush=True)


def connect_tcp(stack, proxy, listener):
    deadline = time.monotonic() + 20
    while True:
        try:
            client = stack.enter_context(socket.create_connection(proxy, 8))
            break
        except ConnectionRefusedError:
            if time.monotonic() >= deadline:
                raise
            time.sleep(0.1)
    client.sendall(b"\x05\x01\x00")
    assert exact(client, 2) == b"\x05\x00"
    client.sendall(b"\x05\x01\x00\x03\x09localhost" + struct.pack(">H", listener.getsockname()[1]))
    assert exact(client, 10)[:2] == b"\x05\x00"
    return client


def stop_process(proc):
    proc.terminate()
    proc.wait(timeout=5)


with tempfile.TemporaryDirectory() as tmp, contextlib.ExitStack() as stack:
    exe = str(Path(sys.argv[1]).resolve())
    server_exe = str(Path(sys.argv[2]).resolve()) if len(sys.argv) > 2 and not sys.argv[2].startswith("--") else exe
    key = secrets.token_bytes(32)
    with bound(socket.SOCK_DGRAM) as reserved:
        backend = reserved.getsockname()
    with bound(socket.SOCK_STREAM) as reserved:
        proxy = reserved.getsockname()
    listener = stack.enter_context(bound(socket.SOCK_STREAM))
    listener.listen()
    listener.settimeout(8)
    selector = stack.enter_context(selectors.DefaultSelector())
    upstreams = [[stack.enter_context(bound(socket.SOCK_DGRAM)) for _ in range(3)] for _ in range(3)]
    for lane, sockets in enumerate(upstreams):
        for index, upstream in enumerate(sockets):
            selector.register(upstream, selectors.EVENT_READ, (lane, index))
    epoch = int(time.time()) // 30
    ports = set()
    for ep in range(epoch - 1, epoch + 7):
        digest = hmac.digest(key, b"ChronoMorph-Meta-v1" + struct.pack(">Q", ep), "sha256")
        ports.add(20000 + int.from_bytes(digest[4:6], "big") % 30000)
    for port in ports:
        sock = stack.enter_context(socket.socket(socket.AF_INET, socket.SOCK_DGRAM))
        sock.bind((IP, port))
        selector.register(sock, selectors.EVENT_READ, "front")
    stopped, block_tcp, mapping = threading.Event(), threading.Event(), [0]
    nat = threading.Thread(target=run_nat, args=(selector, backend, upstreams, stopped, mapping, block_tcp), daemon=True)
    nat.start()
    stack.callback(nat.join, 2)
    stack.callback(stopped.set)
    for binary, mode, address in [(server_exe, "server", backend), (exe, "client", proxy)]:
        path = Path(tmp, mode + ".json")
        path.write_text(json.dumps(dict(listen=f"{IP}:{address[1]}", master_key=key.hex(), server_ip=IP)))
        proc = subprocess.Popen([binary, mode, "-c", str(path)])
        stack.callback(stop_process, proc)
        if mode == "client":
            first_client = proc
    client = connect_tcp(stack, proxy, listener)
    target, _ = listener.accept()
    with target:
        target.settimeout(6)
        for idx in range(len(upstreams[0])):
            mapping[0] = idx
            if idx == 2 and "--idle" in sys.argv:
                time.sleep(65)
            msg = bytes([idx]) * 8192
            client.sendall(msg)
            assert exact(target, len(msg)) == msg
            target.sendall(msg)
            assert exact(client, len(msg)) == msg
            print(f"PASS existing bidirectional TCP stream over UDP mapping {idx+1}", flush=True)
        if "--isolate" in sys.argv:
            test_game_traffic(proxy, block_tcp)
            client.sendall(b"after")
            assert exact(target, 5) == b"after"
        if "--restart" in sys.argv:
            block = bytes(512 * 1024)
            client.sendall(block)
            assert exact(target, len(block)) == block
            stop_process(first_client)
            fresh = subprocess.Popen([exe, "client", "-c", str(Path(tmp, "client.json"))])
            stack.callback(stop_process, fresh)
            second = connect_tcp(stack, proxy, listener)
            new_target, _ = listener.accept()
            with new_target:
                new_target.settimeout(4)
                second.sendall(b"restarted")
                assert exact(new_target, 9) == b"restarted"
                new_target.sendall(b"ok")
                assert exact(second, 2) == b"ok"
            print("PASS immediate client process restart with the server kept online", flush=True)
