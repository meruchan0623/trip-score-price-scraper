from __future__ import annotations

import importlib.machinery
import importlib.util
import shutil
from pathlib import Path
from typing import Callable

from models import TaskRequest, TaskResult

Reporter = Callable[[str], None]
LEGACY_FILE = Path(__file__).with_name("trip_get_scoreandprice.pyw")


def _load_legacy_module():
    loader = importlib.machinery.SourceFileLoader("trip_get_scoreandprice_legacy", str(LEGACY_FILE))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


def load_inputs(request: TaskRequest) -> list[str]:
    urls = [url for url in request.input_paths if url.strip()]
    if request.urls_file:
        lines = Path(request.urls_file).expanduser().read_text(encoding="utf-8").splitlines()
        urls.extend(line.strip() for line in lines if line.strip())
    if not urls:
        raise ValueError("At least one URL is required via --input or --urls-file.")
    return urls


def default_output_path(request: TaskRequest) -> str:
    filename = "trip_score_results_cli.xlsx" if request.rating_only else "trip_scoreandprice_results_cli.xlsx"
    return str(Path.home() / "Downloads" / filename)


def _find_latest_output(pattern: str) -> Path | None:
    downloads_dir = Path.home() / "Downloads"
    candidates = sorted(downloads_dir.glob(pattern), key=lambda path: path.stat().st_mtime, reverse=True)
    return candidates[0] if candidates else None


def scrape_xxx(request: TaskRequest, reporter: Reporter) -> TaskResult:
    try:
        urls = load_inputs(request)
    except Exception as exc:
        return TaskResult(success=False, error=str(exc), exit_code=2)

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
    app.rating_only_var.set(request.rating_only)
    app.extract_real_price_var.set(request.extract_real_price)
    app.extract_product_info_var.set(request.extract_product_info)

    try:
        reporter("opening browser")
        app.open_browser()
        deadline = request.browser_options.timeout
        waited = 0
        while waited < deadline and app.browser_state == "OPENING":
            root.update()
            root.update_idletasks()
            legacy.time.sleep(0.1)
            waited += 0.1
        if not app.driver:
            return TaskResult(success=False, error="browser failed to start", exit_code=4)

        app.urls_to_process = urls
        app.is_stopped.clear()
        app.is_paused.set()
        app.update_progress(0, len(urls))
        before = _find_latest_output("trip_score*_results_*.xlsx")
        app._scraping_logic()
        root.update()
        if not app.results_data:
            return TaskResult(success=False, error="no score/price data captured", exit_code=4)

        output_path = request.output_path or default_output_path(request)
        after = _find_latest_output("trip_score*_results_*.xlsx")
        if after and after != before:
            target = Path(output_path).expanduser()
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.move(str(after), str(target))
            output_path = str(target)
        return TaskResult(
            success=True,
            output_path=output_path,
            processed_count=len(app.results_data),
            exit_code=0,
        )
    except Exception as exc:
        return TaskResult(success=False, error=str(exc), exit_code=4)
    finally:
        try:
            app.close_browser()
        except Exception:
            pass
        try:
            root.destroy()
        except Exception:
            pass
