"""Python Windows validation fixture.

职责：提供 Fixture Protocol v1 readiness、鉴权 probe、受控错误、结构化日志与稳定断点现场。
边界：仅使用 Python 标准库；不调用 SuperDev/MCP，不向日志写入 Authorization 或凭据。
"""

from __future__ import annotations

import hmac
import json
import os
import signal
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

PROVIDER = "python"
CONTRACT_VERSION = "v1"
DEFAULT_PORT = 18172
MAX_BODY_BYTES = 64 * 1024


def write_log(level: str, event: str, **fields: Any) -> None:
    """写入一条结构化 JSON line；调用方只能传入已确认不含凭据的字段。"""

    record = {
        "level": level,
        "event": event,
        "provider": PROVIDER,
        "campaign_id": os.environ.get("FIXTURE_CAMPAIGN_ID", "standalone"),
        **fields,
    }
    stream = sys.stderr if level == "error" else sys.stdout
    stream.write(json.dumps(record, separators=(",", ":"), ensure_ascii=True) + "\n")
    stream.flush()


def is_authorized(header: str | None) -> bool:
    """从非秘密 campaign ID 推导 Bearer 值并定长比较，不回显原始 header。"""

    campaign_id = os.environ.get("FIXTURE_CAMPAIGN_ID", "").strip()
    if not campaign_id:
        return False
    expected = f"Bearer superdev-validation-{campaign_id}"
    return hmac.compare_digest((header or "").strip(), expected)


def fixture_probe(value: int) -> dict[str, Any]:
    """生成稳定、非秘密的断点变量，并返回业务计算结果。"""

    fixture_marker = "breakpoint-visible"
    fixture_count = value + 1
    fixture_provider = PROVIDER
    # SUPERDEV_FIXTURE_BREAKPOINT：断点落在响应生成前，三个局部变量均已稳定赋值。
    return {
        "fixture_marker": fixture_marker,
        "fixture_count": fixture_count,
        "fixture_provider": fixture_provider,
    }


class FixtureHandler(BaseHTTPRequestHandler):
    """实现公开 HTTP 合同；不会暴露进程环境、文件系统或请求凭据。"""

    server_version = "SuperDevFixture/1"

    def log_message(self, _format: str, *_args: Any) -> None:
        """禁用基类非结构化访问日志，所有观察点统一走 write_log。"""

    def send_json(self, status: int, payload: dict[str, Any]) -> None:
        """发送带固定 content type/length 的 JSON 响应。"""

        body = json.dumps(payload, separators=(",", ":"), ensure_ascii=True).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        """处理无需鉴权的 readiness 请求。"""

        if self.path != "/healthz":
            self.send_json(404, {"ok": False, "code": "fixture_not_found", "provider": PROVIDER})
            write_log("error", "fixture_request_rejected", reason="route_not_found", status=404)
            return
        self.send_json(
            200,
            {"ready": True, "contract_version": CONTRACT_VERSION, "provider": PROVIDER},
        )
        write_log("info", "fixture_readiness_succeeded", status=200)

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
        """处理鉴权 probe，并把业务错误限制为稳定 HTTP 500。"""

        if self.path != "/api/probe":
            self.send_json(404, {"ok": False, "code": "fixture_not_found", "provider": PROVIDER})
            write_log("error", "fixture_request_rejected", reason="route_not_found", status=404)
            return
        if not is_authorized(self.headers.get("Authorization")):
            self.send_json(401, {"ok": False, "code": "fixture_unauthorized", "provider": PROVIDER})
            write_log("error", "fixture_request_rejected", reason="unauthorized", status=401)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0 or length > MAX_BODY_BYTES:
                raise ValueError("request body exceeds 64 KiB")
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
        except (ValueError, UnicodeDecodeError, json.JSONDecodeError) as error:
            self.send_json(400, {"ok": False, "code": "fixture_invalid_request", "provider": PROVIDER})
            write_log("error", "fixture_request_failed", reason="invalid_json", status=400, cause=str(error))
            return

        trace_id = payload.get("trace_id") if isinstance(payload.get("trace_id"), str) else ""
        request_id = payload.get("request_id") if isinstance(payload.get("request_id"), str) else ""
        if not trace_id or not request_id:
            self.send_json(400, {"ok": False, "code": "fixture_invalid_request", "provider": PROVIDER})
            write_log("error", "fixture_request_failed", reason="correlation_id_required", status=400)
            return
        outcome = "error" if payload.get("outcome") == "error" else "ok"
        value = payload.get("value", 41)
        if not isinstance(value, int):
            value = 41
        write_log("info", "fixture_request_started", trace_id=trace_id, request_id=request_id, outcome=outcome)
        probe = fixture_probe(value)
        status = 500 if outcome == "error" else 200
        code = "fixture_controlled_error" if outcome == "error" else "fixture_ok"
        self.send_json(
            status,
            {
                "ok": outcome == "ok",
                "code": code,
                "provider": PROVIDER,
                "trace_id": trace_id,
                "request_id": request_id,
                "result": probe["fixture_count"],
            },
        )
        write_log(
            "error" if outcome == "error" else "info",
            "fixture_request_completed",
            trace_id=trace_id,
            request_id=request_id,
            outcome=outcome,
            status=status,
        )


def main() -> int:
    """启动 loopback HTTP 服务并在 Windows 控制信号后干净关闭。"""

    if os.environ.get("FIXTURE_STARTUP_MODE") == "fail":
        write_log("error", "fixture_startup_failed", reason="controlled_startup_failure")
        return 23
    if not os.environ.get("FIXTURE_CAMPAIGN_ID", "").strip():
        write_log("error", "fixture_startup_failed", stage="configuration", cause="FIXTURE_CAMPAIGN_ID is required")
        return 24
    try:
        port = int(os.environ.get("FIXTURE_PORT", str(DEFAULT_PORT)))
    except ValueError as error:
        write_log("error", "fixture_startup_failed", stage="parse_port", cause=str(error))
        return 24
    try:
        server = ThreadingHTTPServer(("127.0.0.1", port), FixtureHandler)
    except OSError as error:
        write_log("error", "fixture_startup_failed", stage="listen", port=port, cause=str(error))
        return 24
    server.daemon_threads = True
    stop_requested = threading.Event()

    def request_stop(signum: int, _frame: Any) -> None:
        # handle_request 使用短 timeout，因此信号处理器只置位，不会与 shutdown 互等死锁。
        write_log("info", "fixture_stopping", signal=signum)
        stop_requested.set()

    signal.signal(signal.SIGINT, request_stop)
    if hasattr(signal, "SIGTERM"):
        signal.signal(signal.SIGTERM, request_stop)
    server.timeout = 0.25
    write_log("info", "fixture_started", host="127.0.0.1", port=port, contract_version=CONTRACT_VERSION)
    try:
        while not stop_requested.is_set():
            server.handle_request()
    except OSError as error:
        write_log("error", "fixture_run_failed", stage="serve", cause=str(error))
        return 24
    finally:
        server.server_close()
    write_log("info", "fixture_stopped")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
