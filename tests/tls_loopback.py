"""Exercise the release CLI and SOCKS client with only a TCP path to the server."""
import contextlib
import json
from pathlib import Path
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time


IP = "127.0.0.1"
EXE = str(Path(sys.argv[1]).resolve())


SERVER_EXE = str(Path(sys.argv[2]).resolve()) if len(sys.argv) > 2 and not sys.argv[2].startswith("--") else EXE


def bind(kind):
    sock = socket.socket(socket.AF_INET, kind)
    sock.bind((IP, 0))
    return sock


def free_port():
    with bind(socket.SOCK_STREAM) as sock:
        return sock.getsockname()[1]


def stop(process):
    if process.poll() is None:
        process.terminate()
        process.wait(timeout=6)


def read(sock, size):
    data = bytearray()
    while len(data) < size:
        chunk = sock.recv(size - len(data))
        if not chunk:
            raise EOFError("proxy closed early")
        data.extend(chunk)
    return bytes(data)


def socks_connect(port, receive_buffer=None):
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    if receive_buffer:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, receive_buffer)
    sock.settimeout(15 if "--dual" in sys.argv else 7)
    sock.connect((IP, port))
    sock.sendall(b"\x05\x01\x00")
    assert read(sock, 2) == b"\x05\x00"
    return sock


def open_target(sock, origin_port):
    # Local IP literals are direct. This hostname goes through the server.
    sock.sendall(b"\x05\x01\x00\x03\x09localhost" + struct.pack(">H", origin_port))
    return read(sock, 10)[1]


def tcp_request(port, origin_port):
    with socks_connect(port) as sock:
        code = open_target(sock, origin_port)
        if code == 0:
            sock.sendall(b"tls-loopback")
            assert read(sock, 12) == b"tls-loopback"
        return code


def echo_tcp_once(listener):
    conn, _ = listener.accept()
    with conn:
        conn.sendall(read(conn, 12))


def echo_udp_once(sock):
    data, addr = sock.recvfrom(1024)
    sock.sendto(data, addr)


def test_udp(client_port, resources):
    origin = resources.enter_context(bind(socket.SOCK_DGRAM))
    threading.Thread(target=echo_udp_once, args=(origin,), daemon=True).start()
    with socks_connect(client_port) as control, bind(socket.SOCK_DGRAM) as udp:
        control.sendall(b"\x05\x03\x00\x01" + b"\0" * 6)
        reply = read(control, 10)
        assert reply[:2] == b"\x05\x00"
        relay = socket.inet_ntoa(reply[4:8]), int.from_bytes(reply[8:10], "big")
        packet = b"\0\0\0\x01" + socket.inet_aton(IP) + struct.pack(">H", origin.getsockname()[1]) + b"udp"
        udp.settimeout(5)
        udp.sendto(packet, relay)
        assert udp.recvfrom(1024)[0] == packet

def exercise_dual_failover(resources, server_cfg, tls_port, client_port, key, tcp):
    def start_server(udp_address):
        server_cfg.write_text(json.dumps({"master_key": key, "listen": udp_address,
                                         "tls_listen": f"{IP}:{tls_port}"}))
        proc = subprocess.Popen([SERVER_EXE, "server", "-c", str(server_cfg)])
        resources.callback(stop, proc)
        return proc

    def test_apps():
        threading.Thread(target=echo_tcp_once, args=(tcp,), daemon=True).start()
        assert tcp_request(client_port, tcp.getsockname()[1]) == 0
        test_udp(client_port, resources)

    # Accept TCP handshakes but never answer TLS. Only fixed-port UDP works.
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as blackhole:
        blackhole.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        blackhole.bind((IP, tls_port))
        blackhole.listen()
        udp_only = start_server(f"{IP}:{tls_port}")
        test_apps()
        print("PASS same client carries TCP and UDP when only fixed UDP works", flush=True)
        stop(udp_only)

    # The previous UDP session is now silently blackholed. Keep the client alive.
    tcp_only = start_server("127.0.0.2:0")
    deadline = time.monotonic() + 8
    while subprocess.run([EXE, "tls-check", "-c", str(server_cfg)], capture_output=True).returncode:
        if time.monotonic() >= deadline:
            raise RuntimeError("TLS listener did not start")
        time.sleep(0.1)
    test_apps()
    print("PASS same client switches both types of new traffic from dead UDP to TLS", flush=True)
    stop(tcp_only)

    both = start_server(f"{IP}:{tls_port}")
    deadline = time.monotonic() + 8
    while subprocess.run([EXE, "tls-check", "-c", str(server_cfg)], capture_output=True).returncode:
        if time.monotonic() >= deadline:
            raise RuntimeError("combined listener did not start")
        time.sleep(0.1)
    test_apps()
    print("PASS simultaneous TCP and UDP listeners on the same numeric port", flush=True)
    stop(both)


