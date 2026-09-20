#!/usr/bin/env python3
"""Deliver a completed Mode2 collection once; no agent/model inference involved."""
from __future__ import annotations

import json
import subprocess
import sys
from collections import Counter
from datetime import datetime, timedelta
from pathlib import Path

ROOT = Path("/Users/example/Documents/trip-sku-price-scrape")
PROJECT = Path("/Users/example/Develop/trip-score-price-scraper")
DELIVER = PROJECT / "trip_deliver.py"
TARGET = "telegram:YOUR_CHAT_ID"
SITES = {"TW", "HK", "RU", "JP", "VN", "TH"}
# The Go CLI defaults 厂商 to this when nothing real was attributed.
LEGACY_VENDOR_PLACEHOLDERS = {"手动输入", "手動輸入"}


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
            # Legacy workbooks wrote the CLI placeholder instead of a real vendor;
            # never let it leak into a business report as if it were a vendor name.
            if vendor and vendor not in LEGACY_VENDOR_PLACEHOLDERS and vendor not in vendors:
                vendors.append(vendor)
    if not vendors:
        return "未返回厂商"
    if len(vendors) == 1:
        return vendors[0]
    return "/".join(vendors)


def _promo_label_text(labels: list[str]) -> str:
    """Show source promotion labels without letting one row exhaust a message."""
    values = []
    for label in labels:
        normalized = " ".join(str(label).split())
        if normalized and normalized not in values:
            values.append(normalized)
    text = " / ".join(values) if values else "无"
    return text if len(text) <= 120 else f"{text[:117]}…"


def _report_date(path: Path) -> str:
    try:
        return datetime.strptime(path.parent.name[:6], "%y%m%d").strftime("%Y-%m-%d")
    except ValueError:
        return datetime.now().strftime("%Y-%m-%d")


SITE_CODES = (("HK", "H"), ("TW", "T"), ("JP", "J"), ("TH", "A"), ("VN", "V"), ("RU", "R"))
SITE_CODE = dict(SITE_CODES)
SITE_ORDER = {code: index for index, (_site, code) in enumerate(SITE_CODES)}
VENDOR_CODE = {"BC": "B", "TGT": "G", "FR": "F", "JY": "Y", "TSIM 網址": "S", "其他": "O", "BC/TGT": "BG"}
# One Telegram text message is capped at 4096 characters; stay below it so a
# daily report is never split into a flood of messages.
SINGLE_MESSAGE_LIMIT = 3750
# Room kept for the closing "未列示 …" line so the final message never exceeds
# the budget because of its own tail.
TAIL_RESERVE = 80
LEGEND = (
    "缩写：站点 " + " ".join(f"{code}={site}" for site, code in SITE_CODES) +
    "｜厂商 " + " ".join(f"{code}={name}" for name, code in VENDOR_CODE.items()) +
    "｜折扣 5+50=5%&50%，- 表示无折扣"
)


def _discount_code(values: tuple[int, ...]) -> str:
    return "+".join(str(value) for value in values) if values else "-"


def _site_code(site: str) -> str:
    return SITE_CODE.get(site.upper(), "?")


def _vendor_code(name: str) -> str:
    return VENDOR_CODE.get(name.strip(), "?")


