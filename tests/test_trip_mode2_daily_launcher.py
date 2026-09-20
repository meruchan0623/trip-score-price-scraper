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
    assert result["pid"] == 424242
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    assert result["run_dir"] == str(run_dir)

    assert len(FakePopen.calls) == 1
    call = FakePopen.calls[0]
    assert call["cmd"] == ["/bin/bash", str(collector)]
    assert call["start_new_session"] is True
    assert call["close_fds"] is True
    assert call["stdout"] != subprocess.PIPE and call["stderr"] != subprocess.PIPE
    assert isinstance(call["stdout"], int) and call["stderr"] == call["stdout"]
    assert (run_dir / MODULE.LOG_FILE).exists()

    state_path = run_dir / MODULE.STATE_FILE
    state = json.loads(state_path.read_text(encoding="utf-8"))
    assert state["status"] == "running"
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
    (run_dir / ".collection_done").write_text("", encoding="utf-8")

    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)

    assert result["result"] == "skipped_done" and result["ok"] is True
    assert FakePopen.calls == []
    assert not (run_dir / MODULE.STATE_FILE).exists()


def test_already_running_when_pid_live(tmp_path):
    collector = _fake_collector(tmp_path)
    run_dir = tmp_path / "root" / f"{DATE}-mode2-daily"
    run_dir.mkdir(parents=True)
    MODULE.write_state_atomic(run_dir, {"status": "running", "pid": os.getpid(), "started_at": "x"})

    result = MODULE.run(_argv(tmp_path, collector), popen=FakePopen)

    assert result["result"] == "already_running" and result["ok"] is True
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


def test_default_paths_point_at_external_collector():
    assert str(MODULE.DEFAULT_COLLECTOR) == "/Users/example/.hermes/scripts/trip_daily_mode2_cron.sh"
    assert str(MODULE.DEFAULT_ROOT) == "/Users/example/Documents/trip-sku-price-scrape"


def test_deployed_collector_matches_project_copy() -> None:
    """The 03:00 job runs the deployed collector; drift would drop the vendor gate."""
    project = Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_daily_mode2_cron.sh"
    deployed = Path.home() / ".hermes" / "scripts" / "trip_daily_mode2_cron.sh"
    assert project.is_file(), f"collector missing from the project: {project}"
    if not deployed.is_file():
        return
    assert deployed.read_bytes() == project.read_bytes(), (
        "deployed ~/.hermes/scripts/trip_daily_mode2_cron.sh is out of sync with "
        "scripts/hermes-cron/trip_daily_mode2_cron.sh; copy the project file over it"
    )
