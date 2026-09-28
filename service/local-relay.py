"""Expose the two WSL test nodes on the Windows Wi-Fi address."""

import select
import os
import socket
import socketserver
import subprocess
import sys
import threading


class Relay(socketserver.BaseRequestHandler):
    def handle(self):
        with socket.create_connection((self.server.wsl_address, self.server.server_address[1])) as peer:
            sockets = (self.request, peer)
            while True:
                readable, _, _ = select.select(sockets, (), ())
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    sockets[1 if source is sockets[0] else 0].sendall(data)


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: python local-relay.py <PC Wi-Fi IPv4>")
    wsl_address = subprocess.check_output(["wsl.exe", "hostname", "-I"], text=True).split()[0]
    servers = []
    try:
        for port in map(int, os.environ.get("RASSVET_PORTS", "18443,18444").split(",")):
            server = Server((sys.argv[1], port), Relay)
            server.wsl_address = wsl_address
            servers.append(server)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            print(f"{sys.argv[1]}:{port} -> {wsl_address}:{port}", flush=True)
        threading.Event().wait()
    except KeyboardInterrupt:
        pass
    finally:
        for server in servers:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
