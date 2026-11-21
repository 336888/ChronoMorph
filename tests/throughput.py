"""Measure release binaries through a delayed, rate-limited local link.

Example: python3 tests/throughput.py ./chronomorph --rtt 200 --mbps 100
Times include application payload delivery and exclude tunnel establishment.
"""
import argparse
import asyncio
from collections import deque
from contextlib import ExitStack
import hashlib
import json
from pathlib import Path
import random
import socket
import statistics
import struct
import subprocess
import tempfile
import time


HOST = "127.0.0.1"
BLOCK = bytes(range(256)) * 256


class Link:
    """One FIFO per direction, with propagation delay and bounded queuing."""

    def __init__(self, args, seed):
        self.loop = asyncio.get_running_loop()
        self.delay = args.rtt / 2000
        self.rate = args.mbps * 1e6 / 8
        self.loss = args.loss / 100
        self.random = random.Random(seed)
        self.free_at = 0
        self.items = deque()
        self.handle = None

    def put(self, data, deliver, datagram=False):
        now = self.loop.time()
        if datagram:
            if self.free_at - now > 0.1 or self.random.random() < self.loss:
                return
        self.free_at = max(self.free_at, now) + len(data) / self.rate
        self.items.append((self.free_at + self.delay, deliver, data))
        if self.handle is None:
            self.handle = self.loop.call_at(self.items[0][0], self.flush)

    def flush(self):
        now = self.loop.time()
        while self.items and self.items[0][0] <= now:
            _, deliver, data = self.items.popleft()
            deliver(data)
        self.handle = self.loop.call_at(self.items[0][0], self.flush) if self.items else None

    def close(self):
        if self.handle:
            self.handle.cancel()
        self.items.clear()


def port(kind):
    with socket.socket(type=kind) as sock:
        sock.bind((HOST, 0))
        return sock.getsockname()[1]


class UDPForwarder(asyncio.DatagramProtocol):
    def __init__(self, backend, up, down):
        self.backend = backend
        self.up, self.down = up, down
        self.client = None

    def connection_made(self, transport):
        self.transport = transport

    def datagram_received(self, data, source):
        if source == self.backend:
            dest, link = self.client, self.down
        else:
            self.client, dest, link = source, self.backend, self.up
        if dest:
            link.put(data, lambda d: self.transport.sendto(d, dest), True)


