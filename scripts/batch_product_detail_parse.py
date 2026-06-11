#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Batch-trigger product detail AI parsing through the backend SSE API.

The script intentionally calls the same HTTP product_detail flow as the
frontend. It does not write databases directly; successful requests let the
backend persist product_details and asynchronously enqueue RAG ingestion.

Input formats:
  CSV with headers: product_url/url/productUrl, optional product_name/name/productName
  JSONL objects: {"product_url": "...", "product_name": "..."}
  TXT lines: URL or "URL<TAB>Product name"
"""

from __future__ import annotations

import argparse
import concurrent.futures
import csv
import dataclasses
import hashlib
import json
import pathlib
import sys
import time
import urllib.error
import urllib.request
from typing import Any


@dataclasses.dataclass(frozen=True)
class ProductInput:
    index: int
    product_url: str
    product_name: str = ""


@dataclasses.dataclass
class ParseResult:
    index: int
    product_url: str
    product_name: str
    request_id: str
    ok: bool
    detail_items_seen: bool
    done_seen: bool
    error: str
    event_counts: dict[str, int]
    duration_ms: int


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Batch trigger SmartInsure product_detail AI parsing.")
    parser.add_argument("--input-file", required=True, help="CSV/JSONL/TXT product list.")
    parser.add_argument("--base-url", default="http://127.0.0.1:34567", help="Backend base URL.")
    parser.add_argument(
        "--endpoint",
        default="/api/agent/deep-chat",
        choices=["/api/chat", "/api/agent/chat", "/api/agent/deep-chat"],
        help="Backend SSE endpoint. Default matches the current frontend DeepAgent detail path.",
    )
    parser.add_argument("--timeout", type=float, default=180.0, help="Per request socket timeout seconds.")
    parser.add_argument("--concurrency", type=int, default=1, help="Concurrent requests. Keep low for LLM safety.")
    parser.add_argument("--limit", type=int, default=0, help="Only process at most N products after offset.")
    parser.add_argument("--offset", type=int, default=0, help="Skip first N loaded products.")
    parser.add_argument("--delay", type=float, default=0.0, help="Delay seconds between task submissions.")
    parser.add_argument("--retries", type=int, default=0, help="Retry times per product on failure.")
    parser.add_argument("--request-prefix", default="batch-product-detail", help="Request id prefix.")
    parser.add_argument("--anonymous-id", default="", help="Optional anonymous_id; omit to run stateless.")
    parser.add_argument("--message", default="", help="Optional product detail question passed as message.")
    parser.add_argument("--output-jsonl", default="", help="Optional result JSONL path.")
    parser.add_argument(
        "--resume-output",
        action="store_true",
        help="Append to output JSONL and skip request ids already recorded in it.",
    )
    parser.add_argument("--dry-run", action="store_true", help="Only print loaded products.")
    parser.add_argument(
        "--wait-after",
        type=float,
        default=2.0,
        help="Sleep seconds after the batch so async RAG workers can finish before log inspection.",
    )
    return parser.parse_args()


def load_products(path: str) -> list[ProductInput]:
    input_path = pathlib.Path(path)
    if not input_path.exists():
        raise FileNotFoundError(path)
    suffix = input_path.suffix.lower()
    if suffix == ".jsonl":
        return load_jsonl(input_path)
    if suffix == ".csv":
        return load_csv(input_path)
    return load_txt(input_path)


def load_jsonl(path: pathlib.Path) -> list[ProductInput]:
    products: list[ProductInput] = []
    with path.open("r", encoding="utf-8") as fh:
        for line_no, line in enumerate(fh, start=1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            payload = json.loads(line)
            product_url = first_non_empty(payload, "product_url", "productUrl", "url")
            product_name = first_non_empty(payload, "product_name", "productName", "name", "title")
            if product_url:
                products.append(ProductInput(line_no, product_url, product_name))
    return products


def load_csv(path: pathlib.Path) -> list[ProductInput]:
    products: list[ProductInput] = []
    with path.open("r", encoding="utf-8-sig", newline="") as fh:
        reader = csv.DictReader(fh)
        for row_no, row in enumerate(reader, start=2):
            product_url = first_non_empty(row, "product_url", "productUrl", "url")
            product_name = first_non_empty(row, "product_name", "productName", "name", "title")
            if product_url:
                products.append(ProductInput(row_no, product_url, product_name))
    return products


def load_txt(path: pathlib.Path) -> list[ProductInput]:
    products: list[ProductInput] = []
    with path.open("r", encoding="utf-8") as fh:
        for line_no, line in enumerate(fh, start=1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "\t" in line:
                product_url, product_name = line.split("\t", 1)
            else:
                product_url, product_name = line, ""
            product_url = product_url.strip()
            product_name = product_name.strip()
            if product_url:
                products.append(ProductInput(line_no, product_url, product_name))
    return products


def first_non_empty(row: dict[str, Any], *keys: str) -> str:
    for key in keys:
        value = row.get(key)
        if value is None:
            continue
        value = str(value).strip()
        if value:
            return value
    return ""


def request_id(prefix: str, item: ProductInput) -> str:
    digest = hashlib.sha1(item.product_url.encode("utf-8")).hexdigest()[:10]
    return f"{prefix}-{item.index}-{digest}"


def trigger_product_detail(args: argparse.Namespace, item: ProductInput) -> ParseResult:
    rid = request_id(args.request_prefix, item)
    started_at = time.time()
    event_counts: dict[str, int] = {}
    detail_items_seen = False
    done_seen = False
    error = ""

    payload: dict[str, Any] = {
        "requestId": rid,
        "action": "product_detail",
        "productUrl": item.product_url,
    }
    if item.product_name:
        payload["productName"] = item.product_name
    if args.message:
        payload["message"] = args.message
    if args.anonymous_id:
        payload["anonymous_id"] = args.anonymous_id

    url = args.base_url.rstrip("/") + args.endpoint
    attempt = 0
    while attempt <= args.retries:
        try:
            for event_name, data in post_sse(url, payload, args.timeout):
                event_counts[event_name] = event_counts.get(event_name, 0) + 1
                if event_name == "detail_items":
                    detail_items_seen = True
                if event_name == "error":
                    error = compact_error(data)
                if event_name == "done":
                    done_seen = True
                    break
            break
        except Exception as exc:  # noqa: BLE001
            error = str(exc)
            attempt += 1
            if attempt <= args.retries:
                time.sleep(min(2**attempt, 10))

    duration_ms = int((time.time() - started_at) * 1000)
    ok = detail_items_seen and done_seen and not error
    return ParseResult(
        index=item.index,
        product_url=item.product_url,
        product_name=item.product_name,
        request_id=rid,
        ok=ok,
        detail_items_seen=detail_items_seen,
        done_seen=done_seen,
        error=error,
        event_counts=event_counts,
        duration_ms=duration_ms,
    )


def post_sse(url: str, payload: dict[str, Any], timeout: float):
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        method="POST",
        headers={"Content-Type": "application/json", "Accept": "text/event-stream"},
    )
    try:
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
                yield event_name, parsed
                event_name = "message"
                data_lines = []
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code}: {body}") from exc


def compact_error(data: Any) -> str:
    if isinstance(data, dict):
        err = data.get("error")
        if isinstance(err, dict):
            return str(err.get("message") or err.get("code") or err)
        return str(data.get("message") or data)
    return str(data)


def result_to_dict(result: ParseResult) -> dict[str, Any]:
    return dataclasses.asdict(result)


def load_recorded_request_ids(path: str) -> set[str]:
    if not path:
        return set()
    output_path = pathlib.Path(path)
    if not output_path.exists():
        return set()
    recorded: set[str] = set()
    with output_path.open("r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                payload = json.loads(line)
            except json.JSONDecodeError:
                continue
            rid = str(payload.get("request_id") or "").strip()
            if rid:
                recorded.add(rid)
    return recorded


def print_result(result: ParseResult) -> None:
    status = "OK" if result.ok else "FAIL"
    details = (
        f"[{status}] line={result.index} request_id={result.request_id} "
        f"detail_items={result.detail_items_seen} done={result.done_seen} "
        f"duration_ms={result.duration_ms} url={result.product_url}"
    )
    if result.product_name:
        details += f" name={result.product_name}"
    if result.error:
        details += f" error={result.error}"
    print(details, flush=True)


def main() -> int:
    args = parse_args()
    products = load_products(args.input_file)
    if args.offset > 0:
        products = products[args.offset :]
    if args.limit > 0:
        products = products[: args.limit]
    recorded_ids = load_recorded_request_ids(args.output_jsonl) if args.resume_output else set()
    if recorded_ids:
        products = [item for item in products if request_id(args.request_prefix, item) not in recorded_ids]
    if not products:
        print("No products loaded.", file=sys.stderr)
        return 2

    print(f"Loaded products: {len(products)}", flush=True)
    if recorded_ids:
        print(f"Skipped recorded results: {len(recorded_ids)}", flush=True)
    if args.dry_run:
        for item in products:
            print(f"line={item.index} url={item.product_url} name={item.product_name}")
        return 0

    output_fh = None
    if args.output_jsonl:
        output_path = pathlib.Path(args.output_jsonl)
        output_path.parent.mkdir(parents=True, exist_ok=True)
        mode = "a" if args.resume_output else "w"
        output_fh = output_path.open(mode, encoding="utf-8")

    success = 0
    failed = 0
    try:
        max_workers = max(1, args.concurrency)
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as executor:
            pending: set[concurrent.futures.Future[ParseResult]] = set()
            next_index = 0

            def submit_next() -> None:
                nonlocal next_index
                if next_index >= len(products):
                    return
                pending.add(executor.submit(trigger_product_detail, args, products[next_index]))
                next_index += 1
                if args.delay > 0:
                    time.sleep(args.delay)

            while next_index < len(products) and len(pending) < max_workers:
                submit_next()

            while pending:
                done, pending = concurrent.futures.wait(pending, return_when=concurrent.futures.FIRST_COMPLETED)
                for future in done:
                    result = future.result()
                    if result.ok:
                        success += 1
                    else:
                        failed += 1
                    print_result(result)
                    if output_fh is not None:
                        output_fh.write(json.dumps(result_to_dict(result), ensure_ascii=False) + "\n")
                        output_fh.flush()
                while next_index < len(products) and len(pending) < max_workers:
                    submit_next()
    finally:
        if output_fh is not None:
            output_fh.close()

    if args.wait_after > 0:
        time.sleep(args.wait_after)
    print(f"done: total={len(products)} success={success} failed={failed}")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
