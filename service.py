from __future__ import annotations

import contextlib
import multiprocessing
import os
import platform
import queue
import re
import shutil
import signal
import sys
import tempfile
import time
import traceback
from datetime import datetime
from pathlib import Path
from typing import Any, Callable

import openpyxl

from browser_startup import BrowserStartupError, BrowserStartupInfo, prepare_chromedriver
from models import ScrapeItem, TaskRequest, TaskResult
from trip_cli_scraper import TripCliScraper
from xlsx_export import export_rows

Reporter = Callable[[dict[str, Any] | str], None]
DEFAULT_VENDOR = "手动输入"
MAX_WORKERS = 5


def load_inputs(request: TaskRequest) -> list[str]:
    return [item.url for item in load_items(request)]


def load_items(request: TaskRequest) -> list[ScrapeItem]:
    items: list[ScrapeItem] = []
    order = 0
    for url in request.input_paths:
        if url.strip():
            items.append(ScrapeItem(url=url.strip(), vendor=DEFAULT_VENDOR, input_order=order))
            order += 1
    if request.urls_file:
        lines = Path(request.urls_file).expanduser().read_text(encoding="utf-8").splitlines()
        for row_number, line in enumerate(lines, 1):
            url = line.strip()
            if url:
                items.append(ScrapeItem(url=url, vendor=DEFAULT_VENDOR, source_row=row_number, input_order=order))
                order += 1
    if request.excel_input:
        excel_items, _warnings = load_excel_items(request.excel_input, start_order=order)
        items.extend(excel_items)
    if not items:
        raise ValueError("At least one URL is required via --input, --urls-file, or --excel-input.")
    return items


def load_excel_items(path: str, start_order: int = 0) -> tuple[list[ScrapeItem], list[str]]:
    workbook_path = Path(path).expanduser()
    workbook = openpyxl.load_workbook(workbook_path, read_only=True, data_only=True)
    worksheet = workbook.active
    items: list[ScrapeItem] = []
    warnings: list[str] = []
    order = start_order
    try:
        max_column = worksheet.max_column or 0
        max_row = worksheet.max_row or 0
        for column in range(1, max_column + 1):
            header_value = worksheet.cell(row=1, column=column).value
            vendor = str(header_value).strip() if header_value is not None else ""
            if not vendor:
                continue
            found = 0
            for row in range(2, max_row + 1):
                cell_value = worksheet.cell(row=row, column=column).value
                if cell_value is None:
                    continue
                url = str(cell_value).strip()
                if not url:
                    continue
                lowered = url.lower()
                if "http" in lowered and "trip.com" in lowered:
                    items.append(
                        ScrapeItem(
                            url=url,
                            vendor=vendor,
                            source_column=column,
                            source_row=row,
                            input_order=order,
                        )
                    )
                    order += 1
                    found += 1
                else:
                    warnings.append(f"ignored non-Trip link at column {column}, row {row}: {url}")
            if not found:
                warnings.append(f"vendor column {column} ({vendor}) has no Trip links")
    finally:
        workbook.close()
    return items, warnings


def default_output_path(request: TaskRequest) -> str:
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    filename = (
        f"trip_score_results_{timestamp}.xlsx"
        if request.rating_only
        else f"trip_scoreandprice_results_{timestamp}.xlsx"
    )
    return str(Path.home() / "Documents" / filename)


def default_vendor_output_dir(base_dir: Path | None = None, timestamp: str | None = None) -> str:
    timestamp = timestamp or datetime.now().strftime("%y%m%d-%H%M")
    return str((base_dir or (Path.home() / "Documents")) / "trip_vendor_results" / timestamp)


def default_single_log_file(output_path: str, timestamp: str | None = None) -> str:
    timestamp = timestamp or datetime.now().strftime("%y%m%d-%H%M")
    return str(Path(output_path).expanduser().parent / f"trip_cli_{timestamp}.log")


