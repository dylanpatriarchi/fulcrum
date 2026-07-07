#!/usr/bin/env python3
"""A trivial HTTP backend for Fulcrum demos.

Usage: fake_backend.py <port> <name>
Serves any path with "hello from <name>"; /healthz returns 200.
"""
import sys
import http.server
import socketserver


def main() -> None:
    port = int(sys.argv[1])
    name = sys.argv[2] if len(sys.argv) > 2 else f"backend:{port}"

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):  # noqa: N802
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(f"hello from {name}\n".encode())

        def log_message(self, *args):  # silence per-request logging
            pass

    with socketserver.ThreadingTCPServer(("127.0.0.1", port), Handler) as httpd:
        httpd.serve_forever()


if __name__ == "__main__":
    main()
