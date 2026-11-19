from __future__ import annotations

import importlib.util
import json
import os
import signal
import subprocess
import sys
import time
from pathlib import Path


ROOT = Path(__file__).parents[1]
SCRIPT = ROOT / "scripts/hermes-cron/trip_mode2_launcher_state.py"
SPEC = importlib.util.spec_from_file_location("mode2_launcher_state", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
LOCK_GUARD = ROOT / "scripts/hermes-cron/trip_mode2_lock_guard.py"
GUARD_SPEC = importlib.util.spec_from_file_location("mode2_lock_guard", LOCK_GUARD)
assert GUARD_SPEC and GUARD_SPEC.loader
GUARD = importlib.util.module_from_spec(GUARD_SPEC)
GUARD_SPEC.loader.exec_module(GUARD)


def test_terminal_state_preserves_launch_fields_and_records_stage(tmp_path: Path) -> None:
    (tmp_path / "launcher_state.json").write_text(json.dumps({"pid": 17, "status": "running"}))
    MODULE.update(tmp_path, "delivery_failed", "delivery", 23, "投递阶段异常退出")
    state = json.loads((tmp_path / "launcher_state.json").read_text())
    assert state["pid"] == 17
    assert state["status"] == "delivery_failed"
    assert state["business_status"] == "failed"
    assert state["business_complete"] is False
    assert state["stage"] == "delivery"
    assert state["exit_code"] == 23
    assert state["reason"] == "投递阶段异常退出"
    assert state["terminal_at"]


def test_completed_terminal_state_marks_business_complete(tmp_path: Path) -> None:
    MODULE.update(tmp_path, "completed", "delivery", 0)
    state = json.loads((tmp_path / "launcher_state.json").read_text())
    assert state["status"] == "completed"
    assert state["business_status"] == "complete"
    assert state["business_complete"] is True


def test_empty_valid_worker_recovery_logs_and_reaches_its_intended_failure(tmp_path: Path) -> None:
    """Bash 3.2 + nounset must allow an all-retry recovery progress log."""
    collector, env, root = _isolated_collector(tmp_path, 99)
    run = root / "260102-mode2-daily"
    (run / "target").mkdir(parents=True)
    (run / "target/provenance.json").write_text("{}")
    (run / "mode2_target_urls.txt").write_text("https://hk.trip.com/things-to-do/detail/100/\n")
    (run / "mode2_target.env").write_text(
        "MODE2_EXPECTED_URLS=1\nMODE2_EXPECTED_ROWS=2\nMODE2_TARGET_SHA256=test\n"
    )
    (run / "w1").mkdir()
    helper = tmp_path / "resume.py"
    helper.write_text(
        "import json, sys\n"
        "if sys.argv[1] == 'inspect-workers': print(json.dumps({'valid': [], 'retry': [1, 2, 3, 4, 5]}))\n"
        "raise SystemExit(0)\n"
    )
    collector.write_text(collector.read_text().replace(
        f'RESUME_HELPER="{ROOT / "scripts/hermes-cron/trip_mode2_resume.py"}"',
        f'RESUME_HELPER="{helper}"',
    ))
    result = subprocess.run(
        ["/bin/bash", str(collector)],
        env=env | {"MODE2_NOTIFY_DISABLED": "1", "MODE2_FINALIZE_ONLY": "1"},
        capture_output=True,
        text=True,
    )
    assert result.returncode == 1
    assert "MODE2_FINALIZE_ONLY requires complete validated worker evidence" in result.stderr
    assert "unbound variable" not in result.stderr


def test_collector_contract_has_owned_lock_and_retryable_notification_marker() -> None:
    collector = (ROOT / "scripts/hermes-cron/trip_daily_mode2_cron.sh").read_text(encoding="utf-8")
    assert 'LOCK_OWNED=1' in collector
    assert '"$LOCK_DIR/owner"' in collector
    assert 'release_owned_lock' in collector
    # Marker is written only after the JSON-confirmed send branch, never before it.
    assert collector.index('hermes send --json') < collector.index('touch "$NOTIFY_MARKER"')
    assert 'status="collection_failed"' in collector and 'status="delivery_failed"' in collector


def _isolated_collector(tmp_path: Path, target_exit: int) -> tuple[Path, dict, Path]:
    project = tmp_path / "project"
    (project / "scripts/hermes-cron").mkdir(parents=True)
    (project / "scripts/hermes-cron/trip_mode2_launcher_state.py").write_bytes(SCRIPT.read_bytes())
    (project / "scripts/hermes-cron/trip_mode2_resume.py").write_bytes(
        (ROOT / "scripts/hermes-cron/trip_mode2_resume.py").read_bytes()
    )
    (project / "scripts/hermes-cron/trip_mode2_lock_guard.py").write_bytes(
        (ROOT / "scripts/hermes-cron/trip_mode2_lock_guard.py").read_bytes()
    )
    target = project / "scripts/daily_mode2_google_target_sync.py"
    target.write_text(f"raise SystemExit({target_exit})\n")
    source = (ROOT / "scripts/hermes-cron/trip_daily_mode2_cron.sh").read_text()
    source = source.replace('PROJECT_ROOT="${TRIP_PROJECT_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}"', f'PROJECT_ROOT="{project}"')
    source = source.replace('RESUME_HELPER="$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_resume.py"', f'RESUME_HELPER="{ROOT / "scripts/hermes-cron/trip_mode2_resume.py"}"')
    source = source.replace('PYTHON="/usr/local/bin/python3"', f'PYTHON="{sys.executable}"')
    source = source.replace('LOCK_DIR="/tmp/trip_mode2_daily.lock"', f'LOCK_DIR="{tmp_path / "lock"}"')
    collector = tmp_path / "collector.sh"; collector.write_text(source); collector.chmod(0o755)
    session = tmp_path / "session.json"; session.write_text("{}")
    root = tmp_path / "runs"
    return collector, os.environ | {"TRIP_CLI_SESSION_FILE": str(session), "TRIP_MODE2_ROOT": str(root), "TRIP_MODE2_DATE": "260102", "TRIP_MODE2_LOCK_TEST_STARTED": "test-start", "TRIP_DELIVERY_TARGET": "telegram:offline_test_chat"}, root


def test_isolated_collector_preserves_exit_state_owner_and_notice_receipt(tmp_path: Path) -> None:
    collector, env, root = _isolated_collector(tmp_path, 23)
    result = subprocess.run(["/bin/bash", str(collector)], env=env | {"MODE2_NOTIFY_DISABLED": "1"}, capture_output=True, text=True)
    run = root / "260102-mode2-daily"
    state = json.loads((run / "launcher_state.json").read_text())
    assert result.returncode == 23 and state["status"] == "collection_failed" and state["exit_code"] == 23
    assert state["reason"] == "目标表同步失败，请查看采集日志" and not (tmp_path / "lock").exists()
    lock = tmp_path / "lock"; lock.mkdir(); (lock / "owner").write_text("someone-else")
    assert subprocess.run(["/bin/bash", str(collector)], env=env | {"MODE2_NOTIFY_DISABLED": "1"}).returncode == 75
    assert (lock / "owner").read_text() == "someone-else"

    for path in (tmp_path / "bad", tmp_path / "good"):
        path.mkdir()
    fake_bin = tmp_path / "bin"; fake_bin.mkdir(); hermes = fake_bin / "hermes"
    bad, bad_env, bad_root = _isolated_collector(tmp_path / "bad", 1)
    hermes.write_text("#!/bin/bash\nexit 1\n"); hermes.chmod(0o755)
    assert subprocess.run(["/bin/bash", str(bad)], env=bad_env | {"PATH": f"{fake_bin}:{bad_env['PATH']}"}).returncode == 1
    assert not (bad_root / "260102-mode2-daily/.failure_notified").exists()
    good, good_env, good_root = _isolated_collector(tmp_path / "good", 1)
    hermes.write_text("#!/bin/bash\nprintf '{\"success\":true,\"message_id\":\"ok\"}\\n'\n"); hermes.chmod(0o755)
    assert subprocess.run(["/bin/bash", str(good)], env=good_env | {"PATH": f"{fake_bin}:{good_env['PATH']}"}).returncode == 1
    assert (good_root / "260102-mode2-daily/.failure_notified").exists()


def test_collector_recovers_only_proven_stale_lock_without_collection(tmp_path: Path) -> None:
    collector, env, _ = _isolated_collector(tmp_path, 99)
    lock = tmp_path / "lock"
    lock.mkdir()
    # A versioned record for a PID that cannot exist is safe to quarantine.
    (lock / "owner").write_text(
        "version=1\ntoken=crashed\npid=999999\nstarted=Mon Jan  1 00:00:00 2001\n"
    )
    result = subprocess.run(
        ["/bin/bash", str(collector)],
        env=env | {"MODE2_NOTIFY_DISABLED": "1", "TRIP_MODE2_LOCK_TEST_HOLD_SECONDS": "0"},
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0
    assert not lock.exists()
    assert not list(tmp_path.glob("lock.stale.*"))


def test_collector_never_steals_live_or_unattributable_lock(tmp_path: Path) -> None:
    collector, env, _ = _isolated_collector(tmp_path, 99)
    lock = tmp_path / "lock"
    lock.mkdir()
    (lock / "owner").write_text(f"version=1\ntoken=live\npid={os.getpid()}\nstarted=test-start\n")
    result = subprocess.run(["/bin/bash", str(collector)], env=env | {"MODE2_NOTIFY_DISABLED": "1"}, capture_output=True, text=True)
    assert result.returncode == 75
    assert (lock / "owner").read_text().splitlines()[1] == "token=live"

    # A live but differently-started PID is PID reuse, not the old owner.
    (lock / "owner").write_text(f"version=1\ntoken=old\npid={os.getpid()}\nstarted=older-start\n")
    result = subprocess.run(
        ["/bin/bash", str(collector)],
        env=env | {"MODE2_NOTIFY_DISABLED": "1", "TRIP_MODE2_LOCK_TEST_HOLD_SECONDS": "0"},
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0 and not lock.exists()

    # A legacy/incomplete record has no reliable process identity, so it stays
    # locked rather than being guessed stale.
    lock.mkdir()
    (lock / "owner").write_text("legacy-token\n")
    result = subprocess.run(["/bin/bash", str(collector)], env=env | {"MODE2_NOTIFY_DISABLED": "1"}, capture_output=True, text=True)
    assert result.returncode == 75
    assert (lock / "owner").read_text() == "legacy-token\n"


def test_lock_guard_distinguishes_dead_owner_from_kill_permission_error(monkeypatch) -> None:
    record = {"pid": "999999", "started": "old"}

    def dead(_pid: int, _signal: int) -> None:
        raise ProcessLookupError

    monkeypatch.setattr(GUARD.os, "kill", dead)
    assert GUARD.is_proven_stale(record) is True

    def forbidden(_pid: int, _signal: int) -> None:
        raise PermissionError

    monkeypatch.setattr(GUARD.os, "kill", forbidden)
    assert GUARD.is_proven_stale(record) is False


def test_collector_concurrent_startup_has_one_owner(tmp_path: Path) -> None:
    collector, env, _ = _isolated_collector(tmp_path, 99)
    held_env = env | {"MODE2_NOTIFY_DISABLED": "1", "TRIP_MODE2_LOCK_TEST_HOLD_SECONDS": "2"}
    first = subprocess.Popen(["/bin/bash", str(collector)], env=held_env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    lock = tmp_path / "lock"
    for _ in range(80):
        if (lock / "owner").exists():
            break
        time.sleep(0.025)
    assert (lock / "owner").exists()
    second = subprocess.run(["/bin/bash", str(collector)], env=env | {"MODE2_NOTIFY_DISABLED": "1"}, capture_output=True, text=True)
    assert second.returncode == 75
    assert first.wait(timeout=5) == 0
    assert not lock.exists()


def test_concurrent_stale_recovery_has_exactly_one_owner(tmp_path: Path) -> None:
    """All contenders start with the same dead owner, not an empty lock."""
    collector, env, _ = _isolated_collector(tmp_path, 99)
    lock = tmp_path / "lock"
    lock.mkdir()
    (lock / "owner").write_text(
        "version=1\ntoken=crashed\npid=999999\nstarted=Mon Jan  1 00:00:00 2001\n"
    )
    hold = env | {"MODE2_NOTIFY_DISABLED": "1", "TRIP_MODE2_LOCK_TEST_HOLD_SECONDS": "1"}
    contenders = [
        subprocess.Popen(["/bin/bash", str(collector)], env=hold, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        for _ in range(4)
    ]
    codes = [process.wait(timeout=6) for process in contenders]
    assert codes.count(0) == 1
    assert codes.count(75) == len(codes) - 1
    assert not lock.exists()


def test_collector_term_stops_only_its_owned_worker_and_records_143(tmp_path: Path) -> None:
    collector, env, root = _isolated_collector(tmp_path, 0)
    project = tmp_path / "project"
    target = project / "scripts/daily_mode2_google_target_sync.py"
    target.write_text("""import sys, json, hashlib
import openpyxl
from pathlib import Path
a=sys.argv
run=Path(a[a.index('--run-dir')+1]); target_dir=run/'target'; target_dir.mkdir(parents=True)
book=target_dir/'日常抓取目标.xlsx'; wb=openpyxl.Workbook(); ws=wb.active; ws.append(['TSIM 網址','BC','TGT','FR','JY','其他']); ws.append(['100',None,None,None,None,None]); wb.save(book); wb.close()
digest=hashlib.sha256(book.read_bytes()).hexdigest()
(target_dir/'provenance.json').write_text(json.dumps({'sha256':digest,'product_ids':['100'],'sites':['hk','ru'],'url_count':1,'site_count':2,'expected_rows':2,'staged_workbook':str(book)}))
Path(a[a.index('--output-urls')+1]).write_text('https://hk.trip.com/things-to-do/detail/100/\\n')
Path(a[a.index('--env-file')+1]).write_text('MODE2_EXPECTED_URLS=1\\nMODE2_EXPECTED_SITES=2\\nMODE2_EXPECTED_ROWS=2\\nMODE2_TARGET_SHA256='+digest+'\\n')
""")
    binary = project / "trip-score-price-scraper"
    pid_file = tmp_path / "worker.pid"
    binary.write_text(f"#!/bin/bash\n[ \"$1\" = --help ] && exit 0\necho $$ > {pid_file}\nexec sleep 30\n"); binary.chmod(0o755)
    fake_go = project / "go"; fake_go.write_text("#!/bin/bash\nexit 0\n"); fake_go.chmod(0o755)
    collector.write_text(collector.read_text().replace('/opt/homebrew/bin/go', str(fake_go)))
    proc = subprocess.Popen(["/bin/bash", str(collector)], env=env | {"MODE2_WORKERS": "1", "MODE2_NOTIFY_DISABLED": "1"})
    for _ in range(80):
        if pid_file.exists():
            break
        time.sleep(0.05)
    assert pid_file.exists()
    child = int(pid_file.read_text())
    proc.send_signal(signal.SIGTERM)
    assert proc.wait(timeout=8) == 143
    with __import__('pytest').raises(ProcessLookupError):
        os.kill(child, 0)
    state = json.loads((root / "260102-mode2-daily/launcher_state.json").read_text())
    assert state["exit_code"] == 143 and state["status"] == "collection_failed"


def test_fake_collector_never_touches_legacy_global_chunks(tmp_path: Path) -> None:
    """Regression guard: tests and production chunks are run-local, never /tmp shared names."""
    sentinel = Path("/tmp/trip_mode2_chunk_1.txt")
    before = sentinel.read_bytes() if sentinel.exists() else None
    collector, env, _ = _isolated_collector(tmp_path, 23)
    assert subprocess.run(["/bin/bash", str(collector)], env=env | {"MODE2_NOTIFY_DISABLED": "1"}).returncode == 23
    assert (sentinel.read_bytes() if sentinel.exists() else None) == before
