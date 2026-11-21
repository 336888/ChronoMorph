"""Common socket fixtures for release integration tests."""
import socket
import struct
import sys
import threading

IP = "127.0.0.1"


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
