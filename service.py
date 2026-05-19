from __future__ import annotations

import importlib.machinery
import importlib.util
import multiprocessing
import os
import platform
import queue
import re
import shutil
import signal
import tempfile
from datetime import datetime
from pathlib import Path
from typing import Callable

import openpyxl

from models import ScrapeItem, TaskRequest, TaskResult

Reporter = Callable[[str], None]
LEGACY_FILE = Path(__file__).with_name("trip_get_scoreandprice.pyw")
DEFAULT_VENDOR = "手动输入"
MAX_WORKERS = 5


def _load_legacy_module():
    loader = importlib.machinery.SourceFileLoader("trip_get_scoreandprice_legacy", str(LEGACY_FILE))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


def load_inputs(request: TaskRequest) -> list[str]:
    items = load_items(request)
    return [item.url for item in items]


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
                items.append(
                    ScrapeItem(
                        url=url,
                        vendor=DEFAULT_VENDOR,
                        source_row=row_number,
                        input_order=order,
                    )
                )
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
            found_for_vendor = 0
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
                    found_for_vendor += 1
                else:
                    warnings.append(f"ignored non-Trip link at column {column}, row {row}: {url}")
            if found_for_vendor == 0:
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


def default_vendor_output_dir(base_dir: Path | None = None) -> str:
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    return str((base_dir or (Path.home() / "Documents")) / f"trip_vendor_results_{timestamp}")


def _expand_output_path(path: str) -> str:
    return str(Path(path).expanduser())


def _safe_filename(value: str) -> str:
    cleaned = re.sub(r'[\\/:*?"<>|\r\n\t]+', "_", value).strip(" ._")
    return cleaned or "未命名厂商"


def _vendor_groups(items: list[ScrapeItem]) -> list[dict]:
    groups: list[dict] = []
    seen: dict[str, int] = {}
    by_key: dict[tuple[str, int | None], dict] = {}
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
    if vendor_count <= 0:
        return 1
    return requested or min(vendor_count, MAX_WORKERS)


def _vendor_output_path(group: dict, output_dir: str, timestamp: str) -> str:
    filename = f"{_safe_filename(group['file_vendor'])}_{timestamp}.xlsx"
    return str(Path(output_dir).expanduser() / filename)


def default_session_path() -> str:
    if platform.system() == "Windows":
        config_dir = Path(os.environ.get("PROGRAMDATA", "C:\\ProgramData")) / "TripDetailFilling"
    elif platform.system() == "Darwin":
        config_dir = Path.home() / "Library" / "Preferences" / "TripDetailFilling"
    else:
        config_dir = Path.home() / ".config" / "TripDetailFilling"
    return str(config_dir / "session_data.json")


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
        log_file=None,
        workers=1,
        rating_only=request.rating_only,
        extract_real_price=request.extract_real_price,
        extract_product_info=request.extract_product_info,
        browser_options=type(request.browser_options)(
            browser=request.browser_options.browser,
            headless=request.browser_options.headless,
            session_file=session_file,
            timeout=request.browser_options.timeout,
            memory_guard_enabled=request.browser_options.memory_guard_enabled,
            memory_limit_mb=request.browser_options.memory_limit_mb,
            restart_every=request.browser_options.restart_every,
        ),
    )


