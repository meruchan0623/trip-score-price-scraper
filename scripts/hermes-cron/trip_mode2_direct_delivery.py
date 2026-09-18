#!/usr/bin/env python3
"""Deliver a completed Mode2 collection once; no agent/model inference involved."""
from __future__ import annotations

import json
import subprocess
import sys
from datetime import datetime, timedelta
from pathlib import Path

ROOT = Path("/Users/example/Documents/trip-sku-price-scrape")
PROJECT = Path("/Users/example/Develop/trip-score-price-scraper")
DELIVER = PROJECT / "trip_deliver.py"
TARGET = "telegram:YOUR_CHAT_ID"
SITES = {"TW", "HK", "RU", "JP", "VN", "TH"}


def send(text: str) -> None:
    result = subprocess.run(["hermes", "send", "--to", TARGET, text], capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or "hermes send failed")


def split_plaintext(text: str, limit: int = 3500) -> list[str]:
    """Split only at line boundaries so Telegram never truncates a product row."""
    chunks: list[str] = []
    current: list[str] = []
    current_size = 0
    for line in text.splitlines():
        addition = len(line) + (1 if current else 0)
        if current and current_size + addition > limit:
            chunks.append("\n".join(current))
            current = [line]
            current_size = len(line)
        else:
            current.append(line)
            current_size += addition
    if current:
        chunks.append("\n".join(current))
    return chunks


def xlsx_in(run_dir: Path) -> Path | None:
    timestamped = sorted(run_dir.glob("*_trip_mode2_daily_6site.xlsx"))
    if timestamped:
        return timestamped[-1]
    standard = run_dir / "trip_mode2_daily_6site.xlsx"
    if standard.is_file() and standard.stat().st_size:
        return standard
    generic = sorted(run_dir.glob("*mode2*6site*.xlsx"))
    return generic[-1] if generic else None


def tags(path: Path) -> dict[tuple[str, str], tuple[str, str, str]]:
    import openpyxl
    wb = openpyxl.load_workbook(path, read_only=True, data_only=True)
    found: dict[tuple[str, str], tuple[str, str, str]] = {}
    for sheet in wb.sheetnames:
        site = sheet.upper()
        if site not in SITES:
            continue
        rows = wb[sheet].iter_rows(values_only=True)
        header = next(rows, None)
        if not header:
            continue
        columns = {str(v).strip(): i for i, v in enumerate(header) if v is not None}
        pid_i = columns.get("商品ID")
        promo_i = columns.get("总促销标签")
        name_i = columns.get("商品名称")
        vendor_i = columns.get("厂商")
        if pid_i is None or promo_i is None:
            continue
        for row in rows:
            pid = str(row[pid_i]).strip() if len(row) > pid_i and row[pid_i] else ""
            if not pid:
                continue
            promo = str(row[promo_i]).strip() if len(row) > promo_i and row[promo_i] else ""
            name = str(row[name_i]).strip() if name_i is not None and len(row) > name_i and row[name_i] else ""
            vendor = str(row[vendor_i]).strip() if vendor_i is not None and len(row) > vendor_i and row[vendor_i] else ""
            found[(pid, site)] = (promo, name, vendor)
    wb.close()
    return found


def discount_signature(label: str) -> tuple[int, ...]:
    """Promotion semantics, independent of the locale-specific marketing copy."""
    import re
    # Trip's total-promo text is localized, but percentage discounts are stable.
    # Do not treat title/product-info wording changes as a promotion change.
    return tuple(sorted({int(value) for value in re.findall(r"(?<!\d)(\d{1,2})\s*%", label)}))


def discount_text(values: tuple[int, ...]) -> str:
    return " + ".join(f"{value}%" for value in values) if values else "无折扣标签"


def _product_name(pid: str, current: dict, previous: dict) -> str:
    """Prefer a compact HK/TW product name and remove technical suffixes."""
    for source in (current, previous):
        for site in ("HK", "TW", "JP", "TH", "VN", "RU"):
            value = source.get((pid, site), ("", "", ""))
            value = value[1] if len(value) > 1 else ""
            if value:
                import re
                raw_name = value.replace("\\|", "|")
                name = re.split(r"[|｜]", raw_name, maxsplit=1)[0].strip(" \\-")
                return name[:48]
    return "未返回商品名"


def _product_vendor(pid: str, current: dict, previous: dict) -> str:
    """Return a stable vendor label for the product across all six sites."""
    vendors: list[str] = []
    for source in (current, previous):
        for site in ("HK", "TW", "JP", "TH", "VN", "RU"):
            value = source.get((pid, site), ("", "", ""))
            vendor = value[2].strip() if len(value) > 2 else ""
            if vendor and vendor not in vendors:
                vendors.append(vendor)
    if not vendors:
        return "未返回厂商"
    if len(vendors) == 1:
        return vendors[0]
    return "/".join(vendors)


