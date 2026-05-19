from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path
from typing import Any, Sequence

from models import BrowserOptions, TaskRequest
from service import scrape_xxx

RUN_MODE_DEFAULTS = {
    "mode1": {
        "headless": True,
        "rating_only": False,
        "extract_real_price": True,
        "extract_product_info": True,
    },
    "mode2": {
        "headless": False,
        "rating_only": True,
        "extract_real_price": False,
        "extract_product_info": True,
    },
}

BASE_RUN_OPTIONS = {
    "headless": False,
    "rating_only": False,
    "extract_real_price": True,
    "extract_product_info": True,
}

LOG_LEVELS = ("quiet", "normal", "verbose")


class Parser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise ValueError(message)


def build_parser() -> Parser:
    parser = Parser(description="Scrape Trip score and price data from one or more product URLs.")
    parser.add_argument("--gui", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--input", action="append", default=[], help="Product URL. Repeat for multiple URLs.")
    parser.add_argument("--urls-file", help="Text file containing one product URL per line.")
    parser.add_argument("--excel-input", help="Excel workbook. First row contains vendors; links start from row 2.")
    parser.add_argument("--output", help="Excel output path.")
    parser.add_argument("--output-dir", help="Directory for vendor Excel outputs.")
    parser.add_argument("--workers", type=int, help="Concurrent browser workers, 1-5. Defaults to min(vendor count, 5).")
    parser.add_argument("--startup-stagger", type=float, default=0.5, help="Seconds to wait between starting vendor browser workers. Use 0 to disable staggering.")
    parser.add_argument("--stop-grace-seconds", type=int, default=30, help="Seconds to wait for partial exports after SIGINT/SIGTERM before terminating workers.")
    parser.add_argument("--run-mode", choices=("mode1", "mode2"), help="Preset run mode. mode1=headless full scrape; mode2=headed rating-only scrape.")
    parser.add_argument("--browser", choices=("chrome",), default="chrome")
    parser.add_argument("--headless", dest="headless", action="store_true", default=None, help="Run Chrome in headless mode.")
    parser.add_argument("--no-headless", dest="headless", action="store_false", help="Run Chrome with a visible browser window.")
    parser.add_argument("--session-file", help="Optional session file path.")
    parser.add_argument("--log-file", help="Optional log file path.")
    parser.add_argument("--log-level", choices=LOG_LEVELS, default="normal", help="Terminal log verbosity: quiet, normal, or verbose.")
    parser.add_argument("--json-lines", action="store_true", help="Emit machine-readable JSON events, one event per line.")
    parser.add_argument("--no-color", action="store_true", help="Disable ANSI color in terminal output.")
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument("--browser-memory-limit-mb", type=int, default=8192, help="Restart Chrome after managed browser RSS exceeds this many MB.")
    parser.add_argument("--browser-restart-every", type=int, default=20, help="Restart Chrome after this many processed URLs.")
    parser.add_argument("--disable-browser-memory-guard", action="store_true", help="Disable Chrome memory guard and automatic browser restarts.")
    parser.add_argument("--rating-only", dest="rating_only", action="store_true", default=None, help="Only scrape rating-related fields.")
    parser.add_argument("--no-rating-only", dest="rating_only", action="store_false", help="Scrape SKU and price fields as well as rating fields.")
    parser.add_argument("--real-price", dest="extract_real_price", action="store_true", default=None, help="Scrape dynamic real-price fields.")
    parser.add_argument("--no-real-price", dest="extract_real_price", action="store_false", help="Skip dynamic real-price extraction.")
    parser.add_argument("--product-info", dest="extract_product_info", action="store_true", default=None, help="Scrape product information fields.")
    parser.add_argument("--no-product-info", dest="extract_product_info", action="store_false", help="Skip product info extraction.")
    return parser


def resolve_run_options(args: argparse.Namespace) -> dict[str, bool]:
    options = BASE_RUN_OPTIONS.copy()
    if args.run_mode:
        options.update(RUN_MODE_DEFAULTS[args.run_mode])
    for name in ("headless", "rating_only", "extract_real_price", "extract_product_info"):
        value = getattr(args, name)
        if value is not None:
            options[name] = value
    return options


class CliRenderer:
    def __init__(
        self,
        log_file: str | None,
        log_level: str = "normal",
        json_lines: bool = False,
        no_color: bool = False,
    ) -> None:
        self.log_path = Path(log_file).expanduser() if log_file else None
        self.log_level = log_level
        self.json_lines = json_lines
        self.no_color = no_color
        self._last_progress_signature: tuple[Any, ...] | None = None
        self._seen_error_reasons: set[str] = set()

    def emit(self, event: dict[str, Any] | str) -> None:
        normalized = self._normalize(event)
        if not self.log_path and normalized.get("log_file"):
            self.log_path = Path(str(normalized["log_file"])).expanduser()
        self._write_log(normalized)
        if self.json_lines:
            self._write_json(normalized)
            return
        self._write_human(normalized)

    def _normalize(self, event: dict[str, Any] | str) -> dict[str, Any]:
        if isinstance(event, dict):
            normalized = dict(event)
        else:
            normalized = {"event": "log", "message": str(event)}
        normalized.setdefault("event", "log")
        normalized.setdefault("ts", time.strftime("%Y-%m-%dT%H:%M:%S%z"))
        return normalized

    def _write_log(self, event: dict[str, Any]) -> None:
        if not self.log_path:
            return
        self.log_path.parent.mkdir(parents=True, exist_ok=True)
        with self.log_path.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(event, ensure_ascii=False, default=str))
            handle.write("\n")
            traceback_text = event.get("traceback")
            if traceback_text:
                handle.write(str(traceback_text).rstrip())
                handle.write("\n")

    def _write_json(self, event: dict[str, Any]) -> None:
        print(json.dumps(event, ensure_ascii=False, default=str), flush=True)

    def _write_human(self, event: dict[str, Any]) -> None:
        name = event.get("event")
        if self.log_level == "quiet" and name not in {"run_started", "run_done", "error"}:
            return
        if name == "run_started":
            self._out(
                "run started "
                f"vendors={event.get('vendor_count', 1)} "
                f"urls={event.get('url_count', 0)} "
                f"workers={event.get('workers', 1)} "
                f"headless={event.get('headless')} "
                f"rating_only={event.get('rating_only')} "
                f"real_price={event.get('extract_real_price')} "
                f"product_info={event.get('extract_product_info')} "
                f"mode={event.get('run_mode') or 'default'} "
                f"output={event.get('output') or event.get('output_dir') or 'auto'}"
            )
        elif name == "browser_prepare":
            if self.log_level in {"normal", "verbose"}:
                self._out(self._prefix_vendor(event, event.get("message", "browser prepare")))
        elif name == "vendor_started":
            if self.log_level in {"normal", "verbose"}:
                self._out(f"[{event.get('vendor')}] started urls={event.get('url_count', 0)}")
        elif name == "driver_prepare_started":
            if self.log_level in {"normal", "verbose"}:
                self._out("driver prepare started")
        elif name == "driver_prepare_done":
            if self.log_level in {"normal", "verbose"}:
                reused = "reused" if event.get("driver_reused") else "prepared"
                elapsed = (event.get("elapsed_ms") or 0) / 1000
                self._out(
                    f"driver {reused} elapsed={elapsed:.1f}s "
                    f"chrome={event.get('chrome_version_main') or 'unknown'} "
                    f"path={event.get('driver_executable_path')}"
                )
        elif name in {"driver_prepare_failed", "startup_preflight_failed"}:
            self._err(
                f"{name.replace('_', ' ')} "
                f"stage={event.get('stage')} "
                f"error_type={event.get('error_type')} "
                f"error={self._quote(event.get('error'))}"
                + (f" suggestion={self._quote(event.get('suggestion'))}" if event.get("suggestion") else "")
                + (f" log={event.get('log_file') or self.log_path}" if event.get("log_file") or self.log_path else "")
            )
        elif name in {"browser_start_failed", "chrome_detect_failed"}:
            self._err(
                f"[{event.get('vendor')}] "
                f"{'browser start failed' if name == 'browser_start_failed' else 'chrome detect failed'} "
                f"stage={event.get('stage') or ('browser_start' if name == 'browser_start_failed' else 'chrome_detect')} "
                f"error={self._quote(event.get('error'))}"
                + (f" log={event.get('log_file') or self.log_path}" if event.get("log_file") or self.log_path else "")
            )
        elif name == "worker_failed":
            self._err(
                f"[{event.get('vendor')}] worker failed "
                f"stage={event.get('stage') or 'worker'} "
                f"exitcode={event.get('exitcode')} "
                f"error={self._quote(event.get('error'))}"
                + (f" log={event.get('log_file') or self.log_path}" if event.get("log_file") or self.log_path else "")
            )
        elif name in {"worker_spawn_started", "worker_ready", "process_started", "runtime_init_started", "chrome_detect_started", "chrome_detect_done", "browser_start_started", "browser_start_done"}:
            if self.log_level in {"normal", "verbose"}:
                message = event.get("message") or name
                self._out(self._prefix_vendor(event, message))
        elif name == "url_started":
            if self.log_level in {"normal", "verbose"}:
                self._out(
                    f"[{event.get('vendor')}] url started "
                    f"{event.get('url_index')}/{event.get('url_total')} "
                    f"url={event.get('url')}"
                )
        elif name == "url_done":
            if self.log_level in {"normal", "verbose"}:
                elapsed = (event.get("elapsed_ms") or 0) / 1000
                self._out(
                    f"[{event.get('vendor')}] url done "
                    f"{event.get('url_index')}/{event.get('url_total')} "
                    f"rows={event.get('rows', 0)} "
                    f"elapsed={elapsed:.1f}s "
                    f"url={event.get('url')}"
                )
        elif name == "url_failed":
            if self.log_level in {"normal", "verbose"}:
                elapsed = (event.get("elapsed_ms") or 0) / 1000
                self._err(
                    f"[{event.get('vendor')}] url failed "
                    f"{event.get('url_index')}/{event.get('url_total')} "
                    f"reason={self._quote(event.get('error'))} "
                    f"elapsed={elapsed:.1f}s "
                    f"url={event.get('url')}"
                )
        elif name == "progress":
            if self.log_level in {"normal", "verbose"}:
                signature = (
                    event.get("vendors_done"),
                    event.get("vendor_count"),
                    event.get("urls_done"),
                    event.get("url_count"),
                    tuple(event.get("active") or []),
                    event.get("failed"),
                )
                if signature != self._last_progress_signature:
                    self._last_progress_signature = signature
                    active = ",".join(event.get("active") or [])
                    self._out(
                        "progress "
                        f"urls={event.get('urls_done', 0)}/{event.get('url_count', 0)} "
                        f"vendors={event.get('vendors_done', 0)}/{event.get('vendor_count', 0)} "
                        f"active={active or '-'} "
                        f"failed={event.get('failed', 0)}"
                    )
        elif name == "vendor_done":
            if event.get("stopped") and int(event.get("rows") or 0) > 0 and event.get("output_path"):
                return
            status = "partial export" if event.get("stopped") and event.get("output_path") else ("done" if event.get("success", True) else "failed")
            message = (
                f"[{event.get('vendor')}] {status} "
                f"urls={event.get('urls_done', event.get('url_count', 0))}/{event.get('url_count', 0)} "
                f"rows={event.get('rows', 0)}"
            )
            if event.get("output_path"):
                message += f" output={event.get('output_path')}"
            if not event.get("success", True):
                message += f" reason={self._quote(event.get('error'))}"
                if self.log_path:
                    message += f" log={self.log_path}"
                self._err(message)
            else:
                self._out(message)
        elif name == "stop_requested":
            self._err(
                "stop requested; waiting for partial exports "
                f"grace={event.get('grace_seconds', 0)}s"
            )
        elif name == "force_stop_requested":
            self._err("force stop requested; terminating remaining browser work")
        elif name == "partial_export_done":
            self._out(
                f"[{event.get('vendor')}] partial export "
                f"rows={event.get('rows', 0)} output={event.get('output_path')}"
            )
        elif name == "warning":
            if self.log_level in {"normal", "verbose"}:
                self._out(f"warning: {event.get('message')}")
        elif name == "error":
            self._err(f"failed: {event.get('message') or event.get('error')}")
        elif name == "run_done":
            self._render_run_done(event)
        elif name == "log":
            if self.log_level == "verbose":
                self._out(self._prefix_vendor(event, event.get("message", "")))

    def _render_run_done(self, event: dict[str, Any]) -> None:
        success = bool(event.get("success"))
        output_paths = event.get("output_paths") or []
        output_path = event.get("output_path")
        processed = event.get("processed_count", 0)
        if success:
            if output_paths:
                self._out(f"captured {processed} rows -> {len(output_paths)} files")
                for path in output_paths:
                    self._out(f"  {path}")
            else:
                self._out(f"captured {processed} rows -> {output_path}")
            return
        if output_paths:
            self._out(f"captured {processed} rows before failure -> {len(output_paths)} files")
            for path in output_paths:
                self._out(f"  {path}")
        elif output_path and processed:
            self._out(f"captured {processed} rows before failure -> {output_path}")
        suffix = ""
        if event.get("log_file"):
            suffix = f"; log={event.get('log_file')}"
        elif self.log_path:
            suffix = f"; log={self.log_path}"
        self._err(f"failed: {event.get('error')}{suffix}")

    def _prefix_vendor(self, event: dict[str, Any], message: Any) -> str:
        vendor = event.get("vendor")
        if vendor:
            return f"[{vendor}] {message}"
        return str(message)

    def _quote(self, value: Any) -> str:
        return json.dumps(str(value or ""), ensure_ascii=False)

    def _err_once(self, reason: str, message: str) -> None:
        if reason in self._seen_error_reasons:
            return
        self._seen_error_reasons.add(reason)
        self._err(message)

    def _out(self, message: str) -> None:
        print(message, flush=True)

    def _err(self, message: str) -> None:
        print(message, file=sys.stderr, flush=True)