def _scrape_vendor_worker(
    vendor: str,
    items: list[ScrapeItem],
    request: TaskRequest,
    output_path: str,
    session_file: str,
    result_queue,
) -> None:
    app = None
    root = None

    def reporter(message: str) -> None:
        result_queue.put({"type": "log", "vendor": vendor, "message": message})

    try:
        legacy = _load_legacy_module()
        legacy.messagebox.showerror = lambda *args, **kwargs: reporter(f"error dialog suppressed: {args[-1]}")
        legacy.messagebox.showwarning = lambda *args, **kwargs: reporter(f"warning dialog suppressed: {args[-1]}")
        legacy.messagebox.showinfo = lambda *args, **kwargs: reporter(f"info dialog suppressed: {args[-1]}")
        root = legacy.tk.Tk()
        root.withdraw()
        app = legacy.WebScraperApp(root)
        original_log = app.log

        def log(message: str) -> None:
            reporter(message)
            original_log(message)

        app.log = log
        app.session_manager.session_file = session_file
        app.browser_memory_guard_enabled = request.browser_options.memory_guard_enabled
        app.browser_memory_limit_mb = request.browser_options.memory_limit_mb
        app.browser_restart_every = request.browser_options.restart_every
        app.cli_headless = request.browser_options.headless
        app.cli_output_path = output_path
        app.include_vendor_column = True
        app.rating_only_var.set(request.rating_only)
        app.extract_real_price_var.set(request.extract_real_price)
        app.extract_product_info_var.set(request.extract_product_info)

        reporter("opening browser")
        app.open_browser()
        waited = 0
        while waited < request.browser_options.timeout and app.browser_state == "OPENING":
            root.update()
            root.update_idletasks()
            legacy.time.sleep(0.1)
            waited += 0.1
        root.update()
        root.update_idletasks()
        if not app.driver:
            raise RuntimeError("browser failed to start")

        app.urls_to_process = items
        app.is_stopped.clear()
        app.is_paused.set()
        app.update_progress(0, len(items))
        app._scraping_logic()
        root.update()
        save_error = getattr(app, "last_save_error", None)
        if save_error:
            raise RuntimeError(f"export failed: {save_error}")
        result_queue.put(
            {
                "type": "done",
                "vendor": vendor,
                "success": True,
                "rows": len(app.results_data),
                "rows_data": app.results_data,
                "output_path": getattr(app, "last_saved_filepath", None) or output_path,
                "session_file": session_file,
            }
        )
    except Exception as exc:
        result_queue.put(
            {
                "type": "done",
                "vendor": vendor,
                "success": False,
                "rows": 0,
                "output_path": output_path,
                "session_file": session_file,
                "error": str(exc),
            }
        )
    finally:
        try:
            if app:
                app._safe_driver_quit()
        except Exception:
            pass
        try:
            if root:
                root.destroy()
        except Exception:
            pass


