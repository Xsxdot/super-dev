"""runtime-validation Python fixture 的无依赖 HTTP 运行与 debugpy 断点合同。

职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
边界：不安装依赖、不访问 SuperDev API，也不持久化 campaign secret。
"""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from urllib.parse import parse_qs, urlparse


class Handler(BaseHTTPRequestHandler):
    """处理 fixture 的三个固定 HTTP 路径。"""

    def do_GET(self) -> None:
        if self.path == "/healthz":
            self._write(200, {"ready": True, "provider": "python"})
            return
        self._write(404, {"ok": False})

    def do_POST(self) -> None:
        parsed = urlparse(self.path)
        if parsed.path != "/api/probe":
            self._write(404, {"ok": False})
            return
        fixture_marker = "breakpoint-visible"
        fixture_count = 42
        fixture_provider = "python"
        _ = fixture_marker  # SUPERDEV_FIXTURE_BREAKPOINT
        controlled_error = parse_qs(parsed.query).get("mode") == ["error"]
        self._write(
            500 if controlled_error else 200,
            {"ok": not controlled_error, "provider": fixture_provider, "count": fixture_count},
        )

    def log_message(self, format: str, *args: object) -> None:
        return

    def _write(self, status: int, value: object) -> None:
        raw = json.dumps(value).encode("utf-8")
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def main() -> None:
    """在 runner 分配的 loopback 动态端口启动 fixture。"""
    port = int(os.environ["FIXTURE_PORT"])
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
