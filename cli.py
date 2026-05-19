from __future__ import annotations

import argparse
from pathlib import Path
from typing import Sequence

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
    parser.add_argument("--run-mode", choices=("mode1", "mode2"), help="Preset run mode. mode1=headless full scrape; mode2=headed rating-only scrape.")
    parser.add_argument("--browser", choices=("chrome",), default="chrome")
    parser.add_argument("--headless", dest="headless", action="store_true", default=None, help="Run Chrome in headless mode.")
    parser.add_argument("--no-headless", dest="headless", action="store_false", help="Run Chrome with a visible browser window.")
    parser.add_argument("--session-file", help="Optional session file path.")
    parser.add_argument("--log-file", help="Optional log file path.")
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


def _build_reporter(log_file: str | None):
    log_path = Path(log_file).expanduser() if log_file else None

    def reporter(message: str) -> None:
        print(message)
        if log_path:
            log_path.parent.mkdir(parents=True, exist_ok=True)
            with log_path.open("a", encoding="utf-8") as handle:
                handle.write(f"{message}\n")

    return reporter


def run_cli(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    try:
        args = parser.parse_args(list(argv) if argv is not None else None)
    except ValueError as exc:
        print(f"argument error: {exc}")
        return 2

    run_options = resolve_run_options(args)

    request = TaskRequest(
        input_paths=args.input,
        urls_file=args.urls_file,
        excel_input=args.excel_input,
        output_path=args.output,
        output_dir=args.output_dir,
        log_file=args.log_file,
        workers=args.workers,
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
    reporter = _build_reporter(args.log_file)
    result = scrape_xxx(request, reporter)
    if result.success:
        if result.output_paths:
            print(f"captured {result.processed_count} rows -> {len(result.output_paths)} files")
            for path in result.output_paths:
                print(f"  {path}")
        else:
            print(f"captured {result.processed_count} rows -> {result.output_path}")
    elif result.output_paths:
        print(f"captured {result.processed_count} rows before failure -> {len(result.output_paths)} files")
        for path in result.output_paths:
            print(f"  {path}")
        print(f"failed: {result.error}")
    elif result.output_path and result.processed_count:
        print(f"captured {result.processed_count} rows before failure -> {result.output_path}")
        print(f"failed: {result.error}")
    else:
        print(f"failed: {result.error}")
    return result.exit_code