def scrape_vendor_batches(
    request: TaskRequest,
    reporter: Reporter,
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

    if request.excel_input:
        _items, warnings = load_excel_items(request.excel_input)
    else:
        warnings = []
    for warning in warnings:
        reporter(f"warning: {warning}")

    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    if request.output_dir:
        output_dir = _expand_output_path(request.output_dir)
    elif output_base_dir:
        output_dir = str(Path(output_base_dir).expanduser() / f"trip_vendor_results_{timestamp}")
    else:
        output_dir = default_vendor_output_dir()
    Path(output_dir).mkdir(parents=True, exist_ok=True)

    base_session = str(Path(request.browser_options.session_file or default_session_path()).expanduser())
    session_temp_dir = tempfile.mkdtemp(prefix="trip_vendor_sessions_")
    result_queue = multiprocessing.Queue()
    pending = list(groups)
    active: list[dict] = []
    results: list[dict] = []
    output_paths: list[str] = []
    completed_groups = 0
    stop_requested = {"value": False}
    old_handlers = {}

    def stop_all(signum, _frame) -> None:
        stop_requested["value"] = True
        reporter(f"received signal {signum}; stopping vendor workers")
        for entry in active:
            process = entry["process"]
            if process.is_alive():
                process.terminate()

    for sig in (signal.SIGINT, signal.SIGTERM):
        old_handlers[sig] = signal.getsignal(sig)
        signal.signal(sig, stop_all)

    def start_next(index: int, group: dict) -> dict:
        output_path = request.output_path if len(groups) == 1 and request.output_path else _vendor_output_path(group, output_dir, timestamp)
        output_path = _expand_output_path(output_path)
        session_file = _copy_session_to_temp(base_session, session_temp_dir, index)
        worker_request = _worker_request(request, output_path, session_file)
        process = multiprocessing.Process(
            target=_scrape_vendor_worker,
            args=(group["vendor"], group["items"], worker_request, output_path, session_file, result_queue),
            daemon=False,
        )
        process.start()
        reporter(f"[{group['vendor']}] worker started with {len(group['items'])} urls")
        return {"process": process, "group": group, "output_path": output_path, "session_file": session_file}

    try:
        index = 0
        while pending and len(active) < workers:
            index += 1
            active.append(start_next(index, pending.pop(0)))

        while active:
            if should_stop and should_stop():
                stop_requested["value"] = True
                reporter("stop requested; terminating vendor workers")
                for entry in active:
                    process = entry["process"]
                    if process.is_alive():
                        process.terminate()
            try:
                message = result_queue.get(timeout=0.5)
            except queue.Empty:
                message = None
            if message:
                if message.get("type") == "log":
                    reporter(f"[{message.get('vendor')}] {message.get('message')}")
                elif message.get("type") == "done":
                    results.append(message)
                    completed_groups += 1
                    if progress_callback:
                        progress_callback(completed_groups, len(groups))
                    if rows_callback and message.get("rows_data"):
                        rows_callback(message["rows_data"])
                    if message.get("success") and message.get("output_path"):
                        output_paths.append(message["output_path"])
                    reporter(f"[{message.get('vendor')}] done: {message.get('rows', 0)} rows")

            still_active = []
            for entry in active:
                process = entry["process"]
                if process.is_alive():
                    still_active.append(entry)
                    continue
                process.join(timeout=0)
                if process.exitcode not in (0, None) and not any(
                    result.get("output_path") == entry["output_path"] for result in results
                ):
                    results.append(
                        {
                            "vendor": entry["group"]["vendor"],
                            "success": False,
                            "rows": 0,
                            "output_path": entry["output_path"],
                            "session_file": entry["session_file"],
                            "error": f"worker exited with code {process.exitcode}",
                        }
                    )
                elif pending and not stop_requested["value"]:
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
        if stop_requested["value"]:
            return TaskResult(
                success=False,
                output_path=output_dir,
                output_paths=output_paths,
                processed_count=rows,
                warnings=warnings,
                error="stopped by signal",
                exit_code=130,
            )
        if failures:
            error = "; ".join(f"{failure.get('vendor')}: {failure.get('error')}" for failure in failures)
            return TaskResult(
                success=False,
                output_path=output_dir,
                output_paths=output_paths,
                processed_count=rows,
                warnings=warnings,
                error=error,
                exit_code=4,
            )
        return TaskResult(
            success=True,
            output_path=output_dir,
            output_paths=output_paths,
            processed_count=rows,
            warnings=warnings,
            exit_code=0,
        )
    finally:
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)
        for entry in active:
            process = entry["process"]
            if process.is_alive():
                process.terminate()
        shutil.rmtree(session_temp_dir, ignore_errors=True)


def _install_signal_handlers(app, reporter: Reporter):
    old_handlers = {}
    stop_requested = {"value": False}

    def request_stop(signum, _frame) -> None:
        stop_requested["value"] = True
        reporter(f"received signal {signum}; stopping after current browser operation")
        app.is_stopped.set()
        app.is_paused.set()
        try:
            if app.driver:
                app.driver.execute_script("window.stop();")
                reporter("sent window.stop() to browser")
        except Exception as exc:
            reporter(f"warning: browser stop command failed: {exc}")

    def toggle_pause(signum, _frame) -> None:
        if app.is_paused.is_set():
            app.is_paused.clear()
            reporter(f"received signal {signum}; task paused")
        else:
            app.is_paused.set()
            reporter(f"received signal {signum}; task resumed")

    for sig in (signal.SIGINT, signal.SIGTERM):
        old_handlers[sig] = signal.getsignal(sig)
        signal.signal(sig, request_stop)

    sigusr1 = getattr(signal, "SIGUSR1", None)
    if sigusr1 is not None:
        old_handlers[sigusr1] = signal.getsignal(sigusr1)
        signal.signal(sigusr1, toggle_pause)
    else:
        reporter("SIGUSR1 pause/resume is not available on this platform")

    def restore() -> None:
        for sig, handler in old_handlers.items():
            signal.signal(sig, handler)

    return stop_requested, restore