with tempfile.TemporaryDirectory() as tmp, contextlib.ExitStack() as resources:
    directory = Path(tmp)
    key = subprocess.check_output([EXE, "keygen"], text=True).split()[-1]
    pin = subprocess.check_output([EXE, "tls-identity", "-create", "-dir", tmp], text=True).strip()
    tls_port, client_port = free_port(), free_port()
    while client_port == tls_port:
        client_port = free_port()
    server_cfg = directory / "server.json"
    # The UDP socket is bound to a different loopback address with no redirect.
    server_cfg.write_text(json.dumps({"listen": "127.0.0.2:0", "master_key": key,
                                      "tls_listen": f"{IP}:{tls_port}"}))
    client_cfg = directory / "client.json"
    client_cfg.write_text(json.dumps({"listen": f"{IP}:{client_port}", "server_ip": IP,
                                      "master_key": key, "tls_port": tls_port, "tls_pin": pin,
                                      "udp_port": tls_port if "--dual" in sys.argv else 0}))
    server = subprocess.Popen([SERVER_EXE, "server", "-c", str(server_cfg)])
    resources.callback(stop, server)
    client = subprocess.Popen([EXE, "client", "-c", str(client_cfg)])
    resources.callback(stop, client)
    deadline = time.monotonic() + 8
    while True:
        result = subprocess.run([EXE, "tls-check", "-c", str(server_cfg)], capture_output=True)
        if result.returncode == 0:
            break
        if time.monotonic() > deadline:
            raise RuntimeError(result.stderr.decode())
        time.sleep(0.1)

    tcp = resources.enter_context(bind(socket.SOCK_STREAM))
    tcp.listen(2)
    threading.Thread(target=echo_tcp_once, args=(tcp,), daemon=True).start()
    assert tcp_request(client_port, tcp.getsockname()[1]) == 0
    print("PASS SOCKS5 TCP with server-side hostname resolution over TLS")

    test_udp(client_port, resources)
    print("PASS SOCKS5 UDP over TLS with no UDP path to the server")

    if "--basic" not in sys.argv:
        with socks_connect(client_port, receive_buffer=4096) as paused:
            assert open_target(paused, tcp.getsockname()[1]) == 0
            writer, _ = tcp.accept()
            with writer:
                writer.setsockopt(socket.SOL_SOCKET, socket.SO_SNDBUF, 65536)
                writer.settimeout(0.25)
                for _ in range(512):
                    try:
                        writer.sendall(bytes(65536))
                    except socket.timeout:
                        break
                else:
                    raise AssertionError("paused receiver never applied backpressure")
                threading.Thread(target=echo_tcp_once, args=(tcp,), daemon=True).start()
                assert tcp_request(client_port, tcp.getsockname()[1]) == 0
        print("PASS independent request while a download is paused", flush=True)

        with socks_connect(client_port) as sock:
            assert open_target(sock, tcp.getsockname()[1]) == 0
            origin_conn, _ = tcp.accept()
            with origin_conn:
                origin_conn.settimeout(5)
                sock.sendall(b"request")
                sock.shutdown(socket.SHUT_WR)
                assert read(origin_conn, 7) == b"request" and origin_conn.recv(1) == b""
                body = b"delayed-response" * 4096
                origin_conn.sendall(body)
                origin_conn.shutdown(socket.SHUT_WR)
                assert read(sock, len(body)) == body and sock.recv(1) == b""
        print("PASS client half-close followed by complete delayed response", flush=True)

    stop(server)
    if "--dual" in sys.argv:
        exercise_dual_failover(resources, server_cfg, tls_port, client_port, key, tcp)
    assert tcp_request(client_port, tcp.getsockname()[1]) != 0
    tcp.settimeout(0.3)
    try:
        conn, _ = tcp.accept()
    except socket.timeout:
        pass
    else:
        conn.close()
        raise AssertionError("client connected directly to the origin after TLS failed")
    print("PASS TLS outage refused the request without reaching the origin directly")
