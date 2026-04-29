from __future__ import annotations

import argparse
from pathlib import Path
from typing import Sequence

from models import BrowserOptions, TaskRequest
from service import scrape_xxx


class Parser(argparse.ArgumentParser):
    def error(self, message: str) -> None:
        raise ValueError(message)


def build_parser() -> Parser:
    parser = Parser(description="Scrape Trip score and price data from one or more product URLs.")
    parser.add_argument("--gui", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--input", action="append", default=[], help="Product URL. Repeat for multiple URLs.")
    parser.add_argument("--urls-file", help="Text file containing one product URL per line.")
    parser.add_argument("--output", help="Excel output path.")
    parser.add_argument("--browser", choices=("chrome",), default="chrome")
    parser.add_argument("--headless", action="store_true", help="Parsed for compatibility; legacy scraper still opens a visible browser.")
    parser.add_argument("--session-file", help="Optional session file path.")
    parser.add_argument("--log-file", help="Optional log file path.")
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument("--rating-only", action="store_true", help="Only scrape rating-related fields.")
    parser.add_argument("--no-real-price", action="store_true", help="Skip dynamic real-price extraction.")
    parser.add_argument("--no-product-info", action="store_true", help="Skip product info extraction.")
    return parser


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

    request = TaskRequest(
        input_paths=args.input,
        urls_file=args.urls_file,
        output_path=args.output,
        log_file=args.log_file,
        rating_only=args.rating_only,
        extract_real_price=not args.no_real_price,
        extract_product_info=not args.no_product_info,
        browser_options=BrowserOptions(
            browser=args.browser,
            headless=args.headless,
            session_file=args.session_file,
            timeout=args.timeout,
        ),
    )
    reporter = _build_reporter(args.log_file)
    if args.headless:
        reporter("warning: --headless is parsed but not enforced by the legacy Chrome bootstrap.")
    result = scrape_xxx(request, reporter)
    if result.success:
        print(f"captured {result.processed_count} rows -> {result.output_path}")
    else:
        print(f"failed: {result.error}")
    return result.exit_code
