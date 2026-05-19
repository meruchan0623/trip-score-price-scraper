from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(slots=True)
class ScrapeItem:
    url: str
    vendor: str = "手动输入"
    source_column: int | None = None
    source_row: int | None = None
    input_order: int = 0


@dataclass(slots=True)
class BrowserOptions:
    browser: str = "chrome"
    headless: bool = False
    session_file: str | None = None
    wait_login: bool = False
    timeout: int = 300
    memory_guard_enabled: bool = True
    memory_limit_mb: int = 8192
    restart_every: int = 20


@dataclass(slots=True)
class TaskRequest:
    urls_file: str | None = None
    input_paths: list[str] = field(default_factory=list)
    excel_input: str | None = None
    output_path: str | None = None
    output_dir: str | None = None
    log_file: str | None = None
    workers: int | None = None
    rating_only: bool = False
    extract_real_price: bool = True
    extract_product_info: bool = True
    browser_options: BrowserOptions = field(default_factory=BrowserOptions)


@dataclass(slots=True)
class TaskResult:
    success: bool
    output_path: str | None = None
    output_paths: list[str] = field(default_factory=list)
    processed_count: int = 0
    warnings: list[str] = field(default_factory=list)
    error: str | None = None
    exit_code: int = 0
