from __future__ import annotations

import importlib.util
import json
import os
import stat
import subprocess
import sys
from pathlib import Path

SCRIPT = Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_mode2_daily_launcher.py"
SPEC = importlib.util.spec_from_file_location("trip_mode2_daily_launcher", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)

DATE = "260919"


class FakePopen:
    """Records Popen kwargs; never starts anything."""

    calls: list[dict] = []

    def __init__(self, cmd, **kwargs):
        self.pid = 424242
        FakePopen.calls.append({"cmd": cmd, **kwargs})


def _fake_collector(tmp_path: Path) -> Path:
    p = tmp_path / "fake_collector.sh"
    p.write_text("#!/bin/bash\nexit 0\n", encoding="utf-8")
    return p


def _argv(tmp_path: Path, collector: Path, *extra: str) -> list[str]:
    return ["--collector", str(collector), "--root", str(tmp_path / "root"), "--date", DATE, *extra]


def setup_function():
    FakePopen.calls = []


def test_launch_detached_and_persists_state(tmp_path):
    collector = _fake_collector(tmp_path)
    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen, now=lambda: 1_700_000_000.0)

    assert result["result"] == "launched" and result["ok"] is True
    assert result["launch_status"] == "launched"
    assert result["business_status"] == "in_progress"
    assert result["business_complete"] is False
    assert result["pid"] == 424242
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    assert result["run_dir"] == str(run_dir)

    assert len(FakePopen.calls) == 1
    call = FakePopen.calls[0]
    assert call["cmd"] == ["/bin/bash", str(collector)]
    assert call["start_new_session"] is True
    assert call["env"]["TRIP_MODE2_DATE"] == DATE
    assert call["env"]["TRIP_MODE2_ROOT"] == str((tmp_path / "root").resolve())
    assert call["close_fds"] is True
    assert call["stdout"] != subprocess.PIPE and call["stderr"] != subprocess.PIPE
    assert isinstance(call["stdout"], int) and call["stderr"] == call["stdout"]
    assert (run_dir / MODULE.LOG_FILE).exists()

    state_path = run_dir / MODULE.STATE_FILE
    state = json.loads(state_path.read_text(encoding="utf-8"))
    assert state["status"] == "running"
    assert state["launch_status"] == "launched"
    assert state["business_status"] == "in_progress"
    assert state["business_complete"] is False
    assert state["pid"] == 424242
    assert state["collector"] == str(collector)
    assert state["run_dir"] == str(run_dir)
    assert state["started_at"]
    assert stat.S_IMODE(state_path.stat().st_mode) == 0o600
    assert not list(run_dir.glob(".*.tmp"))


def test_skips_when_collection_done(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    (run_dir / "x_trip_mode2_daily_2site.xlsx").write_bytes(b"x")
    (run_dir / ".collection_done").write_text(json.dumps({"sites": "hk,ru", "scope": "hk_ru_2site", "output_path": str(run_dir / "x_trip_mode2_daily_2site.xlsx")}), encoding="utf-8")
    (run_dir / ".delivery_done").write_text(json.dumps({"scope": "hk_ru_2site", "source_path": str(run_dir / "x_trip_mode2_daily_2site.xlsx"), "target":"", "completed_at":"x", "artifacts":{"report_xlsx":"ok"}, "messages":{"0":"ok"}}), encoding="utf-8")

    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)

    assert result["result"] == "skipped_done" and result["ok"] is True
    assert result["launch_status"] == "skipped_done"
    assert result["business_status"] == "complete"
    assert result["business_complete"] is True
    assert FakePopen.calls == []
    assert not (run_dir / MODULE.STATE_FILE).exists()


def test_completed_collection_without_delivery_receipt_resumes_without_rescrape(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    output = run_dir / "x_trip_mode2_daily_2site.xlsx"
    output.write_bytes(b"x")
    (run_dir / ".collection_done").write_text(json.dumps({"sites": "hk,ru", "scope": "hk_ru_2site", "output_path": str(output)}), encoding="utf-8")
    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)
    assert result["result"] == "launched"
    state = json.loads((run_dir / MODULE.STATE_FILE).read_text())
    assert state["resume_delivery_only"] is True

