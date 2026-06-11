#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Smoke test the SmartInsure Go backend with simulated user inputs.

Usage:
  python3 scripts/smoke_user_flow.py --start-server
  python3 scripts/smoke_user_flow.py --base-url http://127.0.0.1:34567
"""

from __future__ import annotations

import argparse
import contextlib
import http.server
import json
import os
import pathlib
import signal
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
from typing import Any


ROOT = pathlib.Path(__file__).resolve().parents[1]

MOCK_PRODUCT_HTML = """<!doctype html>
<html>
<head><meta charset="utf-8"><title>烟测百万医疗险</title></head>
<body>
  <h1>烟测百万医疗险</h1>
  <p>一般医疗保险金 300万 保障住院医疗费用、特殊门诊费用和门诊手术费用。</p>
  <p>重大疾病医疗保险金 300万 保障重大疾病住院医疗费用和相关治疗费用。</p>
  <p>可选外购药保险金 100万 可附加报销院外特定药品费用。</p>
</body>
</html>
"""


class MockProductHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self) -> None:  # noqa: N802
        if self.path != "/product/mock":
            self.send_response(404)
            self.end_headers()
            return
        data = MOCK_PRODUCT_HTML.encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt: str, *args: Any) -> None:
        return


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Smoke test SmartInsure backend SSE flows.")
    parser.add_argument(
        "--base-url",
        default=os.environ.get("BACKEND_URL", "http://127.0.0.1:34567"),
        help="Backend base URL when not starting a temporary server.",
    )
    parser.add_argument(
        "--start-server",
        action="store_true",
        help="Start a temporary `go run ./cmd/server` process on a free local port.",
    )
    parser.add_argument("--timeout", type=float, default=30.0, help="HTTP timeout seconds.")
    return parser.parse_args()


def free_port() -> int:
    with contextlib.closing(socket.socket(socket.AF_INET, socket.SOCK_STREAM)) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def start_mock_product_server() -> tuple[http.server.ThreadingHTTPServer, str]:
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), MockProductHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    port = int(server.server_address[1])
    return server, f"http://127.0.0.1:{port}/product/mock"


def start_backend(timeout: float) -> tuple[subprocess.Popen[str], str, list[str]]:
    port = free_port()
    base_url = f"http://127.0.0.1:{port}"
    env = os.environ.copy()
    env["HTTP_ADDR"] = f"127.0.0.1:{port}"
    env["LLM_TIMEOUT"] = "2"
    env["LLM_MAX_RETRIES"] = "0"
    for key in (
        "LLM_API_KEY",
        "MINIMAX_API_KEY",
        "OPENAI_API_KEY",
        "DEEPSEEK_API_KEY",
        "QWEN_API_KEY",
        "ZHIPU_API_KEY",
        "MOONSHOT_API_KEY",
        "ANTHROPIC_API_KEY",
    ):
        env.pop(key, None)

    proc = subprocess.Popen(
        ["go", "run", "./cmd/server"],
        cwd=str(ROOT),
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        preexec_fn=os.setsid if hasattr(os, "setsid") else None,
    )
    logs: list[str] = []

    def collect_logs() -> None:
        assert proc.stdout is not None
        for line in proc.stdout:
            logs.append(line.rstrip())

    threading.Thread(target=collect_logs, daemon=True).start()
    wait_for_health(base_url, timeout, logs, proc)
    return proc, base_url, logs


def stop_backend(proc: subprocess.Popen[str] | None) -> None:
    if proc is None or proc.poll() is not None:
        return
    try:
        if hasattr(os, "killpg"):
            os.killpg(proc.pid, signal.SIGTERM)
        else:
            proc.terminate()
        proc.wait(timeout=5)
    except Exception:
        if hasattr(os, "killpg"):
            os.killpg(proc.pid, signal.SIGKILL)
        else:
            proc.kill()


def wait_for_health(base_url: str, timeout: float, logs: list[str], proc: subprocess.Popen[str]) -> None:
    deadline = time.time() + timeout
    last_error: Exception | None = None
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError("backend process exited early:\n" + "\n".join(logs[-20:]))
        try:
            payload = get_json(f"{base_url}/api/healthz", timeout=1.0)
            if payload.get("status") == "ok":
                return
        except Exception as exc:  # noqa: BLE001
            last_error = exc
            time.sleep(0.2)
    raise RuntimeError(f"backend health check timed out: {last_error}\n" + "\n".join(logs[-20:]))


def get_json(url: str, timeout: float) -> dict[str, Any]:
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def post_sse(base_url: str, payload: dict[str, Any], timeout: float) -> list[dict[str, Any]]:
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(
        f"{base_url}/api/chat",
        data=data,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    events: list[dict[str, Any]] = []
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        event_name = "message"
        data_lines: list[str] = []
        for raw_line in resp:
            line = raw_line.decode("utf-8").rstrip("\n")
            if line.startswith("event:"):
                event_name = line.split(":", 1)[1].strip()
                continue
            if line.startswith("data:"):
                data_lines.append(line.split(":", 1)[1].strip())
                continue
            if line.strip() != "":
                continue
            if not data_lines:
                event_name = "message"
                continue
            raw_data = "\n".join(data_lines)
            try:
                parsed: Any = json.loads(raw_data)
            except json.JSONDecodeError:
                parsed = raw_data
            events.append({"event": event_name, "data": parsed})
            if event_name == "done":
                break
            event_name = "message"
            data_lines = []
    return events


def require_events(label: str, events: list[dict[str, Any]], required: list[str]) -> None:
    names = [item["event"] for item in events]
    errors = [item for item in events if item["event"] == "error"]
    if errors:
        raise AssertionError(f"{label}: unexpected error events: {errors}")
    missing = [name for name in required if name not in names]
    if missing:
        raise AssertionError(f"{label}: missing events {missing}; got {names}")
    print(f"[PASS] {label}: events={names}")


def run_smoke(base_url: str, product_url: str, timeout: float) -> None:
    health = get_json(f"{base_url}/api/healthz", timeout=timeout)
    if health.get("status") != "ok":
        raise AssertionError(f"healthz returned unexpected payload: {health}")
    print("[PASS] healthz")

    suggestions = get_json(f"{base_url}/api/suggestions", timeout=timeout)
    if not suggestions.get("suggestions"):
        raise AssertionError(f"suggestions returned empty payload: {suggestions}")
    print("[PASS] suggestions")

    knowledge_events = post_sse(
        base_url,
        {
            "requestId": "smoke-knowledge",
            "message": "百万医疗险的免赔额是什么意思？",
        },
        timeout,
    )
    require_events("knowledge chat", knowledge_events, ["status", "delta", "done"])

    detail_events = post_sse(
        base_url,
        {
            "requestId": "smoke-detail",
            "action": "product_detail",
            "productUrl": product_url,
            "productName": "烟测百万医疗险",
        },
        timeout,
    )
    require_events("product detail", detail_events, ["status", "detail_items", "delta", "done"])

    followup_events = post_sse(
        base_url,
        {
            "requestId": "smoke-followup",
            "action": "product_followup",
            "productUrl": product_url,
            "message": "外购药能报吗？",
        },
        timeout,
    )
    require_events("product followup", followup_events, ["status", "delta", "done"])


def main() -> int:
    args = parse_args()
    backend_proc: subprocess.Popen[str] | None = None
    mock_server: http.server.ThreadingHTTPServer | None = None
    try:
        mock_server, product_url = start_mock_product_server()
        base_url = args.base_url.rstrip("/")
        if args.start_server:
            backend_proc, base_url, _ = start_backend(args.timeout)
            print(f"[INFO] started backend: {base_url}")
        else:
            print(f"[INFO] using backend: {base_url}")
        print(f"[INFO] mock product page: {product_url}")
        run_smoke(base_url, product_url, args.timeout)
        print("[PASS] smoke user flow completed")
        return 0
    except urllib.error.URLError as exc:
        print(f"[FAIL] HTTP error: {exc}", file=sys.stderr)
        if not args.start_server:
            print("Hint: run with --start-server to launch a temporary local backend.", file=sys.stderr)
        return 1
    except Exception as exc:  # noqa: BLE001
        print(f"[FAIL] {exc}", file=sys.stderr)
        return 1
    finally:
        if mock_server is not None:
            mock_server.shutdown()
            mock_server.server_close()
        stop_backend(backend_proc)


if __name__ == "__main__":
    raise SystemExit(main())