def default_session_path() -> str:
    if platform.system() == "Windows":
        config_dir = Path(os.environ.get("PROGRAMDATA", "C:\\ProgramData")) / "TripDetailFilling"
    elif platform.system() == "Darwin":
        config_dir = Path.home() / "Library" / "Preferences" / "TripDetailFilling"
    else:
        config_dir = Path.home() / ".config" / "TripDetailFilling"
    return str(config_dir / "session_data.json")


def _expand_output_path(path: str) -> str:
    return str(Path(path).expanduser())


def _safe_filename(value: str) -> str:
    cleaned = re.sub(r'[\\/:*?"<>|\r\n\t]+', "_", value).strip(" ._")
    return cleaned or "未命名厂商"


def _vendor_groups(items: list[ScrapeItem]) -> list[dict[str, Any]]:
    groups: list[dict[str, Any]] = []
    seen: dict[str, int] = {}
    by_key: dict[tuple[str, int | None], dict[str, Any]] = {}
    for item in sorted(items, key=lambda current: current.input_order):
        key = (item.vendor or DEFAULT_VENDOR, item.source_column)
        if key not in by_key:
            vendor = item.vendor or DEFAULT_VENDOR
            seen[vendor] = seen.get(vendor, 0) + 1
            suffix = f"_列{item.source_column}" if seen[vendor] > 1 and item.source_column else ""
            file_vendor = f"{vendor}{suffix}" if suffix else vendor
            group = {"vendor": vendor, "file_vendor": file_vendor, "items": []}
            by_key[key] = group
            groups.append(group)
        by_key[key]["items"].append(item)
    return groups


def _resolve_worker_count(requested: int | None, vendor_count: int) -> int:
    if requested is not None and not 1 <= requested <= MAX_WORKERS:
        raise ValueError(f"--workers must be between 1 and {MAX_WORKERS}.")
    return requested or max(1, min(vendor_count, MAX_WORKERS))


def _vendor_output_path(group: dict[str, Any], output_dir: str, timestamp: str) -> str:
    return str(Path(output_dir).expanduser() / f"{_safe_filename(group['file_vendor'])}_{timestamp}.xlsx")


def _copy_session_to_temp(base_session: str, temp_dir: str, index: int) -> str:
    temp_session = Path(temp_dir) / f"session_worker_{index}.json"
    base_path = Path(base_session).expanduser()
    if base_path.exists():
        temp_session.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(base_path, temp_session)
    return str(temp_session)


def _worker_request(request: TaskRequest, output_path: str, session_file: str) -> TaskRequest:
    return TaskRequest(
        input_paths=[],
        urls_file=None,
        excel_input=None,
        output_path=output_path,
        output_dir=None,
        log_file=request.log_file,
        workers=1,
        startup_stagger=request.startup_stagger,
        stop_grace_seconds=request.stop_grace_seconds,
        rating_only=request.rating_only,
        extract_real_price=request.extract_real_price,
        extract_product_info=request.extract_product_info,
        browser_options=type(request.browser_options)(
            browser=request.browser_options.browser,
            headless=request.browser_options.headless,
            session_file=session_file,
            chrome_path=request.browser_options.chrome_path,
            chrome_version_main=request.browser_options.chrome_version_main,
            driver_executable_path=request.browser_options.driver_executable_path,
            timeout=request.browser_options.timeout,
            memory_guard_enabled=request.browser_options.memory_guard_enabled,
            memory_limit_mb=request.browser_options.memory_limit_mb,
            restart_every=request.browser_options.restart_every,
        ),
    )


@contextlib.contextmanager
def _redirect_worker_output(log_file: str | None):
    target = None
    try:
        if log_file:
            log_path = Path(log_file).expanduser()
            log_path.parent.mkdir(parents=True, exist_ok=True)
            target = log_path.open("a", encoding="utf-8")
        else:
            target = open(os.devnull, "w", encoding="utf-8")
        with contextlib.redirect_stdout(target), contextlib.redirect_stderr(target):
            yield
    finally:
        if target:
            target.close()


