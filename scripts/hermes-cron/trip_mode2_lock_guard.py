#!/usr/bin/env python3
"""Atomically recover and acquire the Mode2 directory lock.

The advisory flock serializes stale-owner inspection, quarantine, and creation
of the replacement lock directory.  It intentionally treats incomplete legacy
records and any inability to inspect an owner as busy.
"""

from __future__ import annotations

import argparse
import errno
import fcntl
import os
import subprocess
import sys
from pathlib import Path


BUSY = 75


def owner(path: Path) -> dict[str, str] | None:
    try:
        values = {}
        for line in (path / "owner").read_text(encoding="utf-8").splitlines():
            key, separator, value = line.partition("=")
            if not separator or key in values:
                return None
            values[key] = value
    except OSError:
        return None
    if values.get("version") != "1" or not values.get("token"):
        return None
    if not values.get("pid", "").isdigit() or int(values["pid"]) < 1:
        return None
    if not values.get("started"):
        return None
    return values


def process_start(pid: int) -> str | None:
    # Test harnesses can provide a deterministic process start identity; the
    # production environment never sets this variable.
    if os.environ.get("TRIP_MODE2_LOCK_TEST_STARTED"):
        return os.environ["TRIP_MODE2_LOCK_TEST_STARTED"]
    completed = subprocess.run(
        ["/bin/ps", "-p", str(pid), "-o", "lstart="],
        stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        text=True, check=False,
    )
    value = completed.stdout.strip()
    return value or None


def is_proven_stale(record: dict[str, str]) -> bool:
    pid = int(record["pid"])
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return True
    except PermissionError:
        # EPERM says a process exists but is not ours to inspect: never steal.
        return False
    except OSError:
        return False
    # A live PID with an unreadable start time is ambiguous.  Only a verified
    # mismatch proves PID reuse and therefore a dead recorded owner.
    actual = process_start(pid)
    return actual is not None and actual != record["started"]


def write_owner(lock_dir: Path, args: argparse.Namespace) -> None:
    # The candidate directory is private until it is atomically renamed into
    # place, and the record itself is atomically published within it.
    temporary = lock_dir / ".owner.new"
    temporary.write_text(
        f"version=1\ntoken={args.token}\npid={args.pid}\nstarted={args.started}\n",
        encoding="utf-8",
    )
    os.replace(temporary, lock_dir / "owner")


def acquire(args: argparse.Namespace) -> int:
    lock_dir = Path(args.lock_dir)
    guard_path = Path(f"{args.lock_dir}.recovery.guard")
    guard_path.parent.mkdir(parents=True, exist_ok=True)
    with guard_path.open("a+") as guard:
        fcntl.flock(guard.fileno(), fcntl.LOCK_EX)
        try:
            if lock_dir.exists():
                record = owner(lock_dir)
                if record is None or not is_proven_stale(record):
                    return BUSY
                stale_dir = Path(f"{args.lock_dir}.stale.{args.token}")
                try:
                    os.rename(lock_dir, stale_dir)
                except FileNotFoundError:
                    return BUSY
                # Only expected owner data is disposable. Preserve other stale
                # contents as forensic evidence without blocking new ownership.
                try:
                    (stale_dir / "owner").unlink()
                    stale_dir.rmdir()
                except OSError:
                    pass
            # Never expose an empty canonical directory.  If this process
            # crashes before the rename, only a token-scoped candidate remains;
            # a later contender can safely acquire the still-absent canonical
            # path.  Once renamed, the complete owner record is present.
            candidate = lock_dir.parent / f".{lock_dir.name}.acquiring.{args.token}"
            try:
                candidate.mkdir()
            except FileExistsError:
                return BUSY
            try:
                write_owner(candidate, args)
                os.rename(candidate, lock_dir)
            except OSError:
                # Do not remove candidate evidence after an interrupted or
                # failed publish. It is outside the canonical lock path.
                return 1
            return 0
        finally:
            fcntl.flock(guard.fileno(), fcntl.LOCK_UN)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--lock-dir", required=True)
    parser.add_argument("--token", required=True)
    parser.add_argument("--pid", required=True, type=int)
    parser.add_argument("--started", required=True)
    return acquire(parser.parse_args())


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except OSError as error:
        print(f"mode2 lock guard failed: {error}", file=sys.stderr)
        raise SystemExit(1)
