#!/usr/bin/env python3
"""
daily_mode2_google_target_sync.py — 每日 Mode2 抓取前，从公开 Google Sheet 同步 日常抓取目标.xlsx

流程（fail-closed，不使用旧镜像兜底）：
  1. 下载 gid=0 工作表的 XLSX 导出到临时文件（同目录，便于原子替换）
  2. 校验：必须是 XLSX（zip 魔数、非 HTML）、含 Sheet1/工作表1、含全部厂商表头、至少一个合法产品 ID
  3. 校验通过后 os.replace 原子替换 docs/日常抓取目标.xlsx，并写出 provenance JSON sidecar
  4. 可选：把已校验的工作簿 + provenance 复制进当日 run 目录（不覆盖已完成 run）
  5. 可选：生成 URL 列表并输出 expected 计数（URL 数、站点数、行数）

用法：
  python3 scripts/daily_mode2_google_target_sync.py \
      [--mirror docs/日常抓取目标.xlsx] [--run-dir RUN_DIR] [--sites hk,tw,...] \
      [--output-urls /tmp/trip_mode2_daily_template.txt] [--env-file PATH]

任何失败都以非零退出码结束，且不会替换现有镜像。
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import sys
import tempfile
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable

import openpyxl

REPO_ROOT = Path(__file__).resolve().parent.parent
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from daily_mode2_urls import VENDOR_COLS, extract_product_ids, resolve_vendor_columns  # noqa: E402

# ---- 集中配置：目标 Google Sheet（公开，无需登录/OAuth） ----
SPREADSHEET_ID = os.environ.get("TRIP_GOOGLE_SPREADSHEET_ID", "").strip()
SHEET_GID = os.environ.get("TRIP_GOOGLE_SHEET_GID", "0")
SHEET_NAME = "Sheet1"
ACCEPTED_SHEET_NAMES = ("Sheet1", "工作表1")
EXPORT_URL = (
    "https://docs.google.com/spreadsheets/d/{spreadsheet_id}/export"
    "?format=xlsx&gid={gid}"
)
DEFAULT_MIRROR = REPO_ROOT / "docs" / "日常抓取目标.xlsx"
PROVENANCE_SUFFIX = ".provenance.json"
DEFAULT_SITES = ("hk", "tw", "sg", "my", "en", "kr")
DEFAULT_URL_OUTPUT = Path("/tmp/trip_mode2_daily_template.txt")
DOWNLOAD_TIMEOUT_SEC = 60.0
XLSX_MAGIC = b"PK\x03\x04"
RUN_TARGET_DIRNAME = "target"
COMPLETED_MARKERS = ("COMPLETED", "result.json", "run_result.json")

Fetcher = Callable[[str, float], bytes]


class TargetSyncError(RuntimeError):
    """任何导致本次同步必须失败（且不得替换镜像）的错误。"""


def export_url(spreadsheet_id: str = SPREADSHEET_ID, gid: str = SHEET_GID) -> str:
    if not spreadsheet_id or spreadsheet_id == "YOUR_GOOGLE_SPREADSHEET_ID":
        raise TargetSyncError("Set TRIP_GOOGLE_SPREADSHEET_ID before syncing")
    return EXPORT_URL.format(spreadsheet_id=spreadsheet_id, gid=gid)


def default_fetch(url: str, timeout: float) -> bytes:
    req = urllib.request.Request(
        url, headers={"User-Agent": "trip-mode2-target-sync/1.0"}
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            status = getattr(resp, "status", 200)
            if status != 200:
                raise TargetSyncError(f"HTTP {status} from Google Sheets export")
            ctype = (resp.headers.get("Content-Type") or "").lower()
            if "text/html" in ctype:
                raise TargetSyncError(
                    f"Google returned HTML (Content-Type={ctype}); sheet not public or login required"
                )
            return resp.read()
    except urllib.error.HTTPError as e:
        raise TargetSyncError(f"HTTP {e.code} from Google Sheets export") from e
    except urllib.error.URLError as e:
        raise TargetSyncError(f"transport error fetching Google Sheet: {e.reason}") from e
    except OSError as e:
        raise TargetSyncError(f"transport error fetching Google Sheet: {e}") from e


def ensure_xlsx_bytes(data: bytes) -> None:
    if not data:
        raise TargetSyncError("empty response from Google Sheets export")
    head = data[:512].lstrip().lower()
    if head.startswith(b"<!doctype") or head.startswith(b"<html") or head.startswith(b"<"):
        raise TargetSyncError("Google returned HTML instead of XLSX (not public / login page)")
    if not data.startswith(XLSX_MAGIC):
        raise TargetSyncError("downloaded payload is not an XLSX (zip magic missing)")


def validate_target_workbook(path: Path) -> list[int]:
    """校验镜像结构并返回产品 ID（与 daily_mode2_urls.extract_product_ids 语义一致）。"""
    try:
        wb = openpyxl.load_workbook(path, data_only=True, read_only=True)
    except Exception as e:  # noqa: BLE001 — 任何解析失败都视为无效
        raise TargetSyncError(f"cannot open workbook: {e}") from e
    try:
        sheet_name = next((name for name in ACCEPTED_SHEET_NAMES if name in wb.sheetnames), None)
        if sheet_name is None:
            raise TargetSyncError(
                f"workbook missing one of {list(ACCEPTED_SHEET_NAMES)!r}; found {wb.sheetnames}"
            )
        ws = wb[sheet_name]
        vendor_columns = resolve_vendor_columns(ws)
        missing = [h for h in VENDOR_COLS if h not in vendor_columns]
        if missing:
            raise TargetSyncError(f"workbook missing required headers: {missing}")
    finally:
        wb.close()
    ids = extract_product_ids(path)
    if not ids:
        raise TargetSyncError("workbook contains no valid product IDs")
    return ids


def sha256_of(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def provenance_path(mirror: Path) -> Path:
    return mirror.with_name(mirror.name + PROVENANCE_SUFFIX)


def build_provenance(
    mirror: Path,
    ids: list[int],
    sites: tuple[str, ...],
    *,
    spreadsheet_id: str = SPREADSHEET_ID,
    gid: str = SHEET_GID,
    synced_at: datetime | None = None,
) -> dict:
    synced_at = synced_at or datetime.now(timezone.utc)
    url_count = len(ids)
    return {
        "source": "google_sheets",
        "spreadsheet_id": spreadsheet_id,
        "gid": gid,
        "sheet_name": SHEET_NAME,
        "export_url": export_url(spreadsheet_id, gid),
        "synced_at": synced_at.isoformat(),
        "mirror": str(mirror),
        "sha256": sha256_of(mirror),
        "size_bytes": mirror.stat().st_size,
        "url_count": url_count,
        "product_count": url_count,
        "sites": list(sites),
        "site_count": len(sites),
        "expected_rows": url_count * len(sites),
        # 产品 ID 以文本保存，避免下游被当成数值截断/转换
        "product_ids": [str(pid) for pid in ids],
    }


def _atomic_write_json(path: Path, payload: dict) -> None:
    fd, tmp = tempfile.mkstemp(prefix=path.name + ".", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(payload, f, ensure_ascii=False, indent=2)
            f.write("\n")
        os.replace(tmp, path)
    except BaseException:
        Path(tmp).unlink(missing_ok=True)
        raise


def sync_target(
    mirror: Path = DEFAULT_MIRROR,
    *,
    sites: tuple[str, ...] = DEFAULT_SITES,
    fetch: Fetcher | None = None,
    spreadsheet_id: str = SPREADSHEET_ID,
    gid: str = SHEET_GID,
    timeout: float = DOWNLOAD_TIMEOUT_SEC,
) -> dict:
    """下载 → 校验 → 原子替换镜像 → 写 provenance。失败时抛 TargetSyncError，镜像不变。"""
    fetch = fetch or default_fetch
    mirror = Path(mirror)
    mirror.parent.mkdir(parents=True, exist_ok=True)
    url = export_url(spreadsheet_id, gid)
    try:
        data = fetch(url, timeout)
    except TargetSyncError:
        raise
    except Exception as e:  # noqa: BLE001
        raise TargetSyncError(f"transport error fetching Google Sheet: {e}") from e
    ensure_xlsx_bytes(data)

    fd, tmp_name = tempfile.mkstemp(prefix=mirror.name + ".", suffix=".download.xlsx", dir=mirror.parent)
    tmp = Path(tmp_name)
    try:
        with os.fdopen(fd, "wb") as f:
            f.write(data)
        ids = validate_target_workbook(tmp)
        os.replace(tmp, mirror)
    except BaseException:
        tmp.unlink(missing_ok=True)
        raise
    prov = build_provenance(mirror, ids, sites, spreadsheet_id=spreadsheet_id, gid=gid)
    _atomic_write_json(provenance_path(mirror), prov)
    return prov


def run_is_completed(run_dir: Path) -> bool:
    return any((run_dir / m).exists() for m in COMPLETED_MARKERS)


def stage_run_target(run_dir: Path, mirror: Path, prov: dict) -> Path:
    """把已校验的目标工作簿和 provenance 复制到 run 目录；已完成的 run 拒绝覆盖。"""
    run_dir = Path(run_dir)
    if run_is_completed(run_dir):
        raise TargetSyncError(f"run directory already completed, refusing to overwrite: {run_dir}")
    target_dir = run_dir / RUN_TARGET_DIRNAME
    existing_prov = target_dir / "provenance.json"
    if existing_prov.exists():
        try:
            old = json.loads(existing_prov.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            old = {}
        if old.get("sha256") and old["sha256"] != prov["sha256"]:
            raise TargetSyncError(
                f"run directory already has a different staged target (sha256 mismatch): {run_dir}"
            )
    target_dir.mkdir(parents=True, exist_ok=True)
    staged_wb = target_dir / mirror.name
    fd, tmp_name = tempfile.mkstemp(prefix=mirror.name + ".", suffix=".stage", dir=target_dir)
    os.close(fd)
    try:
        shutil.copyfile(mirror, tmp_name)
        os.replace(tmp_name, staged_wb)
    except BaseException:
        Path(tmp_name).unlink(missing_ok=True)
        raise
    if sha256_of(staged_wb) != prov["sha256"]:
        raise TargetSyncError("staged workbook hash mismatch after copy")
    _atomic_write_json(existing_prov, {**prov, "staged_workbook": str(staged_wb)})
    return target_dir


def write_url_list(prov: dict, output: Path, source_site: str = "hk") -> int:
    lines = [
        f"https://{source_site}.trip.com/things-to-do/detail/{pid}/"
        for pid in prov["product_ids"]
    ]
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return len(lines)


def expected_counts(prov: dict) -> dict:
    return {
        "MODE2_EXPECTED_URLS": int(prov["url_count"]),
        "MODE2_EXPECTED_SITES": int(prov["site_count"]),
        "MODE2_EXPECTED_ROWS": int(prov["url_count"]) * int(prov["site_count"]),
        "MODE2_TARGET_SHA256": prov["sha256"],
    }


def write_env_file(prov: dict, path: Path) -> None:
    counts = expected_counts(prov)
    body = "".join(f"{k}={v}\n" for k, v in counts.items())
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(body, encoding="utf-8")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(description="同步 Google Sheet 日常抓取目标并生成 mode2 URL 列表")
    p.add_argument("--mirror", default=str(DEFAULT_MIRROR))
    p.add_argument("--run-dir", default=None, help="当日 run 目录；复制目标工作簿与 provenance")
    p.add_argument("--sites", default=",".join(DEFAULT_SITES))
    p.add_argument("--source-site", default="hk")
    p.add_argument("--output-urls", default=None, help="写出 URL 列表（同 daily_mode2_urls.py 格式）")
    p.add_argument("--env-file", default=None, help="写出 MODE2_EXPECTED_* 供 shell source")
    p.add_argument("--timeout", type=float, default=DOWNLOAD_TIMEOUT_SEC)
    return p.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    sites = tuple(s.strip() for s in args.sites.split(",") if s.strip())
    if not sites:
        print("ERROR: --sites is empty", file=sys.stderr)
        return 2
    mirror = Path(args.mirror)
    try:
        prov = sync_target(mirror, sites=sites, timeout=args.timeout)
        if args.run_dir:
            stage_run_target(Path(args.run_dir), mirror, prov)
        if args.output_urls:
            write_url_list(prov, Path(args.output_urls), args.source_site)
        if args.env_file:
            write_env_file(prov, Path(args.env_file))
    except TargetSyncError as e:
        print(f"ERROR: target sync failed, canonical mirror NOT replaced: {e}", file=sys.stderr)
        return 1
    summary = {k: v for k, v in prov.items() if k != "product_ids"}
    summary.update(expected_counts(prov))
    print(json.dumps(summary, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