def _scrape_vendor_worker(
    worker_id: int,
    vendor: str,
    items: list[ScrapeItem],
    request: TaskRequest,
    output_path: str,
    session_file: str,
    result_queue,
    global_total: int,
    stop_event,
) -> None:
    def emit(event: dict[str, Any]) -> None:
        payload = dict(event)
        payload.setdefault("vendor", vendor)
        payload.setdefault("worker_id", worker_id)
        payload.setdefault("output_path", output_path)
        payload.setdefault("log_file", request.log_file)
        result_queue.put(payload)

    scraper = None
    rows: list[dict] = []
    saved_path: str | None = None
    error: str | None = None
    error_type: str | None = None
    error_stage: str | None = None
    error_traceback: str | None = None
    current_stage = "process_started"
    with _redirect_worker_output(request.log_file):
        try:
            emit({"event": "process_started", "stage": "process_started", "message": "worker process started"})
            current_stage = "runtime_init"
            emit({"event": "runtime_init_started", "stage": "runtime_init", "message": "initializing worker runtime"})
            emit({"event": "worker_ready", "message": "worker process ready"})
            scraper = TripCliScraper(request, emit, vendor=vendor, worker_id=worker_id, global_total=global_total)
            current_stage = "chrome_detect"
            scraper.prepare_environment()
            current_stage = "browser_start"
            scraper.start_browser()
            current_stage = "scrape"
            rows = scraper.scrape_items(items, stop_event=stop_event, rows=rows)
        except Exception as exc:
            error = str(exc)
            error_type = type(exc).__name__
            error_stage = current_stage
            error_traceback = traceback.format_exc()
            emit(
                {
                    "event": "worker_failed",
                    "stage": error_stage,
                    "exitcode": None,
                    "error": error,
                    "error_type": error_type,
                    "traceback": error_traceback,
                    "rows": len(rows),
                    "url_count": len(items),
                    "urls_done": len(rows),
                }
            )
        finally:
            stopped = bool(stop_event and stop_event.is_set())
            if rows:
                try:
                    saved_path = export_rows(
                        rows,
                        output_path,
                        rating_only=request.rating_only,
                        extract_real_price=request.extract_real_price,
                        extract_product_info=request.extract_product_info,
                        include_vendor_column=True,
                    )
                    event_name = "partial_export_done" if stopped or error else "export_done"
                    emit(
                        {
                            "event": event_name,
                            "rows": len(rows),
                            "output_path": str(Path(saved_path).expanduser().resolve()),
                        }
                    )
                except Exception as exc:
                    error = f"export failed: {exc}"
                    error_type = type(exc).__name__
                    error_stage = "export"
                    error_traceback = traceback.format_exc()
            result_queue.put(
                {
                    "event": "vendor_done",
                    "vendor": vendor,
                    "worker_id": worker_id,
                    "success": not stopped and error is None,
                    "stopped": stopped,
                    "rows": len(rows),
                    "rows_data": rows,
                    "output_path": saved_path or output_path,
                    "session_file": session_file,
                    "error": "stopped by signal" if stopped and error is None else error,
                    "error_type": error_type,
                    "stage": "stopped" if stopped else (error_stage or "done"),
                    "traceback": error_traceback,
                    "log_file": request.log_file,
                    "url_count": len(items),
                    "urls_done": len(rows),
                }
            )
            if scraper:
                scraper.close()


def _active_summary(states: dict[str, dict[str, Any]]) -> list[str]:
    summary = []
    for vendor, state in states.items():
        current = state.get("current_index") or 0
        total = state.get("total") or 0
        stage = state.get("stage") or "ready"
        summary.append(f"{vendor}:{current}/{total}:{stage}")
    return summary


def _emit_progress(
    reporter: Reporter,
    *,
    states: dict[str, dict[str, Any]],
    completed_groups: int,
    vendor_count: int,
    urls_done: int,
    url_count: int,
    failed: int,
) -> None:
    reporter(
        {
            "event": "progress",
            "vendors_done": completed_groups,
            "vendor_count": vendor_count,
            "urls_done": urls_done,
            "url_count": url_count,
            "active": _active_summary(states),
            "failed": failed,
        }
    )


