#!/usr/bin/env python3
"""Resumable HK/RU promotion-report delivery; collection remains local."""
from __future__ import annotations

import hashlib
import importlib.util
import json
import os
import re
import subprocess
import sys
from datetime import datetime, datetime as NativeDateTime, timedelta
from pathlib import Path
from zoneinfo import ZoneInfo

ROOT = Path(os.environ.get("TRIP_DATA_ROOT", str(Path.home() / "Documents" / "trip-sku-price-scrape")))
PROJECT = Path(os.environ.get("TRIP_PROJECT_ROOT", str(Path(__file__).resolve().parents[2])))
DELIVER = PROJECT / "trip_deliver.py"
REPORT = PROJECT / "scripts/trip_competitor_promo_report.py"
TARGET = os.environ.get("TRIP_DELIVERY_TARGET", "").strip()
SITES = ("HK", "RU")
SCOPE = "hk_ru_2site"
REPORT_BASENAME = "Trip_Mode2_HK-RU_竞品促销报告_{stamp}.xlsx"
SOURCE_START = re.compile(r"^(?P<stamp>\d{6}-\d{4})_trip_mode2_daily_2site\.xlsx$")
SHANGHAI = ZoneInfo("Asia/Shanghai")


def digest(path):
    hasher = hashlib.sha256()
    with Path(path).open("rb") as stream:
        for block in iter(lambda: stream.read(1048576), b""):
            hasher.update(block)
    return hasher.hexdigest()


def receipt_write(path, value):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    os.replace(temporary, path)


def report_stamp(output, marker):
    """Use the collection-start name; never use a retry's current clock."""
    matched = SOURCE_START.fullmatch(Path(output).name)
    if matched:
        return NativeDateTime.strptime(matched.group("stamp"), "%y%m%d-%H%M").strftime("%Y%m%d-%H%M")
    try:
        return NativeDateTime.fromisoformat(str(marker.get("cron_tick", ""))).strftime("%Y%m%d-%H%M")
    except ValueError:
        return NativeDateTime.fromtimestamp(Path(output).stat().st_mtime, SHANGHAI).strftime("%Y%m%d-%H%M")


def report_paths(report_dir, output, marker):
    legacy = report_dir / "report.xlsx"
    timestamped = report_dir / REPORT_BASENAME.format(stamp=report_stamp(output, marker))
    return legacy, timestamped


def marker_output(run, marker, baseline=False):
    path = Path(str(marker.get("output_path", "")))
    sites = str(marker.get("sites", "")).lower()
    good = sites == "hk,ru" and marker.get("scope") == SCOPE and path.name.endswith("_2site.xlsx")
    good = good or (baseline and sites == "tw,hk,ru,jp,vn,th" and path.name.endswith("_6site.xlsx"))
    return path if good and path.is_file() and path.parent == run else None


def xlsx_in(run, baseline=False):
    try:
        selected = marker_output(run, json.loads((run / ".collection_done").read_text()), baseline)
        if selected:
            return selected
    except (OSError, ValueError):
        pass
    patterns = ["*_trip_mode2_daily_2site.xlsx"] + (["*_trip_mode2_daily_6site.xlsx"] if baseline else [])
    for pattern in patterns:
        candidates = sorted(path for path in run.glob(pattern) if path.is_file() and path.stat().st_size)
        if candidates:
            return candidates[-1]
    return None


def strict_hk_ru(path, expected=None, target_ids=None):
    import openpyxl

    workbook = openpyxl.load_workbook(path, read_only=True, data_only=True)
    try:
        if workbook.sheetnames != list(SITES):
            raise ValueError(f"expected exactly HK/RU sheets, got {workbook.sheetnames!r}")
        found = {}
        for site in SITES:
            rows = workbook[site].iter_rows(values_only=True)
            header = next(rows, ())
            if "商品ID" not in header:
                raise ValueError(f"{site} missing 商品ID")
            index = list(header).index("商品ID")
            ids = []
            for row in rows:
                value = row[index] if index < len(row) else None
                if not isinstance(value, str) or not value or value != value.strip() or not value.isascii() or not value.isdigit():
                    raise ValueError(f"{site} has blank or malformed 商品ID")
                ids.append(value)
            if not ids or len(ids) != len(set(ids)):
                raise ValueError(f"{site} has duplicate 商品ID")
            if expected is not None and len(ids) != expected:
                raise ValueError(f"{site} rows={len(ids)} expected={expected}")
            if target_ids is not None and set(ids) != target_ids:
                raise ValueError(f"{site} 商品ID set mismatches frozen target")
            found[site] = set(ids)
        if found["HK"] != found["RU"]:
            raise ValueError("HK/RU 商品ID sets differ")
    finally:
        workbook.close()


