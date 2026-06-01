"""Python HTTP pipeline example.

Responsibilities:
  - Expose /health for deployment health checks.
  - Expose /info with language and app metadata.

Boundaries:
  - Uses only the Python standard library.
  - Does not depend on a reverse proxy.
"""

import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        if self.path == "/info":
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.end_headers()
            payload = {"app": "python-http", "language": "python", "version": os.getenv("APP_VERSION", "")}
            self.wfile.write(json.dumps(payload).encode("utf-8"))
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"python-http")


if __name__ == "__main__":
    port = int(os.getenv("PORT", "18082"))
    HTTPServer(("0.0.0.0", port), Handler).serve_forever()
