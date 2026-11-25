#!/usr/bin/env python3
"""
trip_deliver.py — 携程 SKU/商品信息抓取「结果一定不漏发」投递兜底（方案 B）

用法（抓取任务可执行后由调用方固定调用）：
    python3 trip_deliver.py <run_done_jsonl_or_output_path> --to telegram:<chat_id> [--subject TEXT]

保证机制：
1. 从 CLI 的 run_done JSONL 事件（或直接给 output_path）解析最终输出文件。
2. 校验输出文件存在且非空。
3. 用 `hermes send --json --to <target> "MEDIA:<path>"` 把原生 XLSX 发送到发起聊天。
4. Telegram 仅在 exit 0 且 JSON 服务回执确认实际送达时写入 .delivered 成功回执并 exit 0。
5. 任何失败（缺文件/空文件/hermes send 非 0/未验证回执）→ 写 .pending_delivery 标记、返回非零，
   让调用方/上层把该任务标记为「失败待补发」，绝不静默当作成功。

6. `--force` 显式覆盖重复投递保护（仅用于操作者确认的「修正后重发同一 run」）。

退出码：0=已送达；1=投递失败/后端错误；2=用法错误。
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

STABILIZER = Path(__file__).resolve().parent / "scripts" / "stabilize_vendor_attribution.py"


def _stabilize_vendor_attribution(output_path: Path) -> None:
    """Best-effort attribution stabilization; never send an unverified failure as success."""
    if output_path.suffix.lower() != ".xlsx":
        return
    if not STABILIZER.exists():
        raise RuntimeError(f"vendor stabilizer missing: {STABILIZER}")
    result = subprocess.run(
        ["/usr/local/bin/python3", str(STABILIZER), str(output_path)],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip() or "vendor stabilizer failed")


def _find_output_path(run_source: str, run_dir: Path) -> Path | None:
    p = Path(run_source)
    if p.exists():
        return p
    jsonl = run_dir / "terminal_output.txt"
    if jsonl.exists():
        for line in jsonl.read_text(encoding="utf-8", errors="replace").splitlines():
            try:
                ev = json.loads(line)
            except (ValueError, TypeError):
                continue
            if ev.get("event") == "run_done" and ev.get("output_path"):
                return Path(ev["output_path"])
            if ev.get("output_path"):
                return Path(ev["output_path"])
    return None


def _write_marker(run_dir: Path, payload: dict) -> None:
    """Write a marker file robustly; never raise even if run_dir is missing."""
    marker = run_dir / ".pending_delivery.json"
    try:
        marker.parent.mkdir(parents=True, exist_ok=True)
        marker.write_text(json.dumps(payload))
    except OSError:
        # run_dir unwritable/missing parent — still surface a marker in /tmp.
        fallback = Path("/tmp") / (
            f"pending_delivery_{abs(hash(str(run_dir)))}.json"
        )
        try:
            payload["marker"] = str(marker)
            fallback.write_text(json.dumps(payload))
        except OSError:
            pass


def _already_delivered(run_dir: Path, output_path: Path, target: str) -> bool:
    """Idempotency guard for duplicate manual-run delivery invocations."""
    try:
        payload = json.loads((run_dir / ".delivered.json").read_text(encoding="utf-8"))
    except (OSError, ValueError, TypeError):
        return False
    return (
        payload.get("status") == "delivered"
        and payload.get("path") == str(output_path)
        and payload.get("target") == target
    )


def _target_parts(target: str) -> tuple[str, str, str | None]:
    platform, separator, remainder = target.partition(":")
    if not separator:
        return platform, "", None
    chat_id, separator, thread_id = remainder.partition(":")
    return platform, chat_id, thread_id if separator else None


def _receipt_field(value: object, name: str) -> object | None:
    """Find a Hermes JSON response field, including its result/target wrappers."""
    if isinstance(value, dict):
        if name in value:
            return value[name]
        for child in value.values():
            found = _receipt_field(child, name)
            if found is not None:
                return found
    elif isinstance(value, list):
        for child in value:
            found = _receipt_field(child, name)
            if found is not None:
                return found
    return None


def _receipt_has_problem(value: object) -> bool:
    if isinstance(value, dict):
        for key, child in value.items():
            if key in {"error", "errors", "warning", "warnings"} and child:
                return True
            if key == "skipped" and child is True:
                return True
            if _receipt_has_problem(child):
                return True
    elif isinstance(value, list):
        return any(_receipt_has_problem(child) for child in value)
    return False


def _write_transport_receipt(output_path: Path, raw_receipt: str) -> None:
    receipt_path = output_path.parent / "delivery_transport_receipt.json"
    fd, temporary_path = tempfile.mkstemp(
        prefix=f".{receipt_path.name}.", dir=receipt_path.parent, text=True
    )
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(raw_receipt)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary_path, receipt_path)
    except BaseException:
        Path(temporary_path).unlink(missing_ok=True)
        raise


def _verify_telegram_receipt(target: str, raw_receipt: str) -> bool:
    try:
        receipt = json.loads(raw_receipt)
    except (ValueError, TypeError):
        return False
    if not isinstance(receipt, dict) or receipt.get("success") is not True:
        return False
    if _receipt_has_problem(receipt):
        return False
    platform, chat_id, requested_thread_id = _target_parts(target)
    if str(_receipt_field(receipt, "platform") or "") != platform:
        return False
    if str(_receipt_field(receipt, "chat_id") or "") != chat_id:
        return False
    response_thread_id = _receipt_field(receipt, "thread_id")
    if requested_thread_id is not None and str(response_thread_id or "") != requested_thread_id:
        return False
    message_id = _receipt_field(receipt, "message_id")
    return message_id is not None and str(message_id).strip() != ""


def _deliver(target: str, output_path: Path, subject: str | None) -> int:
    body = f"MEDIA:{output_path}"
    cmd = ["hermes", "send", "--json", "--to", target]
    if subject:
        cmd += ["--subject", subject]
    cmd.append(body)
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        sys.stderr.write(f"hermes send failed rc={result.returncode}: {result.stderr}\n")
        return 1
    if not target.startswith("telegram:"):
        return 0
    if not _verify_telegram_receipt(target, result.stdout):
        sys.stderr.write("hermes send returned an unverified Telegram delivery receipt\n")
        return 1
    try:
        _write_transport_receipt(output_path, result.stdout)
    except OSError as exc:
        sys.stderr.write(f"could not save verified Telegram delivery receipt: {exc}\n")
        return 1
    return 0


def main(argv: list[str]) -> int:
    if len(argv) < 2 or "--to" not in argv:
        print(
            "usage: trip_deliver.py <output.xlsx | run_dir> --to telegram:<chat_id> [--subject TEXT] [--force]",
            file=sys.stderr,
        )
        return 2
    run_source = argv[0]
    target = None
    subject = None
    force = False
    i = 1
    while i < len(argv):
        if argv[i] == "--to" and i + 1 < len(argv):
            target = argv[i + 1]
            i += 2
        elif argv[i] == "--subject" and i + 1 < len(argv):
            subject = argv[i + 1]
            i += 2
        elif argv[i] == "--force":
            # Explicit operator override for a corrected re-send of the same
            # run. Never implied: the default stays duplicate-safe.
            force = True
            i += 1
        else:
            i += 1
    if not target:
        print("missing --to target", file=sys.stderr)
        return 2

    run_dir = Path(run_source) if Path(run_source).is_dir() else Path(run_source).parent
    output_path = _find_output_path(run_source, run_dir)
    if output_path is None:
        print(f"cannot locate output workbook from {run_source!r}", file=sys.stderr)
        _write_marker(run_dir, {"error": "no_output_path"})
        return 1
    if not output_path.exists() or output_path.stat().st_size == 0:
        print(f"output workbook missing/empty: {output_path}", file=sys.stderr)
        _write_marker(run_dir, {"error": "empty_or_missing", "path": str(output_path)})
        return 1

    if not force and _already_delivered(run_dir, output_path, target):
        print(f"already delivered: {output_path} -> {target}", file=sys.stderr)
        return 0

    try:
        _stabilize_vendor_attribution(output_path)
    except RuntimeError as exc:
        print(f"vendor attribution stabilization failed: {exc}", file=sys.stderr)
        _write_marker(run_dir, {"error": "vendor_stabilization_failed", "path": str(output_path)})
        return 1

    rc = _deliver(target, output_path, subject)
    if rc == 0:
        (run_dir / ".delivered.json").write_text(
            json.dumps({"status": "delivered", "path": str(output_path), "target": target})
        )
        (run_dir / ".pending_delivery.json").unlink(missing_ok=True)
        return 0
    _write_marker(run_dir, {"status": "pending", "path": str(output_path), "target": target})
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