def _report_date(path: Path) -> str:
    try:
        return datetime.strptime(path.parent.name[:6], "%y%m%d").strftime("%Y-%m-%d")
    except ValueError:
        return datetime.now().strftime("%Y-%m-%d")


def promo_message(today: Path, previous: Path | None) -> str:
    """List products/sites whose actual discount percentage set changed."""
    from collections import defaultdict

    current = tags(today)
    yesterday = tags(previous) if previous else {}
    changes: dict[str, dict[tuple[tuple[int, ...], tuple[int, ...]], set[str]]] = defaultdict(
        lambda: defaultdict(set)
    )

    for key in sorted(set(current) | set(yesterday)):
        pid, site = key
        now = current.get(key, ("", "", ""))
        old = yesterday.get(key, ("", "", ""))
        now_label = now[0] if now else ""
        old_label = old[0] if old else ""
        now_sig, old_sig = discount_signature(now_label), discount_signature(old_label)
        if now_sig != old_sig:
            changes[pid][(old_sig, now_sig)].add(site)

    lines = [
        f"📊 携程 Mode2 六站促销标签变化｜{_report_date(today)}",
        f"变化产品：{len(changes)} 个",
    ]
    if not changes:
        lines.append("无实际促销折扣变化（仅文案或语言变化不计）。")
        return "\n".join(lines)

    lines.append("")
    order = {site: i for i, site in enumerate(("HK", "TW", "JP", "TH", "VN", "RU"))}
    for pid in sorted(changes, key=lambda value: (not value.isdigit(), int(value) if value.isdigit() else value)):
        name = _product_name(pid, current, yesterday)
        vendor = _product_vendor(pid, current, yesterday)
        transitions = sorted(changes[pid].items(), key=lambda item: (item[0], sorted(item[1])))
        for index, ((old_sig, now_sig), sites) in enumerate(transitions):
            site_text = "/".join(sorted(sites, key=lambda site: order.get(site, 99)))
            product_text = f"{pid}｜{name}｜{vendor}" if index == 0 else f"{pid}｜{name}｜{vendor}（续）"
            lines.append(
                f"• {product_text}｜{site_text}｜{discount_text(old_sig)} → {discount_text(now_sig)}"
            )
    return "\n".join(lines)


def main() -> int:
    import os

    # Manual-only override (set by trip_daily_mode2_cron.sh under MODE2_FORCE_RERUN=1):
    # re-deliver the latest XLSX and regenerate the promo report even though the
    # .delivery_done / .promo_change_reported markers already exist.
    force = os.environ.get("MODE2_FORCE_DELIVERY") == "1"
    today = datetime.now().strftime("%y%m%d")
    yesterday = (datetime.now() - timedelta(days=1)).strftime("%y%m%d")
    run_dir = ROOT / f"{today}-mode2-daily"
    done = run_dir / ".collection_done"
    delivery = run_dir / ".delivery_done"
    promo_done = run_dir / ".promo_change_reported"
    if not done.exists():
        raise SystemExit("collection marker missing")
    output = xlsx_in(run_dir)
    if not output:
        raise SystemExit("Mode2 output xlsx missing")
    payload = json.loads(done.read_text())
    if force:
        print(f"[delivery] MODE2_FORCE_DELIVERY=1: re-delivering {output}", file=sys.stderr)
    if force or not delivery.exists():
        # Retry up to 3 times with backoff; mark pending on failure so the next
        # cron tick / manual run can compensate instead of losing the deliverable.
        import time
        pending = run_dir / ".pending_delivery.json"
        last_error = ""
        for attempt in range(1, 4):
            result = subprocess.run([sys.executable, str(DELIVER), str(output), "--to", TARGET, "--subject", "Trip Mode2 Daily"], capture_output=True, text=True)
            if result.returncode == 0:
                delivery.write_text(json.dumps({"delivered_at": datetime.now().isoformat(), "output_path": str(output), "target": TARGET, "processed_count": payload.get("processed_count")}))
                pending.unlink(missing_ok=True)
                last_error = ""
                break
            last_error = (result.stderr or result.stdout or "delivery failed").strip()
            if attempt < 3:
                time.sleep(10 * attempt)
        if last_error:
            pending.write_text(json.dumps({"status": "pending", "path": str(output), "target": TARGET, "error": last_error, "attempts": 3}))
            raise SystemExit(last_error)
    if force or not promo_done.exists():
        baseline = xlsx_in(ROOT / f"{yesterday}-mode2-daily")
        if baseline:
            message = promo_message(output, baseline)
            chunks = split_plaintext(message)
            for index, chunk in enumerate(chunks, start=1):
                prefix = f"（{index}/{len(chunks)}）\n" if len(chunks) > 1 else ""
                send(prefix + chunk)
            promo_done.write_text(json.dumps({
                "reported_at": datetime.now().isoformat(),
                "target": TARGET,
                "product_count": message.count("\n• "),
                "chunks": len(chunks),
            }))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
