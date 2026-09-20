#!/usr/bin/env python3
"""Stabilize vendor values in Trip scrape workbooks without changing the Go CLI.

The canonical daily target table is the only authority for a daily run. A row is
filled when its product ID maps to exactly one vendor column of the frozen
target; a product listed in more than one vendor column is resolved by the
manual attribution reference when it covers the ID, and otherwise reported as
the joined canonical column names.

Existing named vendors are preserved. Blank or legacy "手动输入" values are only
replaced by a resolved vendor; when nothing can be resolved the cell is left
untouched and counted as `unmatched`.

The daily Mode2 collector passes `--require-attribution`, which turns a
systematic extraction failure into a non-zero exit so a run can never silently
deliver a workbook whose 厂商 column is entirely "手动输入". Ad-hoc and manual
scrapes keep the default (fill-only) behaviour because their products need not
appear in the daily target at all.
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

from openpyxl import load_workbook

REPO_ROOT = Path(__file__).resolve().parents[1]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from daily_mode2_urls import (  # noqa: E402
    TARGET_SHEET_NAMES,
    VENDOR_COLS,
    resolve_vendor_columns,
)

DEFAULT_TARGET = REPO_ROOT / "docs" / "日常抓取目标.xlsx"
DEFAULT_MANUAL_ATTRIBUTION = Path(
    "/Users/example/Documents/trip-operations/vendor-attribution/manual_vendor_attribution_latest.json"
)
DETAIL_ID_RE = re.compile(r"/detail/(\d+)", re.I)
LEGACY_UNSTABLE_VALUES = {"", "手动输入", "手動輸入"}
CANONICAL_VENDOR_ORDER = tuple(VENDOR_COLS)
EXIT_NO_MATCHES = 3
EXIT_NO_TARGET_IDS = 4
# The manual reference names one vendor differently from the target headers.
MANUAL_VENDOR_ALIASES = {
    "tsim": "TSIM 網址",
    "tsim 網址": "TSIM 網址",
    "bc": "BC",
    "tgt": "TGT",
    "fr": "FR",
    "jy": "JY",
    "oth": "其他",
    "其他": "其他",
}


def product_id(value: object) -> str:
    """Return the product ID for an Excel cell, URL, or numeric value.

    Google Sheets exports product IDs as numbers, so a float cell renders as
    "85473945.0"; that must normalize to "85473945" or every target lookup
    silently misses.
    """
    if isinstance(value, bool) or value is None:
        return ""
    if isinstance(value, float):
        return str(int(value)) if value.is_integer() else ""
    if isinstance(value, int):
        return str(value)
    text = str(value).strip()
    if not text:
        return ""
    match = DETAIL_ID_RE.search(text)
    if match:
        return match.group(1)
    if text.endswith(".0") and text[:-2].isdigit():
        return text[:-2]
    return text if text.isdigit() else ""


def resolve_target_sheet(workbook):
    for sheet_name in TARGET_SHEET_NAMES:
        if sheet_name in workbook.sheetnames:
            return workbook[sheet_name]
    raise KeyError(
        f"target workbook missing one of {list(TARGET_SHEET_NAMES)!r}; "
        f"found {workbook.sheetnames!r}"
    )


def target_map(path: Path) -> tuple[dict[str, str], dict[str, list[str]], int]:
    """Return (unambiguous product->vendor, conflicted product->vendors, target ID count)."""
    workbook = load_workbook(path, data_only=True)
    try:
        sheet = resolve_target_sheet(workbook)
        columns = resolve_vendor_columns(sheet)
        missing = [name for name in CANONICAL_VENDOR_ORDER if name not in columns]
        if missing:
            raise KeyError(f"target workbook missing vendor headers: {missing}")
        found: dict[str, set[str]] = defaultdict(set)
        for row in sheet.iter_rows(min_row=2, values_only=True):
            for vendor, column in columns.items():
                if column - 1 >= len(row):
                    continue
                pid = product_id(row[column - 1])
                if pid:
                    found[pid].add(vendor)
    finally:
        workbook.close()
    unique = {pid: next(iter(vendors)) for pid, vendors in found.items() if len(vendors) == 1}
    conflicts = {
        pid: sorted(vendors, key=CANONICAL_VENDOR_ORDER.index)
        for pid, vendors in found.items()
        if len(vendors) > 1
    }
    return unique, conflicts, len(found)


def manual_attribution_map(path: Path | None) -> dict[str, str]:
    """Load the curated manual vendor reference; missing/unreadable files are ignored."""
    if not path or not Path(path).is_file():
        return {}
    try:
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    lookup = payload.get("lookup_by_product_id") if isinstance(payload, dict) else None
    if not isinstance(lookup, dict):
        return {}
    resolved: dict[str, str] = {}
    for raw_pid, entry in lookup.items():
        pid = product_id(raw_pid)
        name = entry.get("vendor_name") if isinstance(entry, dict) else entry
        vendor = MANUAL_VENDOR_ALIASES.get(str(name or "").strip().lower())
        if pid and vendor:
            resolved[pid] = vendor
    return resolved


def resolve_vendors(
    unique: dict[str, str],
    conflicts: dict[str, list[str]],
    manual: dict[str, str],
) -> tuple[dict[str, str], dict[str, int]]:
    """Merge the frozen target with the manual reference into one vendor lookup."""
    stats = {"from_target": 0, "from_manual_on_conflict": 0, "joined_conflict": 0, "from_manual_only": 0}
    resolved = dict(unique)
    stats["from_target"] = len(unique)
    for pid, vendors in conflicts.items():
        manual_vendor = manual.get(pid)
        if manual_vendor:
            resolved[pid] = manual_vendor
            stats["from_manual_on_conflict"] += 1
        else:
            resolved[pid] = "/".join(vendors)
            stats["joined_conflict"] += 1
    for pid, vendor in manual.items():
        if pid not in resolved:
            resolved[pid] = vendor
            stats["from_manual_only"] += 1
    return resolved, stats


def stabilize(path: Path, vendors: dict[str, str]) -> dict[str, object]:
    workbook = load_workbook(path)
    stats = {
        "workbook": str(path),
        "sheets": 0,
        "filled": 0,
        "preserved": 0,
        "unmatched": 0,
        "missing_columns": 0,
    }
    try:
        for sheet in workbook.worksheets:
            headers = {str(cell.value or "").strip(): cell.column for cell in sheet[1]}
            vendor_col, id_col, url_col = headers.get("厂商"), headers.get("商品ID"), headers.get("URL")
            if not vendor_col or (not id_col and not url_col):
                stats["missing_columns"] += 1
                continue
            stats["sheets"] += 1
            for row in range(2, sheet.max_row + 1):
                cell = sheet.cell(row, vendor_col)
                current = str(cell.value or "").strip()
                if current not in LEGACY_UNSTABLE_VALUES:
                    stats["preserved"] += 1
                    continue
                pid = product_id(sheet.cell(row, id_col).value) if id_col else ""
                if not pid and url_col:
                    pid = product_id(sheet.cell(row, url_col).value)
                vendor = vendors.get(pid)
                if vendor:
                    cell.value = vendor
                    stats["filled"] += 1
                else:
                    # Nothing in the target or the manual reference covers this
                    # ID: never invent a vendor name.
                    stats["unmatched"] += 1
        workbook.save(path)
    finally:
        workbook.close()
    return stats


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("workbook", nargs="+", type=Path)
    parser.add_argument("--target-table", type=Path, default=DEFAULT_TARGET)
    parser.add_argument("--manual-attribution", type=Path, default=DEFAULT_MANUAL_ATTRIBUTION)
    parser.add_argument(
        "--require-attribution",
        action="store_true",
        help="Fail the run when vendor attribution cannot resolve any row (daily Mode2 collector).",
    )
    args = parser.parse_args()
    if not args.target_table.exists():
        raise SystemExit(f"target table missing: {args.target_table}")
    unique, conflicts, id_count = target_map(args.target_table)
    if id_count == 0:
        print(
            f"FATAL: target table {args.target_table} yielded no product IDs; refusing to "
            "stabilize vendor attribution",
            file=sys.stderr,
        )
        return EXIT_NO_TARGET_IDS
    manual = manual_attribution_map(args.manual_attribution)
    vendors, origin = resolve_vendors(unique, conflicts, manual)
    results = [stabilize(path, vendors) for path in args.workbook]
    filled = sum(int(result["filled"]) for result in results)
    unmatched = sum(int(result["unmatched"]) for result in results)
    missing_columns = sum(int(result["missing_columns"]) for result in results)
    if filled == 0 and unmatched > 0 and missing_columns == 0:
        message = (
            "vendor attribution filled 0 rows while "
            f"{unmatched} rows stayed unresolved; the 厂商 column would still read 手动输入"
        )
        if args.require_attribution:
            print(f"FATAL: {message}", file=sys.stderr)
            return EXIT_NO_MATCHES
        print(f"WARNING: {message} (fill-only mode: workbook left unchanged)", file=sys.stderr)
    payload = {
        "canonical_matches": len(unique),
        "canonical_conflicts": len(conflicts),
        "target_product_ids": id_count,
        "resolved_vendors": len(vendors),
        "origin": origin,
        "manual_attribution": str(args.manual_attribution) if manual else "",
        "results": results,
    }
    print(json.dumps(payload, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
