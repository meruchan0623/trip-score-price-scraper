#!/usr/bin/env python3
"""Small atomic terminal-state writer used by the detached Mode2 collector."""
from __future__ import annotations

import argparse
import json
import os
import tempfile
from datetime import datetime
from pathlib import Path


def update(run_dir: Path, status: str, stage: str, exit_code: int, reason: str = "") -> None:
    path = run_dir / "launcher_state.json"
    try:
        current = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        current = {}
    current.update({
        "status": status,
        "stage": stage,
        "exit_code": int(exit_code),
        "terminal_at": datetime.now().astimezone().isoformat(),
        # Keep terminal collector status compatible while making business
        # completion independently auditable from detached-launch success.
        "business_status": "complete" if status == "completed" else "failed",
        "business_complete": status == "completed",
    })
    current.setdefault("launch_status", "unknown")
    if reason:
        current["reason"] = reason[:160]
    else:
        current.pop("reason", None)
    fd, temporary = tempfile.mkstemp(prefix=".launcher_state.", suffix=".tmp", dir=run_dir)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(current, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    except BaseException:
        Path(temporary).unlink(missing_ok=True)
        raise


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--run-dir", type=Path, required=True)
    parser.add_argument("--status", choices=("completed", "collection_failed", "delivery_failed"), required=True)
    parser.add_argument("--stage", required=True)
    parser.add_argument("--exit-code", type=int, required=True)
    parser.add_argument("--reason", default="")
    args = parser.parse_args(argv)
    update(args.run_dir, args.status, args.stage, args.exit_code, args.reason)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
