#!/usr/bin/env python3
"""Fail-closed, run-local input and worker validation for HK/RU Mode2 recovery."""
from __future__ import annotations

import argparse
import hashlib
import json
import sys
from datetime import datetime
from pathlib import Path

import openpyxl

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))
from scripts.daily_mode2_google_target_sync import expected_counts, sha256_of, write_env_file, write_url_list
from daily_mode2_urls import extract_product_ids

SITES = ("HK", "RU")
STATE = "mode2_resume.json"


def die(message: str) -> None:
    raise SystemExit(message)


def provenance_stamp(prov: dict) -> str | None:
    """Best available immutable collection clock for pre-helper interrupted runs."""
    try:
        return datetime.fromisoformat(str(prov["synced_at"])).astimezone().strftime("%y%m%d-%H%M")
    except (KeyError, TypeError, ValueError):
        return None


def load_frozen(run_dir: Path, url_file: Path, env_file: Path, workers: int, *, write_files: bool, start_stamp: str | None = None) -> dict:
    target_dir = run_dir / "target"
    provenance_path = target_dir / "provenance.json"
    try:
        prov = json.loads(provenance_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        die(f"frozen target provenance missing or invalid: {exc}")
    workbook = target_dir / Path(str(prov.get("staged_workbook") or "日常抓取目标.xlsx")).name
    if not workbook.is_file() or sha256_of(workbook) != prov.get("sha256"):
        die("frozen target workbook missing or byte hash does not match provenance")
    ids = [str(x) for x in extract_product_ids(workbook)]
    if not ids or len(ids) != len(set(ids)) or ids != [str(x) for x in prov.get("product_ids", [])]:
        die("frozen target IDs do not exactly match provenance")
    if tuple(str(x).lower() for x in prov.get("sites", [])) != ("hk", "ru"):
        die("frozen target provenance site scope is not exactly hk,ru")
    counts = expected_counts(prov)
    if counts["MODE2_EXPECTED_URLS"] != len(ids) or counts["MODE2_EXPECTED_ROWS"] != len(ids) * 2:
        die("frozen target expected counts are inconsistent")
    state_path = run_dir / STATE
    old = {}
    if state_path.exists():
        try: old = json.loads(state_path.read_text(encoding="utf-8"))
        except (OSError, ValueError): die("resume state is invalid")
        if old.get("workers") != workers: die("recorded worker count differs; use isolated force rerun")
        if old.get("target_sha256") != prov["sha256"]: die("resume state target hash differs")
    if write_files:
        write_url_list(prov, url_file)
        write_env_file(prov, env_file)
        state_path.write_text(json.dumps({"workers": workers, "target_sha256": prov["sha256"],
            "collection_start_stamp": start_stamp or old.get("collection_start_stamp") or provenance_stamp(prov)}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    else:
        expected_urls = [f"https://hk.trip.com/things-to-do/detail/{pid}/" for pid in ids]
        if not url_file.is_file() or url_file.read_text(encoding="utf-8").splitlines() != expected_urls:
            die("frozen URL list missing or differs from frozen target")
        expected_env = "".join(f"{k}={v}\n" for k, v in counts.items())
        if not env_file.is_file() or env_file.read_text(encoding="utf-8") != expected_env:
            die("frozen expected-count env missing or differs from frozen target")
        if not state_path.exists():
            state_path.write_text(json.dumps({"workers": workers, "target_sha256": prov["sha256"],
                "collection_start_stamp": provenance_stamp(prov)}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return {"provenance": prov, "ids": ids, "counts": counts, "workbook": workbook}


def chunks(ids: list[str], worker_base: Path, workers: int, write: bool) -> list[set[str]]:
    chunk_dir = worker_base / "chunks"
    size = (len(ids) + workers - 1) // workers
    result = []
    for number in range(1, workers + 1):
        values = ids[(number - 1) * size:number * size]
        if not values: die(f"chunk {number} is empty")
        expected = {f"https://hk.trip.com/things-to-do/detail/{pid}/" for pid in values}
        path = chunk_dir / f"w{number}.txt"
        if write:
            chunk_dir.mkdir(parents=True, exist_ok=True)
            path.write_text("\n".join(sorted(expected, key=lambda x: values.index(x.rstrip('/').split('/')[-1]))) + "\n", encoding="utf-8")
        elif path.exists() and set(path.read_text(encoding="utf-8").splitlines()) != expected:
            die(f"run-local chunk {number} differs from frozen target")
        result.append({x.rstrip("/").split("/")[-1] for x in expected})
    return result


def worker_ids(path: Path, expected: set[str], number: int) -> int:
    log = path / "terminal_output.txt"
    xlsx = path / f"trip_mode2_w{number}.xlsx"
    if not xlsx.is_file() or not xlsx.stat().st_size or not log.is_file(): die(f"worker {number} evidence missing")
    events = []
    for line in log.read_text(encoding="utf-8", errors="replace").splitlines():
        try: event = json.loads(line)
        except ValueError: continue
        if event.get("event") == "run_done": events.append(event)
    if len(events) != 1 or events[0].get("success") is not True or int(events[0].get("processed_count", -1)) != len(expected) * 2:
        die(f"worker {number} lacks authoritative successful run_done evidence")
    wb = openpyxl.load_workbook(xlsx, read_only=True, data_only=True)
    try:
        if wb.sheetnames != list(SITES): die(f"worker {number} sheets must be exactly HK/RU")
        for site in SITES:
            rows = wb[site].iter_rows(values_only=True); header = next(rows, ())
            if "商品ID" not in header: die(f"worker {number} {site} missing 商品ID")
            index = list(header).index("商品ID"); found = []
            for row in rows:
                value = row[index] if index < len(row) else None
                if not isinstance(value, str) or not value.isascii() or not value.isdigit(): die(f"worker {number} {site} malformed 商品ID")
                found.append(value)
            if len(found) != len(expected) or len(found) != len(set(found)) or set(found) != expected:
                die(f"worker {number} {site} IDs do not exactly match its chunk")
    finally: wb.close()
    return len(expected) * 2


def inspect_workers(worker_base: Path, expected: list[set[str]]) -> dict[str, list[int]]:
    """Classify evidence without changing it.

    A failed validation is deliberately local to one worker here: callers use
    this only to choose retry chunks.  Finalization still calls
    ``validate-workers`` and therefore requires every chunk to pass.
    """
    valid, retry = [], []
    for number, ids in enumerate(expected, 1):
        try:
            worker_ids(worker_base / f"w{number}", ids, number)
        except SystemExit:
            retry.append(number)
        else:
            valid.append(number)
    return {"valid": valid, "retry": retry}


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="command", required=True)
    for name in ("freeze", "check-frozen"):
        q = sub.add_parser(name); q.add_argument("--run-dir", type=Path, required=True); q.add_argument("--url-file", type=Path, required=True); q.add_argument("--env-file", type=Path, required=True); q.add_argument("--workers", type=int, required=True); q.add_argument("--start-stamp")
    for name in ("make-chunks", "validate-workers", "inspect-workers"):
        q = sub.add_parser(name); q.add_argument("--run-dir", type=Path, required=True); q.add_argument("--url-file", type=Path, required=True); q.add_argument("--env-file", type=Path, required=True); q.add_argument("--worker-base", type=Path, required=True); q.add_argument("--workers", type=int, required=True)
    args = p.parse_args(argv)
    frozen = load_frozen(args.run_dir, args.url_file, args.env_file, args.workers,
                         write_files=args.command == "freeze", start_stamp=getattr(args, "start_stamp", None))
    if args.command in ("make-chunks", "validate-workers", "inspect-workers"):
        expected = chunks(frozen["ids"], args.worker_base, args.workers, args.command == "make-chunks")
        if args.command == "inspect-workers":
            print(json.dumps(inspect_workers(args.worker_base, expected), separators=(",", ":")))
            return 0
        if args.command == "validate-workers":
            total = sum(worker_ids(args.worker_base / f"w{i}", expected[i - 1], i) for i in range(1, args.workers + 1))
            print(total)
    return 0

if __name__ == "__main__": raise SystemExit(main())
