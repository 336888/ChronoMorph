"""Verify public TLS forwarding and private ChronoMorph traffic on a single loopback port."""
import base64
import contextlib
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import time

from loopback_helpers import IP, bind, free_port, stop, tcp_request, test_udp, echo_tcp_once


EXE = str(Path(sys.argv[1]).resolve())


def wait_port(port, proc):
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"service exited early ({proc.returncode})")
        try:
            with socket.create_connection((IP, port), timeout=.25):
                return
        except OSError:
            time.sleep(.05)
    raise TimeoutError(f"local listener {port} did not start")


def fetch(port, cert):
    ctx = ssl.create_default_context(cafile=str(cert))
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.set_alpn_protocols(["http/1.1"])
    with socket.create_connection((IP, port), timeout=6) as raw:
        with ctx.wrap_socket(raw, server_hostname="example.com") as tls:
            leaf = tls.getpeercert(binary_form=True)
            tls.sendall(b"GET / HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n\r\n")
            response = tls.makefile("rb").read()
            assert b"200 OK" in response and response.endswith(b"local REALITY test origin\n")
            return leaf


def main():
    with tempfile.TemporaryDirectory() as tmp, contextlib.ExitStack() as handles:
        base = Path(tmp)
        cert = base / "origin.pem"
        origin = subprocess.Popen([sys.argv[2], str(cert)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        handles.callback(stop, origin)
        target = origin.stdout.readline().strip()
        public = free_port()
        key = os.urandom(32).hex()
        config = base / "server.json"
        config.write_text(json.dumps({"master_key": key, "listen": "127.0.0.1:0", "reality": {
            "name": "example.com", "target": target, "listen": f"{IP}:{public}", "only": True}}))
        node = json.loads(subprocess.check_output([EXE, "reality", "-c", str(config), "-create", "-export"]))
        server = subprocess.Popen([EXE, "server", "-c", str(config)])
        handles.callback(stop, server)
        wait_port(public, server)
        assert fetch(public, cert) == fetch(int(target.split(":")[-1]), cert)
        print("PASS public website content, verified hostname and identical origin certificate", flush=True)
        # Go uses the system certificate store on Windows. The verified fetch above
        # and the authenticated echo below exercise both paths with our local CA.
        if os.name != "nt":
            subprocess.run([EXE, "reality", "-c", str(config), "-check"], check=True, timeout=30,
                           env=dict(os.environ, SSL_CERT_FILE=str(cert)))
        tcp = handles.enter_context(bind(socket.SOCK_STREAM))
        tcp.listen()
        for field, value in [(None, None), ("reality_key", base64.urlsafe_b64encode(os.urandom(32)).decode().rstrip("=")),
                             ("reality_id", os.urandom(8).hex()), ("master_key", os.urandom(32).hex()),
                             ("tls_pin", os.urandom(32).hex())]:
            port = free_port()
            profile = dict(node, server_ip=IP, master_key=key, listen=f"{IP}:{port}")
            if field:
                profile[field] = value
            client = base / "client.json"
            client.write_text(json.dumps(profile))
            proc = subprocess.Popen([EXE, "client", "-c", str(client)])
            try:
                wait_port(port, proc)
                if field:
                    assert tcp_request(port, tcp.getsockname()[1]) != 0, field
                    print("PASS rejected incorrect", field, flush=True)
                else:
                    threading.Thread(target=echo_tcp_once, args=(tcp,), daemon=True).start()
                    assert tcp_request(port, tcp.getsockname()[1]) == 0
                    test_udp(port, handles)
                    print("PASS authenticated TCP echo and SOCKS5 UDP echo", flush=True)
            finally:
                stop(proc)


if __name__ == "__main__":
    main()