def _preflight(request: TaskRequest, reporter: Reporter) -> None:
    reporter({"event": "browser_prepare", "message": f"python={sys.executable}"})
    reporter({"event": "browser_prepare", "message": f"platform={platform.platform()}"})
    if request.log_file:
        Path(request.log_file).expanduser().parent.mkdir(parents=True, exist_ok=True)


def _apply_browser_startup_info(request: TaskRequest, info: BrowserStartupInfo) -> None:
    request.browser_options.chrome_path = info.chrome_path
    request.browser_options.chrome_version_main = info.chrome_version_main
    request.browser_options.driver_executable_path = info.driver_executable_path


def _prepare_browser_runtime(request: TaskRequest, reporter: Reporter) -> BrowserStartupInfo:
    reporter({"event": "driver_prepare_started", "stage": "driver_prepare", "message": "preparing chromedriver"})
    try:
        info = prepare_chromedriver()
    except BrowserStartupError as exc:
        event = {
            "event": "driver_prepare_failed" if exc.stage == "driver_prepare" else "startup_preflight_failed",
            "stage": exc.stage,
            "error_type": exc.error_type,
            "error": str(exc),
            "suggestion": exc.suggestion,
            "traceback": exc.traceback_text,
            "log_file": request.log_file,
        }
        event.update(exc.details)
        reporter(event)
        raise
    _apply_browser_startup_info(request, info)
    reporter(
        {
            "event": "driver_prepare_done",
            "stage": "driver_prepare",
            "message": "chromedriver ready",
            "chrome_path": info.chrome_path,
            "chrome_version_main": info.chrome_version_main,
            "driver_executable_path": info.driver_executable_path,
            "driver_reused": info.driver_reused,
            "cache_dir": info.cache_dir,
            "ssl_cert_file": info.ssl_cert_file,
            "ssl_cert_source": info.ssl_cert_source,
            "elapsed_ms": info.driver_prepare_ms,
            "log_file": request.log_file,
        }
    )
    return info


