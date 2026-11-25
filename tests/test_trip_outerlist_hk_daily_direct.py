import importlib.util
from pathlib import Path


def _wrapper():
    path = Path(__file__).parents[1] / 'scripts/hermes-cron/trip_outerlist_hk_daily_direct.py'
    spec = importlib.util.spec_from_file_location('trip_outerlist_hk_daily_direct_test', path)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def test_wrapper_uses_detail_and_explicit_resume_pair(tmp_path, monkeypatch):
    wrapper = _wrapper()
    commands = []
    monkeypatch.setattr(wrapper, 'run_bounded', lambda command, _timeout: commands.append(command) or 1)
    monkeypatch.setattr(wrapper, 'find_run_by_suffix', lambda *_: None)
    assert wrapper.run_locked(tmp_path, 'telegram:test', '/safe/source') == 1
    command = commands[0]
    assert '--data-source' in command and command[command.index('--data-source') + 1] == 'detail'
    assert command[-3:] == ['--resume-run-dir', '/safe/source', '--failed-only']


def test_wrapper_rejects_missing_or_failed_result_before_delivery(tmp_path, monkeypatch):
    wrapper = _wrapper()
    run = tmp_path / '261125-mode2-outerlist-api-daily-cron-x'; run.mkdir()
    monkeypatch.setattr(wrapper, 'run_bounded', lambda *_: 0)
    monkeypatch.setattr(wrapper, 'find_run_by_suffix', lambda *_: run)
    assert wrapper.run_locked(tmp_path, 'telegram:test') == 1