async def main(args):
    binary = str(Path(args.binary).resolve())
    server_bin = str(Path(args.server or binary).resolve())
    loop = asyncio.get_running_loop()
    up, down = Link(args, 1), Link(args, 2)
    backend_udp, backend_tcp = port(socket.SOCK_DGRAM), port(socket.SOCK_STREAM)
    proxy_port = port(socket.SOCK_STREAM)
    while proxy_port == backend_tcp:
        proxy_port = port(socket.SOCK_STREAM)

    async def tcp_forward(reader, writer):
        other_reader, other_writer = await asyncio.open_connection(HOST, backend_tcp)

        async def pump(src, dst, link):
            while data := await src.read(64 * 1024):
                link.put(data, dst.write)
                # Apply backpressure without serializing the propagation delay.
                if link.free_at > loop.time() + 0.05:
                    await asyncio.sleep(link.free_at - loop.time())
                await dst.drain()

        try:
            await asyncio.gather(pump(reader, other_writer, up), pump(other_reader, writer, down))
        finally:
            writer.close()
            other_writer.close()

    with tempfile.TemporaryDirectory() as tmp, ExitStack() as stack:
        root = Path(tmp)
        key = subprocess.check_output([server_bin, "keygen"], text=True).split()[-1]
        pin = subprocess.check_output([server_bin, "tls-identity", "-create", "-dir", tmp], text=True).strip()
        tcp = await asyncio.start_server(tcp_forward, HOST, 0)
        tcp_front = tcp.sockets[0].getsockname()[1]
        stack.callback(tcp.close)
        udp_socket = stack.enter_context(socket.socket(type=socket.SOCK_DGRAM))
        udp_socket.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 4 << 20)
        udp_socket.bind((HOST, 0))
        udp_front = udp_socket.getsockname()[1]
        udp, _ = await loop.create_datagram_endpoint(lambda: UDPForwarder((HOST, backend_udp), up, down), sock=udp_socket)
        stack.callback(udp.close)
        unused = stack.enter_context(socket.socket())
        unused.bind((HOST, 0))
        server = {"listen": f"{HOST}:{backend_udp}", "tls_listen": f"{HOST}:{backend_tcp}", "master_key": key}
        client = {"listen": f"{HOST}:{proxy_port}", "server_ip": HOST, "master_key": key,
                  "tls_port": tcp_front if args.transport != "udp" else unused.getsockname()[1], "tls_pin": pin}
        if args.transport != "tcp":
            client["udp_port"] = udp_front
        client_log = stack.enter_context((root / "client.log").open("w+"))
        for exe, mode, cfg in [(server_bin, "server", server), (binary, "client", client)]:
            path = root / (mode + ".json")
            path.write_text(json.dumps(cfg))
            proc = subprocess.Popen([exe, mode, "-c", str(path)], stdout=subprocess.DEVNULL, stderr=client_log if mode == "client" else subprocess.DEVNULL)
            stack.callback(proc.wait, 5)
            stack.callback(proc.terminate)

        origin_connections = asyncio.Queue()
        origin = await asyncio.start_server(lambda r, w: origin_connections.put_nowait((r, w)), HOST, 0)
        stack.callback(origin.close)
        target = origin.sockets[0].getsockname()[1]
        for _ in range(100):
            try:
                cr, cw = await asyncio.open_connection(HOST, proxy_port)
                break
            except ConnectionRefusedError:
                await asyncio.sleep(.05)
        else:
            raise RuntimeError("proxy did not start")
        stack.callback(cw.close)
        cw.write(b"\x05\x01\x00")
        assert await cr.readexactly(2) == b"\x05\x00"
        open_start = time.monotonic()
        cw.write(b"\x05\x01\x00\x03\x09localhost" + struct.pack(">H", target))
        assert (await cr.readexactly(10))[1] == 0, "SOCKS connection failed"
        elapsed = round((time.monotonic() - open_start) * 1000)
        selected = "TLS/TCP" if args.transport == "tcp" else "UDP"
        if args.transport == "auto":
            client_log.seek(0)
            selected = next(line.split()[-1] for line in client_log if "网页通道已连接" in line)
        print(json.dumps(dict(selected=selected, first_connect_ms=elapsed)), flush=True)
        sr, sw = await origin_connections.get()
        stack.callback(sw.close)

        async def transfer(reader, writer, size):
            async def send():
                for _ in range(size // len(BLOCK)):
                    writer.write(BLOCK)
                    await writer.drain()

            async def recv():
                left = size
                digest = hashlib.sha256()
                while left:
                    data = await reader.read(min(left, 256 * 1024))
                    if not data:
                        raise EOFError("incomplete application payload")
                    left -= len(data)
                    digest.update(data)
                return digest.digest()

            start = time.monotonic()
            _, digest = await asyncio.gather(send(), recv())
            elapsed = time.monotonic() - start
            expected = hashlib.sha256()
            for _ in range(size // len(BLOCK)):
                expected.update(BLOCK)
            assert digest == expected.digest()
            return round(size * 8 / elapsed / 1e6, 2)

        for direction, reader, writer in [("download", cr, sw), ("upload", sr, cw)]:
            await transfer(reader, writer, 1 << 20)
            values = [await transfer(reader, writer, args.mib << 20) for _ in range(args.runs)]
            print(json.dumps(dict(transport=args.transport, direction=direction, rtt_ms=args.rtt, limit_mbps=args.mbps,
                                  udp_loss_pct=args.loss, mib=args.mib, mbps=values)), flush=True)
            if statistics.median(values) < args.min_mbps:
                raise RuntimeError(f"{direction} below {args.min_mbps:g} Mbps: {values}")
        up.close()
        down.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("--server", help="separate server binary for compatibility measurements")
    parser.add_argument("--transport", choices=["tcp", "udp", "auto"], default="tcp")
    parser.add_argument("--rtt", type=int, default=200)
    parser.add_argument("--mbps", type=int, default=100)
    parser.add_argument("--mib", type=int, default=8)
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--min-mbps", type=float, default=0, help="minimum median for each direction")
    parser.add_argument("--loss", type=float, default=0, help="percent random UDP datagram loss per direction")
    args = parser.parse_args()
    if min(args.mbps, args.mib, args.runs) <= 0 or args.rtt < 0 or args.min_mbps < 0 or not 0 <= args.loss < 100:
        parser.error("invalid link or transfer size")
    asyncio.run(asyncio.wait_for(main(args), timeout=600))