def report_module():
    spec = importlib.util.spec_from_file_location("promo_report", REPORT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def frozen_target_ids(target, module):
    targets, _ = module.load_targets(target)
    ids = {product_id for _, product_id in targets if product_id.isascii() and product_id.isdigit()}
    if not ids:
        raise ValueError("frozen target snapshot has no digit-string product IDs")
    return ids


def deliver(path, subject):
    result = subprocess.run([sys.executable, str(DELIVER), str(path), "--to", TARGET, "--subject", subject], capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError((result.stderr or result.stdout or "trip_deliver failed").strip())
    receipt_path = Path(path).parent / ".delivered.json"
    try:
        receipt = json.loads(receipt_path.read_text())
    except (OSError, ValueError) as error:
        raise RuntimeError(f"trip_deliver completed without a usable receipt: {error}") from error
    if receipt.get("status") != "delivered" or receipt.get("path") != str(path) or receipt.get("target") != TARGET:
        raise RuntimeError("trip_deliver receipt does not confirm this report attachment")
    return {"receipt_path": str(receipt_path), "receipt": receipt}


def send(message):
    result = subprocess.run(["hermes", "send", "--json", "--to", TARGET, message], capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or "hermes send failed")
    try:
        payload = json.loads(result.stdout)
    except ValueError as error:
        raise RuntimeError("hermes send returned invalid JSON") from error
    message_id = payload.get("message_id")
    if payload.get("success") is not True or not isinstance(message_id, (str, int)) or not str(message_id):
        raise RuntimeError("hermes send did not confirm a target message receipt")
    return str(message_id)


def selected_run(argv=None):
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument('--date', help='YYMMDD run date')
    group.add_argument('--run-dir', type=Path, help='existing Mode2 run directory')
    parser.add_argument('--root', type=Path, default=ROOT)
    args = parser.parse_args([] if argv is None else argv)
    root = args.root.resolve()
    if args.run_dir:
        run = args.run_dir.resolve()
        if run.parent != root or not re.fullmatch(r'\d{6}-mode2-daily', run.name):
            parser.error('--run-dir must be a direct YYMMDD-mode2-daily child of --root')
        return run
    if args.date:
        if not re.fullmatch(r'\d{6}', args.date):
            parser.error('--date must be YYMMDD')
        return root / f'{args.date}-mode2-daily'
    return root / f"{datetime.now():%y%m%d}-mode2-daily"


def _main(argv=None):
    if not TARGET:
        raise SystemExit("Set TRIP_DELIVERY_TARGET before delivery")
    run = selected_run(argv)
    now = datetime.now()
    run_date = NativeDateTime.strptime(run.name[:6], "%y%m%d")
    try:
        marker = json.loads((run / ".collection_done").read_text())
    except (OSError, ValueError) as error:
        raise SystemExit(f"HK/RU collection marker missing or invalid: {error}")
    output = marker_output(run, marker)
    if not output:
        raise SystemExit("collection marker does not select an existing HK/RU two-site workbook")
    target = Path(str(marker.get("target_snapshot", "")))
    if not target.is_file():
        raise SystemExit("frozen target snapshot missing")
    module = report_module()
    target_ids = frozen_target_ids(target, module)
    strict_hk_ru(output, int(marker.get("url_count") or 0) or None, target_ids)
    previous = xlsx_in(run.parent / f"{(run_date - timedelta(days=1)):%y%m%d}-mode2-daily", True)
    source_hash = hashlib.sha256("|".join(digest(path) for path in (output, target) + ((previous,) if previous else ())).encode()).hexdigest()
    # The run directory names the immutable collection day.  A delivery retry
    # after midnight must reuse its original report/receipt, not make a new
    # report path that looks like a different delivery.
    report_dir = run / f"{run_date:%y%m%d}-{source_hash[:16]}-promo-replay"
    legacy_report, timestamped_report = report_paths(report_dir, output, marker)
    receipt_path, final_path = run / ".delivery_done", run / ".promo_change_reported"
    try:
        receipt = json.loads(receipt_path.read_text())
    except (OSError, ValueError):
        receipt = {}
    same_source = all((receipt.get("scope") == SCOPE, receipt.get("source_path") == str(output), receipt.get("source_sha256") == source_hash, receipt.get("target") == TARGET))
    # A pre-naming generic artifact is immutable; only this invocation's newly
    # rendered file is renamed before its first delivery.
    report = legacy_report if legacy_report.is_file() else timestamped_report
    same = same_source and receipt.get("report_path") == str(report)
    rendered = False
    if not report.is_file() and not legacy_report.is_file():
        if report_dir.exists():
            report_dir.rename(report_dir.with_name(report_dir.name + ".incomplete-preserved"))
        arguments = ["--current", str(output), "--target", str(target), "--output-dir", str(report_dir)]
        if previous:
            arguments.extend(("--previous", str(previous)))
        module.main(arguments)
        rendered = True
    if rendered and legacy_report.is_file() and not timestamped_report.exists():
        legacy_report.rename(timestamped_report)
    if not same:
        receipt = {"scope": SCOPE, "source_path": str(output), "source_sha256": source_hash, "report_path": str(report), "target": TARGET, "artifacts": {}, "messages": {}}
    if not receipt["artifacts"].get("report_xlsx"):
        receipt["artifacts"]["report_xlsx"] = deliver(report, "Trip Mode2 HK/RU competitor promo report")
        receipt_write(receipt_path, receipt)
    for index, message in enumerate((report_dir / "messages.txt").read_text().strip().split("\n\n--- Telegram message ---\n\n")):
        key = str(index)
        if message.strip() and not receipt["messages"].get(key):
            receipt["messages"][key] = send(message)
            receipt_write(receipt_path, receipt)
    receipt["completed_at"] = now.isoformat()
    receipt_write(receipt_path, receipt)
    receipt_write(final_path, {"scope": SCOPE, "source_path": str(output), "source_sha256": source_hash, "report_path": str(report), "completed_at": now.isoformat()})
    (run / ".pending_delivery.json").unlink(missing_ok=True)
    return 0


def main(argv=None):
    try:
        return _main(argv)
    except Exception as error:
        # Parsing failures may not designate a safe run directory.
        try:
            run = selected_run(argv)
            receipt_write(run / ".pending_delivery.json", {"status": "pending", "error": str(error), "scope": SCOPE})
        except SystemExit:
            pass
        raise SystemExit(str(error))


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