def scrape_vendor_batches(
    request: TaskRequest,
    reporter: Reporter,
    run_mode: str | None = None,
    output_base_dir: str | None = None,
    should_stop: Callable[[], bool] | None = None,
    progress_callback: Callable[[int, int], None] | None = None,
    rows_callback: Callable[[list[dict]], None] | None = None,
) -> TaskResult:
    multiprocessing.freeze_support()
    try:
        items = load_items(request)
        groups = _vendor_groups(items)
        workers = _resolve_worker_count(request.workers, len(groups))
    except Exception as exc:
        return TaskResult(success=False, error=str(exc), exit_code=2)

    warnings = load_excel_items(request.excel_input)[1] if request.excel_input else []
    for warning in warnings:
        reporter({"event": "warning", "message": warning})

    timestamp = datetime.now().strftime("%y%m%d-%H%M")
    if request.output_dir:
        output_dir = _expand_output_path(request.output_dir)
    elif output_base_dir:
        output_dir = str(Path(output_base_dir).expanduser() / "trip_vendor_results" / timestamp)
    else:
        output_dir = default_vendor_output_dir(timestamp=timestamp)
    Path(output_dir).mkdir(parents=True, exist_ok=True)
    if not request.log_file:
        request.log_file = str(Path(output_dir).expanduser() / "run.log")
    Path(request.log_file).expanduser().parent.mkdir(parents=True, exist_ok=True)

    session_temp_dir: str | None = None
    stop_event = multiprocessing.Event()
    active: list[dict[str, Any]] = []
    stop_requested = {"value": False}
    stop_started_at = {"value": None}
    stop_attempts = {"value": 0}
    force_terminate_sent = {"value": False}
    old_handlers = {}

    def terminate_active_workers() -> None:
        for entry in active:
            process = entry["process"]
            if process.is_alive():
                process.terminate()

    def stop_all(signum, _frame) -> None:
        stop_attempts["value"] += 1
        if stop_attempts["value"] == 1:
            if not stop_requested["value"]:
                stop_started_at["value"] = time.monotonic()
            stop_requested["value"] = True
            stop_event.set()
            reporter(
                {
                    "event": "stop_requested",
                    "message": f"received signal {signum}; graceful stop requested",
                    "grace_seconds": request.stop_grace_seconds,
                    "attempt": 1,
                    "mode": "graceful",
                }
            )
            return
        force_terminate_sent["value"] = True
        reporter(
            {
                "event": "force_stop_requested",
                "message": f"received signal {signum}; forcing remaining workers to terminate",
                "attempt": stop_attempts["value"],
                "mode": "force",
            }
        )
        terminate_active_workers()

    for sig in (signal.SIGINT, signal.SIGTERM):
        old_handlers[sig] = signal.getsignal(sig)
        signal.signal(sig, stop_all)

    total_urls = sum(len(group["items"]) for group in groups)
    reporter(
        {
            "event": "run_started",
            "mode": "excel",
            "run_mode": run_mode,
            "vendor_count": len(groups),
            "url_count": total_urls,
            "workers": workers,
            "headless": request.browser_options.headless,
            "rating_only": request.rating_only,
            "extract_real_price": request.extract_real_price,
            "extract_product_info": request.extract_product_info,
            "output_dir": str(Path(output_dir).expanduser().resolve()),
            "log_file": str(Path(request.log_file).expanduser().resolve()),
        }
    )
    _preflight(request, reporter)
    try:
        _prepare_browser_runtime(request, reporter)
    except BrowserStartupError as exc:
        failed_vendors = [str(group["vendor"]) for group in groups]
        error = str(exc)
        if exc.suggestion:
            error = f"{error}; suggestion: {exc.suggestion}"
        return TaskResult(False, output_dir, [], 0, warnings, failed_vendors, request.log_file, error, 4)
    if stop_requested["value"]:
        return TaskResult(False, output_dir, [], 0, warnings, [], request.log_file, "stopped by signal", 130)

    base_session = str(Path(request.browser_options.session_file or default_session_path()).expanduser())
    session_temp_dir = tempfile.mkdtemp(prefix="trip_vendor_sessions_")
    result_queue = multiprocessing.Queue()
    pending = list(groups)
    active_states: dict[str, dict[str, Any]] = {}
    results: list[dict[str, Any]] = []
    output_paths: list[str] = []
    completed_groups = 0
    urls_done = 0
    failed_urls = 0
    worker_results: dict[int, dict[str, Any]] = {}

    def start_next(index: int, group: dict[str, Any]) -> dict[str, Any]:
        output_path = request.output_path if len(groups) == 1 and request.output_path else _vendor_output_path(group, output_dir, timestamp)
        output_path = _expand_output_path(output_path)
        session_file = _copy_session_to_temp(base_session, session_temp_dir, index)
        worker_request = _worker_request(request, output_path, session_file)
        reporter({"event": "worker_spawn_started", "vendor": group["vendor"], "worker_id": index, "message": "spawning worker"})
        process = multiprocessing.Process(
            target=_scrape_vendor_worker,
            args=(index, group["vendor"], group["items"], worker_request, output_path, session_file, result_queue, total_urls, stop_event),
            daemon=False,
        )
        process.start()
        reporter({"event": "vendor_started", "vendor": group["vendor"], "worker_id": index, "url_count": len(group["items"])})
        active_states[group["vendor"]] = {"total": len(group["items"]), "current_index": 0, "stage": "spawned"}
        if request.startup_stagger > 0:
            time.sleep(request.startup_stagger)
        return {"process": process, "group": group, "output_path": output_path, "session_file": session_file, "worker_id": index, "started_at": time.monotonic()}

    try:
        index = 0
        while pending and len(active) < workers:
            index += 1
            active.append(start_next(index, pending.pop(0)))

        while active:
            if should_stop and should_stop():
                if not stop_requested["value"]:
                    stop_started_at["value"] = time.monotonic()
                    reporter(
                        {
                            "event": "stop_requested",
                            "message": "stop requested",
                            "grace_seconds": request.stop_grace_seconds,
                        }
                    )
                stop_requested["value"] = True
                stop_event.set()
            if stop_requested["value"] and stop_started_at["value"] is not None:
                if (
                    not force_terminate_sent["value"]
                    and request.stop_grace_seconds >= 0
                    and time.monotonic() - stop_started_at["value"] > request.stop_grace_seconds
                ):
                    force_terminate_sent["value"] = True
                    reporter({"event": "warning", "message": "stop grace period exceeded; terminating remaining workers"})
                    terminate_active_workers()

            try:
                message = result_queue.get(timeout=0.5)
            except queue.Empty:
                message = None

            if message:
                event = message.get("event")
                vendor = message.get("vendor")
                if vendor in active_states:
                    if event == "url_started":
                        active_states[vendor]["current_index"] = message.get("url_index")
                        active_states[vendor]["stage"] = "open"
                        message["global_done"] = urls_done
                        message["global_total"] = total_urls
                    elif event == "url_done":
                        active_states[vendor]["current_index"] = message.get("url_index")
                        active_states[vendor]["stage"] = "done"
                        urls_done += 1
                        message["global_done"] = urls_done
                        message["global_total"] = total_urls
                    elif event == "url_failed":
                        active_states[vendor]["current_index"] = message.get("url_index")
                        active_states[vendor]["stage"] = "failed"
                        urls_done += 1
                        failed_urls += 1
                        message["global_done"] = urls_done
                        message["global_total"] = total_urls
                    elif event == "log" and message.get("stage"):
                        active_states[vendor]["stage"] = message.get("stage")
                    elif event in {
                        "worker_ready",
                        "runtime_init_started",
                        "chrome_detect_started",
                        "chrome_detect_done",
                        "chrome_detect_failed",
                        "browser_start_started",
                        "browser_start_done",
                        "browser_start_failed",
                        "worker_failed",
                    }:
                        active_states[vendor]["stage"] = message.get("stage") or event

                if event == "vendor_done":
                    worker_results[message.get("worker_id")] = message
                    results.append(message)
                    completed_groups += 1
                    active_states.pop(vendor, None)
                    if progress_callback:
                        progress_callback(completed_groups, len(groups))
                    if rows_callback and message.get("rows_data"):
                        rows_callback(message["rows_data"])
                    if message.get("output_path") and (message.get("success") or int(message.get("rows") or 0) > 0):
                        output_paths.append(message["output_path"])
                    reporter(message)
                else:
                    reporter(message)

                _emit_progress(
                    reporter,
                    states=active_states,
                    completed_groups=completed_groups,
                    vendor_count=len(groups),
                    urls_done=urls_done,
                    url_count=total_urls,
                    failed=failed_urls + len([result for result in results if not result.get("success")]),
                )

            still_active = []
            for entry in active:
                process = entry["process"]
                if process.is_alive():
                    still_active.append(entry)
                    continue
                process.join(timeout=0)
                vendor = entry["group"]["vendor"]
                has_result = entry["worker_id"] in worker_results
                if not has_result:
                    ended_at = entry.setdefault("ended_at", time.monotonic())
                    if time.monotonic() - ended_at < 1.0:
                        still_active.append(entry)
                        continue
                    exitcode = process.exitcode
                    stage = active_states.get(vendor, {}).get("stage") or "worker_exit"
                    reason = (
                        f"missing vendor_done from worker after exitcode {exitcode}"
                        if exitcode in (0, None)
                        else f"worker exited with code {exitcode}"
                    )
                    worker_failed = {
                        "event": "worker_failed",
                        "vendor": vendor,
                        "worker_id": entry["worker_id"],
                        "stage": stage,
                        "exitcode": exitcode,
                        "success": False,
                        "rows": 0,
                        "output_path": entry["output_path"],
                        "session_file": entry["session_file"],
                        "log_file": request.log_file,
                        "error": reason,
                        "error_type": "MissingVendorDone" if exitcode in (0, None) else "WorkerProcessExited",
                        "url_count": len(entry["group"]["items"]),
                        "urls_done": 0,
                    }
                    reporter(worker_failed)
                    failure = {
                        "event": "vendor_done",
                        "vendor": vendor,
                        "worker_id": entry["worker_id"],
                        "success": False,
                        "rows": 0,
                        "output_path": entry["output_path"],
                        "session_file": entry["session_file"],
                        "log_file": request.log_file,
                        "error": reason,
                        "error_type": worker_failed["error_type"],
                        "stage": stage,
                        "exitcode": exitcode,
                        "url_count": len(entry["group"]["items"]),
                        "urls_done": 0,
                    }
                    worker_results[entry["worker_id"]] = failure
                    results.append(failure)
                    completed_groups += 1
                    active_states.pop(vendor, None)
                    reporter(failure)
                if pending and not stop_requested["value"]:
                    index += 1
                    still_active.append(start_next(index, pending.pop(0)))
            active = still_active

        successful_session = next((result.get("session_file") for result in results if result.get("success") and result.get("session_file")), None)
        if successful_session and Path(successful_session).exists():
            base_path = Path(base_session)
            base_path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(successful_session, base_path)

        rows = sum(int(result.get("rows") or 0) for result in results)
        failures = [result for result in results if not result.get("success")]
        failed_vendors = [str(result.get("vendor")) for result in failures if result.get("vendor")]
        if stop_requested["value"]:
            return TaskResult(False, output_dir, output_paths, rows, warnings, failed_vendors, request.log_file, "stopped by signal", 130)
        if failures:
            error = "; ".join(f"{failure.get('vendor')}: {failure.get('error')}" for failure in failures)
            return TaskResult(False, output_dir, output_paths, rows, warnings, failed_vendors, request.log_file, error, 4)
        if rows == 0 and groups:
            failed_vendors = [str(group["vendor"]) for group in groups]
            error = "no rows captured and no successful worker output"
            return TaskResult(False, output_dir, output_paths, rows, warnings, failed_vendors, request.log_file, error, 4)
        return TaskResult(True, output_dir, output_paths, rows, warnings, [], request.log_file, None, 0)
    finally:
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)
        terminate_active_workers()
        if session_temp_dir:
            shutil.rmtree(session_temp_dir, ignore_errors=True)