def scrape_xxx(request: TaskRequest, reporter: Reporter) -> TaskResult:
    if request.excel_input:
        return scrape_vendor_batches(request, reporter)

    try:
        urls = load_inputs(request)
    except Exception as exc:
        return TaskResult(success=False, error=str(exc), exit_code=2)

    output_path = _expand_output_path(request.output_path or default_output_path(request))
    legacy = _load_legacy_module()
    legacy.messagebox.showerror = lambda *args, **kwargs: reporter(f"error dialog suppressed: {args[-1]}")
    legacy.messagebox.showwarning = lambda *args, **kwargs: reporter(f"warning dialog suppressed: {args[-1]}")
    legacy.messagebox.showinfo = lambda *args, **kwargs: reporter(f"info dialog suppressed: {args[-1]}")
    root = legacy.tk.Tk()
    root.withdraw()
    app = legacy.WebScraperApp(root)

    original_log = app.log

    def log(message: str) -> None:
        reporter(message)
        original_log(message)

    app.log = log
    if request.browser_options.session_file:
        app.session_manager.session_file = str(Path(request.browser_options.session_file).expanduser())
    app.browser_memory_guard_enabled = request.browser_options.memory_guard_enabled
    app.browser_memory_limit_mb = request.browser_options.memory_limit_mb
    app.browser_restart_every = request.browser_options.restart_every
    app.cli_headless = request.browser_options.headless
    app.cli_output_path = output_path
    app.rating_only_var.set(request.rating_only)
    app.extract_real_price_var.set(request.extract_real_price)
    app.extract_product_info_var.set(request.extract_product_info)

    stop_requested, restore_signal_handlers = _install_signal_handlers(app, reporter)
    try:
        reporter("opening browser")
        app.open_browser()
        deadline = request.browser_options.timeout
        waited = 0
        while waited < deadline and app.browser_state == "OPENING" and not stop_requested["value"]:
            root.update()
            root.update_idletasks()
            legacy.time.sleep(0.1)
            waited += 0.1
        root.update()
        root.update_idletasks()
        if stop_requested["value"]:
            return TaskResult(success=False, error="stopped before browser startup completed", exit_code=130)
        if not app.driver:
            return TaskResult(success=False, error="browser failed to start", exit_code=4)

        app.urls_to_process = urls
        app.is_stopped.clear()
        if stop_requested["value"]:
            app.is_stopped.set()
        app.is_paused.set()
        app.update_progress(0, len(urls))
        app._scraping_logic()
        root.update()
        if not app.results_data:
            if stop_requested["value"]:
                return TaskResult(success=False, error="stopped by signal", exit_code=130)
            return TaskResult(success=False, error="no score/price data captured", exit_code=4)

        saved_path = getattr(app, "last_saved_filepath", None) or output_path
        save_error = getattr(app, "last_save_error", None)
        if save_error:
            return TaskResult(
                success=False,
                output_path=saved_path,
                processed_count=len(app.results_data),
                error=f"export failed: {save_error}",
                exit_code=4,
            )
        if stop_requested["value"] or app.is_stopped.is_set():
            return TaskResult(
                success=False,
                output_path=saved_path,
                processed_count=len(app.results_data),
                error="stopped by signal",
                exit_code=130,
            )
        return TaskResult(
            success=True,
            output_path=saved_path,
            processed_count=len(app.results_data),
            exit_code=0,
        )
    except Exception as exc:
        return TaskResult(success=False, error=str(exc), exit_code=4)
    finally:
        restore_signal_handlers()
        try:
            app._safe_driver_quit()
        except Exception:
            pass
        try:
            root.destroy()
        except Exception:
            pass
