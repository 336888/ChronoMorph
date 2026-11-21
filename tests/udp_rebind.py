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


def run_nat(selector, origin, upstreams, stopped, mapping):
    client = None
    while not stopped.is_set():
        for event, _ in selector.select(0.1):
            sock = event.fileobj
            packet, sender = sock.recvfrom(4096)
            current = upstreams[mapping[0]]
            if event.data == "front":
                client = sock, sender
                current.sendto(packet, origin)
            elif sock is current and client:
                client[0].sendto(packet, client[1])


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
    upstreams = [stack.enter_context(bound(socket.SOCK_DGRAM)) for _ in range(3)]
    for upstream in upstreams:
        selector.register(upstream, selectors.EVENT_READ, "back")
    epoch = int(time.time()) // 30
    ports = set()
    for ep in range(epoch - 1, epoch + 7):
        digest = hmac.digest(key, b"ChronoMorph-Meta-v1" + struct.pack(">Q", ep), hashlib.sha256)
        ports.add(20000 + int.from_bytes(digest[4:6], "big") % 30000)
    for port in ports:
        sock = stack.enter_context(socket.socket(socket.AF_INET, socket.SOCK_DGRAM))
        sock.bind((IP, port))
        selector.register(sock, selectors.EVENT_READ, "front")
    stopped, mapping = threading.Event(), [0]
    nat = threading.Thread(target=run_nat, args=(selector, backend, upstreams, stopped, mapping), daemon=True)
    nat.start()
    stack.callback(nat.join, 2)
    stack.callback(stopped.set)
    for binary, mode, address in [(server_exe, "server", backend), (exe, "client", proxy)]:
        path = Path(tmp, mode + ".json")
        path.write_text(json.dumps(dict(listen=f"{IP}:{address[1]}", master_key=key.hex(), server_ip=IP)))
        stack.callback(stop_process, subprocess.Popen([binary, mode, "-c", str(path)]))
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
    target, _ = listener.accept()
    with target:
        target.settimeout(6)
        for idx in range(len(upstreams)):
            mapping[0] = idx
            if idx == 2 and "--idle" in sys.argv:
                time.sleep(65)
            msg = bytes([idx]) * 8192
            client.sendall(msg)
            assert exact(target, len(msg)) == msg
            target.sendall(msg)
            assert exact(client, len(msg)) == msg
            print(f"PASS existing bidirectional TCP stream over UDP mapping {idx+1}", flush=True)