def _absolute_paths(paths: list[str]) -> list[str]:
    return [str(Path(path).expanduser().resolve()) for path in paths]


def run_cli(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    try:
        args = parser.parse_args(list(argv) if argv is not None else None)
    except ValueError as exc:
        print(f"argument error: {exc}", file=sys.stderr)
        return 2

    run_options = resolve_run_options(args)
    renderer = CliRenderer(
        log_file=args.log_file,
        log_level=args.log_level,
        json_lines=args.json_lines,
        no_color=args.no_color,
    )

    request = TaskRequest(
        input_paths=args.input,
        urls_file=args.urls_file,
        excel_input=args.excel_input,
        output_path=args.output,
        output_dir=args.output_dir,
        log_file=args.log_file,
        workers=args.workers,
        startup_stagger=max(args.startup_stagger, 0),
        stop_grace_seconds=max(args.stop_grace_seconds, 0),
        rating_only=run_options["rating_only"],
        extract_real_price=run_options["extract_real_price"],
        extract_product_info=run_options["extract_product_info"],
        browser_options=BrowserOptions(
            browser=args.browser,
            headless=run_options["headless"],
            session_file=args.session_file,
            timeout=args.timeout,
            memory_guard_enabled=not args.disable_browser_memory_guard,
            memory_limit_mb=args.browser_memory_limit_mb,
            restart_every=args.browser_restart_every,
        ),
    )
    result = scrape_xxx(request, renderer.emit, run_mode=args.run_mode)
    renderer.emit(
        {
            "event": "run_done",
            "success": result.success,
            "exit_code": result.exit_code,
            "processed_count": result.processed_count,
            "output_path": str(Path(result.output_path).expanduser().resolve()) if result.output_path else None,
            "output_paths": _absolute_paths(result.output_paths),
            "failed_vendors": result.failed_vendors,
            "log_file": str(Path(result.log_file).expanduser().resolve()) if result.log_file else None,
            "error": result.error,
        }
    )
    return result.exit_code