def test_incomplete_or_pending_delivery_receipt_resumes_without_rescrape(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"; run_dir.mkdir(parents=True)
    output = run_dir / "x_trip_mode2_daily_2site.xlsx"; output.write_bytes(b"x")
    (run_dir / ".collection_done").write_text(json.dumps({"sites":"hk,ru","scope":"hk_ru_2site","output_path":str(output)}))
    (run_dir / ".delivery_done").write_text(json.dumps({"scope":"hk_ru_2site","source_path":str(output),"target":"","artifacts":{"report_xlsx":"ok"},"messages":{}}))
    (run_dir / ".pending_delivery.json").write_text("{}")
    result=MODULE.run(_argv(tmp_path,collector),popen=FakePopen)
    assert result["result"]=="launched" and json.loads((run_dir/MODULE.STATE_FILE).read_text())["resume_delivery_only"] is True


def test_legacy_six_site_marker_does_not_suppress_replacement_scope(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    (run_dir / ".collection_done").write_text(json.dumps({"sites": "tw,hk,ru,jp,vn,th", "output_path": "old_6site.xlsx"}))
    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)
    assert result["result"] == "launched"


def test_already_running_when_pid_live(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    MODULE.write_state_atomic(run_dir, {"status": "running", "pid": os.getpid(), "started_at": "x"})

    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)

    assert result["result"] == "already_running" and result["ok"] is True
    assert result["launch_status"] == "already_running"
    assert result["business_status"] == "in_progress"
    assert result["business_complete"] is False
    assert result["pid"] == os.getpid()
    assert FakePopen.calls == []


def test_stale_pid_is_replaced(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    # Spawn and reap a real process so the PID is guaranteed dead.
    dead = subprocess.Popen([sys.executable, "-c", "pass"])
    dead.wait()
    MODULE.write_state_atomic(run_dir, {"status": "running", "pid": dead.pid, "started_at": "old"})

    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)

    assert result["result"] == "launched"
    assert len(FakePopen.calls) == 1
    state = json.loads((run_dir / MODULE.STATE_FILE).read_text(encoding="utf-8"))
    assert state["pid"] == 424242
    assert state["replaced_stale"]["pid"] == dead.pid


def test_old_terminal_state_is_replaced_with_new_running_pid(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    MODULE.write_state_atomic(run_dir, {"status": "collection_failed", "pid": 17, "exit_code": 23})

    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)

    state = json.loads((run_dir / MODULE.STATE_FILE).read_text())
    assert result["result"] == "launched"
    assert state["status"] == "running" and state["pid"] == 424242
    assert state["replaced_stale"]["pid"] == 17


def test_dry_run_does_not_launch_or_write_pid(tmp_path):
    collector = _fake_collector(tmp_path)
    result = MODULE.run(_argv(tmp_path, collector, "--dry-run"), popen=FakePopen)

    assert result["result"] == "dry_run" and result["ok"] is True
    assert result["command"] == ["/bin/bash", str(collector)]
    assert FakePopen.calls == []
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    assert not (run_dir / MODULE.STATE_FILE).exists()


def test_missing_collector_fails_without_state(tmp_path):
    missing = tmp_path / "nope.sh"
    result = MODULE.run(_argv(tmp_path, missing), popen=FakePopen)

    assert result["result"] == "collector_missing" and result["ok"] is False
    assert FakePopen.calls == []
    assert not (tmp_path / "root" / f"{DATE}-mode2-daily" / MODULE.STATE_FILE).exists()


def test_env_overrides_apply(tmp_path, monkeypatch):
    collector = _fake_collector(tmp_path)
    monkeypatch.setenv("TRIP_MODE2_COLLECTOR", str(collector))
    monkeypatch.setenv("TRIP_MODE2_ROOT", str(tmp_path / "envroot"))
    monkeypatch.setenv("TRIP_MODE2_DATE", "250101")

    result = MODULE.run([], popen=FakePopen)

    assert result["result"] == "launched"
    assert result["run_dir"] == str(tmp_path / "envroot" / "250101-mode2-daily")


def test_subprocess_fast_terminal_collector_state_is_not_regressed(tmp_path):
    root = tmp_path / "root"
    collector = tmp_path / "fast_terminal.sh"
    writer = Path(__file__).parents[1] / "scripts/hermes-cron/trip_mode2_launcher_state.py"
    collector.write_text("#!/bin/bash\n" +
                         'while [ ! -f "$TRIP_MODE2_LAUNCH_READY_FILE" ]; do sleep 0.01; done\n' +
                         'rm -f "$TRIP_MODE2_LAUNCH_READY_FILE"\n' +
                         f'"{sys.executable}" "{writer}" --run-dir "$PWD" --status completed --stage delivery --exit-code 0\n', encoding="utf-8")
    collector.chmod(0o755)
    completed = subprocess.run([sys.executable, str(SCRIPT), "--collector", str(collector), "--root", str(root), "--date", DATE], capture_output=True, text=True, check=True)
    assert json.loads(completed.stdout)["result"] == "launched"
    run_dir = root / f"{DATE}-mode2-daily"
    # Wait briefly for the detached child, then assert its terminal write survived.
    for _ in range(40):
        state = json.loads((run_dir / MODULE.STATE_FILE).read_text())
        if state.get("status") == "completed":
            break
        __import__("time").sleep(0.025)
    assert state["status"] == "completed" and state["stage"] == "delivery"


def test_default_paths_point_at_external_collector():
    assert str(MODULE.DEFAULT_COLLECTOR) == str(Path(os.environ.get("TRIP_HERMES_HOME", str(Path.home() / ".hermes"))) / "scripts/trip_daily_mode2_cron.sh")
    assert str(MODULE.DEFAULT_ROOT) == str(Path(os.environ.get("TRIP_DATA_ROOT", str(Path.home() / "Documents" / "trip-sku-price-scrape"))))


def test_scheduled_collector_is_hk_ru_only_with_dynamic_target_count():
    script = (Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_daily_mode2_cron.sh").read_text()
    assert 'SITES="hk,ru"' in script
    assert '--sites "$SITES"' in script
    assert 'NUM_WORKERS="${MODE2_WORKERS:-5}"' in script
    assert 'URL_COUNT=$(wc -l < "$URL_FILE"' in script
    assert '538' not in script
    assert "sites = ['HK', 'RU']" in script


def test_same_day_recovery_retries_only_inspected_chunks_in_isolated_dirs() -> None:
    script = (Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_daily_mode2_cron.sh").read_text()
    assert 'inspect-workers --run-dir "$TARGET_RUN_DIR"' in script
    assert 'VALID_WORKERS=()' in script
    assert 'WORKER_BASE="$RUN_DIR/retry-$RUN_TIMESTAMP"' in script
    assert 'ln -s "$RUN_DIR/w$i" "$WORKER_BASE/w$i"' in script
    assert 'for i in "${WORKERS_TO_RUN[@]}"; do' in script


def test_deployed_collector_matches_project_copy() -> None:
    """The 03:00 job runs the deployed collector; drift would drop the status fixes."""
    if os.environ.get("TRIP_ASSERT_DEPLOYED_PARITY") != "1":
        return
    project = Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_daily_mode2_cron.sh"
    deployed = Path.home() / ".hermes" / "scripts" / "trip_daily_mode2_cron.sh"
    assert project.is_file(), f"collector missing from the project: {project}"
    if not deployed.is_file():
        return
    assert deployed.read_bytes() == project.read_bytes(), (
        "deployed ~/.hermes/scripts/trip_daily_mode2_cron.sh is out of sync with "
        "scripts/hermes-cron/trip_daily_mode2_cron.sh; copy the project file over it"
    )
