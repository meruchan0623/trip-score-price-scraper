#!/usr/bin/env python3
"""Stabilize vendor values in Trip scrape workbooks without changing Go CLI.

The canonical daily target table is the only authority. Its vendor-column
header is used only when a product ID / Trip detail URL maps to exactly one
vendor. Existing named vendors are preserved. Blank or legacy "手动输入" values
are replaced only for an unambiguous daily-target match; all other cells are
left untouched (blank stays blank, "手动输入" is preserved).
"""
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

from openpyxl import load_workbook

DEFAULT_TARGET = Path(__file__).resolve().parents[1] / "docs" / "日常抓取目标.xlsx"
DETAIL_ID_RE = re.compile(r"/detail/(\d+)", re.I)
CANONICAL_VENDOR_ORDER = ("TSIM 網址", "BC", "TGT", "FR", "JY", "其他")
LEGACY_UNSTABLE_VALUES = {"", "手动输入", "手動輸入"}


def product_id(value: object) -> str:
    text = str(value or "").strip()
    match = DETAIL_ID_RE.search(text)
    if match:
        return match.group(1)
    return text if text.isdigit() else ""


def target_map(path: Path) -> tuple[dict[str, str], dict[str, list[str]]]:
    workbook = load_workbook(path, read_only=False, data_only=True)
    sheet = workbook.active
    headers = {col: str(sheet.cell(1, col).value or "").strip() for col in range(1, sheet.max_column + 1)}
    vendor_columns = [(col, vendor) for col, vendor in headers.items() if vendor in CANONICAL_VENDOR_ORDER]
    found: dict[str, set[str]] = defaultdict(set)
    for row in range(2, sheet.max_row + 1):
        for col, vendor in vendor_columns:
            pid = product_id(sheet.cell(row, col).value)
            if pid:
                found[pid].add(vendor)
    workbook.close()
    unique = {pid: next(iter(vendors)) for pid, vendors in found.items() if len(vendors) == 1}
    conflicts = {pid: sorted(vendors, key=CANONICAL_VENDOR_ORDER.index) for pid, vendors in found.items() if len(vendors) > 1}
    return unique, conflicts


def stabilize(path: Path, vendors: dict[str, str]) -> dict[str, int]:
    workbook = load_workbook(path)
    stats = {"workbook": str(path), "sheets": 0, "filled": 0, "preserved": 0, "unmatched": 0, "missing_columns": 0}
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
                # No unambiguous fixed-source match: leave the cell as-is
                # (blank stays blank, manual provenance 手动输入/手動輸入 is preserved).
                stats["unmatched"] += 1
    workbook.save(path)
    workbook.close()
    return stats


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("workbook", nargs="+", type=Path)
    parser.add_argument("--target-table", type=Path, default=DEFAULT_TARGET)
    args = parser.parse_args()
    if not args.target_table.exists():
        raise SystemExit(f"target table missing: {args.target_table}")
    canonical, conflicts = target_map(args.target_table)
    results = [stabilize(path, canonical) for path in args.workbook]
    print(json.dumps({"canonical_matches": len(canonical), "canonical_conflicts": len(conflicts), "results": results}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