def scrape_xxx(request: TaskRequest, reporter: Reporter, run_mode: str | None = None) -> TaskResult:
    if request.excel_input:
        return scrape_vendor_batches(request, reporter, run_mode=run_mode)

    try:
        items = load_items(request)
    except Exception as exc:
        return TaskResult(success=False, error=str(exc), exit_code=2)

    output_path = _expand_output_path(request.output_path or default_output_path(request))
    if not request.log_file:
        request.log_file = default_single_log_file(output_path)
    Path(request.log_file).expanduser().parent.mkdir(parents=True, exist_ok=True)
    if not request.browser_options.session_file:
        request.browser_options.session_file = default_session_path()
    reporter(
        {
            "event": "run_started",
            "mode": "single",
            "run_mode": run_mode,
            "vendor_count": 1,
            "url_count": len(items),
            "workers": 1,
            "headless": request.browser_options.headless,
            "rating_only": request.rating_only,
            "extract_real_price": request.extract_real_price,
            "extract_product_info": request.extract_product_info,
            "output": str(Path(output_path).expanduser().resolve()),
            "log_file": str(Path(request.log_file).expanduser().resolve()),
        }
    )
    _preflight(request, reporter)
    single_progress = {"done": 0}
    stop_event = multiprocessing.Event()
    old_handlers = {}
    scraper_holder: dict[str, Any] = {}
    stop_attempts = {"value": 0}
    force_stop_requested = {"value": False}

    def stop_single(signum, _frame) -> None:
        stop_attempts["value"] += 1
        if stop_attempts["value"] == 1:
            stop_event.set()
            reporter(
                {
                    "event": "stop_requested",
                    "message": f"received signal {signum}; graceful stop requested",
                    "grace_seconds": request.stop_grace_seconds,
                    "attempt": 1,
                    "mode": "graceful",
                }
            )
            scraper = scraper_holder.get("scraper")
            driver = getattr(scraper, "driver", None)
            if driver:
                try:
                    driver.execute_script("window.stop();")
                except Exception:
                    pass
            return
        force_stop_requested["value"] = True
        reporter(
            {
                "event": "force_stop_requested",
                "message": f"received signal {signum}; forcing browser shutdown",
                "attempt": stop_attempts["value"],
                "mode": "force",
            }
        )
        scraper = scraper_holder.get("scraper")
        if scraper:
            scraper.close()

    for sig in (signal.SIGINT, signal.SIGTERM):
        old_handlers[sig] = signal.getsignal(sig)
        signal.signal(sig, stop_single)

    try:
        _prepare_browser_runtime(request, reporter)
    except BrowserStartupError as exc:
        error = str(exc)
        if exc.suggestion:
            error = f"{error}; suggestion: {exc.suggestion}"
        return TaskResult(False, output_path, [], 0, [], [], request.log_file, error, 4)
    if stop_event.is_set():
        return TaskResult(False, output_path, [], 0, [], [], request.log_file, "stopped by signal", 130)

    def single_reporter(event: dict[str, Any]) -> None:
        if event.get("event") == "url_started":
            event["global_done"] = single_progress["done"]
            event["global_total"] = len(items)
        elif event.get("event") in {"url_done", "url_failed"}:
            single_progress["done"] += 1
            event["global_done"] = single_progress["done"]
            event["global_total"] = len(items)
            reporter(event)
            _emit_progress(
                reporter,
                states={DEFAULT_VENDOR: {"current_index": event.get("url_index"), "total": len(items), "stage": event.get("event").replace("url_", "")}},
                completed_groups=0,
                vendor_count=1,
                urls_done=single_progress["done"],
                url_count=len(items),
                failed=1 if event.get("event") == "url_failed" else 0,
            )
            return
        reporter(event)

    scraper = TripCliScraper(request, single_reporter, vendor=DEFAULT_VENDOR, worker_id=1, global_total=len(items))
    scraper_holder["scraper"] = scraper
    rows: list[dict] = []
    try:
        scraper.prepare_environment()
        scraper.start_browser()
        rows = scraper.scrape_items(items, stop_event=stop_event, rows=rows)
        if rows:
            saved_path = export_rows(
                rows,
                output_path,
                rating_only=request.rating_only,
                extract_real_price=request.extract_real_price,
                extract_product_info=request.extract_product_info,
                include_vendor_column=False,
            )
            event_name = "partial_export_done" if stop_event.is_set() else "export_done"
            reporter({"event": event_name, "rows": len(rows), "output_path": str(Path(saved_path).expanduser().resolve())})
            if stop_event.is_set():
                return TaskResult(False, saved_path, [], len(rows), [], [], request.log_file, "stopped by signal", 130)
            return TaskResult(True, saved_path, [], len(rows), [], [], request.log_file, None, 0)
        if stop_event.is_set():
            return TaskResult(False, output_path, [], 0, [], [], request.log_file, "stopped by signal", 130)
        return TaskResult(False, output_path, [], 0, [], [], request.log_file, "no score/price data captured", 4)
    except Exception as exc:
        reporter({"event": "error", "message": str(exc), "traceback": traceback.format_exc()})
        if rows:
            try:
                saved_path = export_rows(
                    rows,
                    output_path,
                    rating_only=request.rating_only,
                    extract_real_price=request.extract_real_price,
                    extract_product_info=request.extract_product_info,
                    include_vendor_column=False,
                )
                reporter({"event": "partial_export_done", "rows": len(rows), "output_path": str(Path(saved_path).expanduser().resolve())})
                return TaskResult(False, saved_path, [], len(rows), [], [], request.log_file, str(exc), 130 if stop_event.is_set() else 4)
            except Exception:
                pass
        if stop_event.is_set():
            return TaskResult(False, output_path, [], 0, [], [], request.log_file, "stopped by signal", 130)
        return TaskResult(False, output_path, [], 0, [], [], request.log_file, str(exc), 4)
    finally:
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)
        scraper.close()
