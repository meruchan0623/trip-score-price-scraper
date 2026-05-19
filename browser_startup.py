from __future__ import annotations

import os
import platform
import re
import shutil
import ssl
import subprocess
import time
import traceback
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from undetected_chromedriver import Patcher


DRIVER_REPO_URL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"


@dataclass(slots=True)
class BrowserStartupInfo:
    chrome_path: str
    chrome_version_main: int | None
    driver_executable_path: str
    cache_dir: str
    ssl_cert_file: str | None = None
    ssl_cert_source: str | None = None
    driver_prepare_ms: int = 0
    driver_reused: bool = False


class BrowserStartupError(RuntimeError):
    def __init__(
        self,
        message: str,
        *,
        stage: str,
        error_type: str,
        suggestion: str | None = None,
        details: dict[str, Any] | None = None,
        traceback_text: str | None = None,
    ) -> None:
        super().__init__(message)
        self.stage = stage
        self.error_type = error_type
        self.suggestion = suggestion
        self.details = details or {}
        self.traceback_text = traceback_text


def detect_chrome_path() -> str | None:
    system = platform.system()
    if system == "Darwin":
        paths = [
            "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
            str(Path.home() / "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
            "google-chrome",
            "chrome",
        ]
    elif system == "Windows":
        paths = [
            os.path.expandvars(r"%ProgramFiles%\Google\Chrome\Application\chrome.exe"),
            os.path.expandvars(r"%ProgramFiles(x86)%\Google\Chrome\Application\chrome.exe"),
            os.path.expandvars(r"%LocalAppData%\Google\Chrome\Application\chrome.exe"),
        ]
    else:
        paths = ["google-chrome", "chromium", "chromium-browser"]
    for path in paths:
        found = shutil.which(path) if os.path.sep not in path else path
        if found and Path(found).exists():
            return found
    return None


def chrome_major_version(chrome_path: str | None) -> int | None:
    if not chrome_path:
        return None
    try:
        result = subprocess.run([chrome_path, "--version"], capture_output=True, text=True, timeout=5)
        match = re.search(r"(\d+)\.", result.stdout or result.stderr)
        return int(match.group(1)) if match else None
    except Exception:
        return None


def configure_ssl_cert_file() -> tuple[str | None, str | None]:
    existing = os.environ.get("SSL_CERT_FILE")
    if existing:
        return existing, "env"
    try:
        import certifi

        cert_path = certifi.where()
    except Exception:
        return None, None
    os.environ["SSL_CERT_FILE"] = cert_path
    os.environ.setdefault("REQUESTS_CA_BUNDLE", cert_path)
    return cert_path, "certifi"


def python_cert_install_command() -> str:
    version = f"{platform.python_version_tuple()[0]}.{platform.python_version_tuple()[1]}"
    return f"/Applications/Python {version}/Install Certificates.command"


def ssl_fix_suggestion() -> str:
    return (
        f'Run "{python_cert_install_command()}" or install certifi and rerun. '
        "The driver release check requires valid Python HTTPS certificates."
    )


def _check_cache_dir(cache_dir: str) -> None:
    path = Path(cache_dir).expanduser()
    try:
        path.mkdir(parents=True, exist_ok=True)
        probe = path / ".write_test"
        probe.write_text("ok", encoding="utf-8")
        probe.unlink(missing_ok=True)
    except Exception as exc:
        raise BrowserStartupError(
            f"undetected_chromedriver cache is not writable: {path}",
            stage="driver_cache",
            error_type=type(exc).__name__,
            suggestion=f"Make this directory writable: {path}",
            traceback_text=traceback.format_exc(),
        ) from exc


def _find_cached_driver(version_main: int | None) -> str | None:
    patcher = Patcher(version_main=version_main or 0)
    cache = Path(patcher.data_path).expanduser()
    candidates = sorted(
        [path for path in cache.rglob("*chromedriver*") if path.is_file()],
        key=lambda path: path.stat().st_mtime,
        reverse=True,
    )
    for candidate in candidates:
        try:
            if patcher.is_binary_patched(str(candidate)):
                return str(candidate)
        except Exception:
            continue
    return None


def _probe_ssl() -> None:
    request = urllib.request.Request(DRIVER_REPO_URL, method="HEAD")
    try:
        with urllib.request.urlopen(request, timeout=8):
            return
    except urllib.error.HTTPError as exc:
        if 200 <= exc.code < 500:
            return
        raise


def _classify_driver_error(exc: Exception) -> tuple[str, str | None]:
    text = f"{type(exc).__name__}: {exc}"
    if isinstance(exc, urllib.error.URLError) and isinstance(getattr(exc, "reason", None), ssl.SSLCertVerificationError):
        return "DriverDownloadSSLError", ssl_fix_suggestion()
    if "CERTIFICATE_VERIFY_FAILED" in text or "SSLCertVerificationError" in text:
        return "DriverDownloadSSLError", ssl_fix_suggestion()
    return type(exc).__name__, None


def prepare_chromedriver() -> BrowserStartupInfo:
    started = time.monotonic()
    ssl_cert_file, ssl_cert_source = configure_ssl_cert_file()
    chrome_path = detect_chrome_path()
    if not chrome_path:
        raise BrowserStartupError(
            "Google Chrome not found",
            stage="chrome_detect",
            error_type="ChromeNotFound",
            suggestion="Install Google Chrome or place it in /Applications.",
        )
    if os.path.sep in chrome_path and not os.access(chrome_path, os.X_OK):
        raise BrowserStartupError(
            f"Chrome path is not executable: {chrome_path}",
            stage="chrome_detect",
            error_type="ChromeNotExecutable",
            suggestion=f"Check execute permission for: {chrome_path}",
        )

    version_main = chrome_major_version(chrome_path)
    patcher = Patcher(version_main=version_main or 0)
    cache_dir = str(Path(patcher.data_path).expanduser())
    _check_cache_dir(cache_dir)

    cached_driver = _find_cached_driver(version_main)
    if cached_driver:
        return BrowserStartupInfo(
            chrome_path=chrome_path,
            chrome_version_main=version_main,
            driver_executable_path=cached_driver,
            cache_dir=cache_dir,
            ssl_cert_file=ssl_cert_file,
            ssl_cert_source=ssl_cert_source,
            driver_prepare_ms=int((time.monotonic() - started) * 1000),
            driver_reused=True,
        )

    try:
        _probe_ssl()
    except Exception as exc:
        error_type, suggestion = _classify_driver_error(exc)
        raise BrowserStartupError(
            f"driver prepare failed: {exc}",
            stage="driver_prepare",
            error_type=error_type,
            suggestion=suggestion,
            details={"ssl_cert_file": ssl_cert_file, "ssl_cert_source": ssl_cert_source},
            traceback_text=traceback.format_exc(),
        ) from exc

    try:
        patcher.auto(version_main=version_main)
    except Exception as exc:
        error_type, suggestion = _classify_driver_error(exc)
        raise BrowserStartupError(
            f"driver prepare failed: {exc}",
            stage="driver_prepare",
            error_type=error_type,
            suggestion=suggestion,
            details={"ssl_cert_file": ssl_cert_file, "ssl_cert_source": ssl_cert_source},
            traceback_text=traceback.format_exc(),
        ) from exc

    driver_path = str(Path(patcher.executable_path).expanduser())
    return BrowserStartupInfo(
        chrome_path=chrome_path,
        chrome_version_main=version_main,
        driver_executable_path=driver_path,
        cache_dir=cache_dir,
        ssl_cert_file=ssl_cert_file,
        ssl_cert_source=ssl_cert_source,
        driver_prepare_ms=int((time.monotonic() - started) * 1000),
        driver_reused=False,
    )