def _transition_lines(current, previous, today, limit: int) -> str:
    """Render one compact single-message report, ordered by business priority.

    Every line is `产品ID｜厂商｜站点｜旧折扣→新折扣`. Sections are emitted in
    priority order (撤销 → 力度变化 → 新增) and the renderer stops as soon as the
    next line would exceed the message budget, so the most actionable rows are
    always the ones that survive. Raw localized promotion labels stay in the
    companion workbook; this message never repeats them.
    """
    from collections import defaultdict

    changes: dict[str, dict[tuple[tuple[int, ...], tuple[int, ...]], set[str]]] = defaultdict(
        lambda: defaultdict(set)
    )
    for key in sorted(set(current) | set(previous)):
        pid, site = key
        now_label = current.get(key, ("", "", ""))[0]
        old_label = previous.get(key, ("", "", ""))[0]
        now_sig = discount_signature(now_label)
        old_sig = discount_signature(old_label)
        if now_sig != old_sig:
            changes[pid][(old_sig, now_sig)].add(site)

    rows = []
    changed_pairs: set[tuple[str, str]] = set()
    for pid, transitions in changes.items():
        vendor = _vendor_code(_product_vendor(pid, current, previous))
        for (old_sig, now_sig), sites in transitions.items():
            changed_pairs.update((pid, site) for site in sites)
            codes = sorted(_site_code(site) for site in sites)
            site_text = "".join(sorted(codes, key=lambda code: SITE_ORDER.get(code, 99)))
            rows.append(
                {
                    "pid": pid,
                    "vendor": vendor,
                    "sites": site_text,
                    "site_count": len(sites),
                    "transition": f"{_discount_code(old_sig)}→{_discount_code(now_sig)}",
                    "kind": "lost" if not now_sig else ("gain" if not old_sig else "shift"),
                }
            )

    by_kind = defaultdict(list)
    for row in rows:
        by_kind[row["kind"]].append(row)
    for group in by_kind.values():
        # Widest reach first, then product ID for stable, reproducible output.
        group.sort(key=lambda row: (-row["site_count"], not row["pid"].isdigit(), int(row["pid"]) if row["pid"].isdigit() else row["pid"]))

    # Site counts are changed (product, site) pairs, so a product changing on
    # three sites is counted once per site, not once per product.
    site_counts = Counter(site for _pid, site in changed_pairs)
    vendor_counts = Counter(_vendor_code(_product_vendor(pid, current, previous)) for pid in changes)

    header = [
        f"📊 携程 Mode2 六站促销标签变化｜{_report_date(today)}",
        f"变化 {len(changes)} 产品／{len(rows)} 处：撤销 {len(by_kind['lost'])}｜力度变化 {len(by_kind['shift'])}｜新增 {len(by_kind['gain'])}",
        "站点 " + " ".join(
            f"{_site_code(site)}{site_counts.get(site, 0)}"
            for site in sorted(site_counts, key=lambda s: SITE_ORDER.get(_site_code(s), 99))
        ),
        "厂商 " + " ".join(
            f"{code}{vendor_counts[code]}" for code in VENDOR_CODE.values() if vendor_counts.get(code)
        ),
        LEGEND,
        "",
    ]
    if not rows:
        return "\n".join(header[:3]) + "\n无实际促销折扣变化（仅文案或语言变化不计）。"

    sections = [
        ("▼撤销折扣", "lost", "撤销"),
        ("▼力度变化", "shift", "力度"),
        ("▼新增折扣", "gain", "新增"),
    ]
    lines = list(header)
    omitted = {}
    budget_stop = False
    row_budget = max(limit - TAIL_RESERVE, len("\n".join(header)))
    for title, kind, label in sections:
        group = by_kind.get(kind) or []
        if not group:
            omitted[label] = 0
            continue
        lines.append(f"{title} {len(group)}")
        shown = 0
        for row in group:
            line = f"{row['pid']}｜{row['vendor']}｜{row['sites']}｜{row['transition']}"
            if budget_stop or sum(len(item) + 1 for item in lines) + len(line) + 1 > row_budget:
                budget_stop = True
                break
            lines.append(line)
            shown += 1
        omitted[label] = len(group) - shown

    hidden = sum(omitted.values())
    if hidden:
        detail = "｜".join(f"{label} {count}" for label, count in omitted.items() if count)
        lines.append(f"（未列示 {hidden} 处：{detail}；完整明细见同日 XLSX 的「总促销标签」标红行）")
    return "\n".join(lines)


def promo_message(today: Path, previous: Path | None, limit: int = SINGLE_MESSAGE_LIMIT) -> str:
    """Build the compact single-message promotion-change report."""
    current = tags(today)
    yesterday = tags(previous) if previous else {}
    return _transition_lines(current, yesterday, today, limit)


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
            command = [sys.executable, str(DELIVER), str(output), "--to", TARGET, "--subject", "Trip Mode2 Daily"]
            if force:
                # A forced resend is an explicit operator action, so the
                # duplicate-safe guard must be bypassed deliberately.
                command.append("--force")
            result = subprocess.run(command, capture_output=True, text=True)
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
            if len(message) <= 4000:
                send(message)
                chunks = [message]
            else:
                # Safety valve only: the renderer is budgeted for one message.
                chunks = split_plaintext(message)
                for index, chunk in enumerate(chunks, start=1):
                    send(f"（{index}/{len(chunks)}）\n{chunk}")
            promo_done.write_text(json.dumps({
                "reported_at": datetime.now().isoformat(),
                "target": TARGET,
                "product_count": message.count("\n• "),
                "chunks": len(chunks),
            }))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
