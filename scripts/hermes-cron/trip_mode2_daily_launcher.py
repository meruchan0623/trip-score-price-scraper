#!/usr/bin/env python3
"""Quick launcher for the daily HK/RU Mode2 collector.

Hermes cron has a bounded execution window; the direct collector
(``trip_daily_mode2_cron.sh``) runs far longer than that. This launcher starts
the collector as a detached session, persists audit state under the current
Mode2 run directory, and returns immediately. It never waits for the
collector and never touches collection, workbook, or delivery behavior.

Overrides (for tests / explicit operation):
  --collector PATH / TRIP_MODE2_COLLECTOR   collector script path
  --root PATH      / TRIP_MODE2_ROOT        run-dir root
  --date YYMMDD    / TRIP_MODE2_DATE        run date (default: today, local)
  --dry-run                                 report what would launch; no process, no PID
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

DEFAULT_COLLECTOR = Path('/Users/tsimclaw/.hermes/scripts/trip_daily_mode2_cron.sh')
DEFAULT_ROOT = Path('/Users/tsimclaw/Documents/trip-sku-price-scrape')
RUN_DIR_SUFFIX = '-mode2-daily'
DONE_MARKER = '.collection_done'
SITES = ('hk', 'ru')
STATE_FILE = 'launcher_state.json'
LOG_FILE = 'launcher_collector.log'


def run_dir_for(root: Path, date_yymmdd: str) -> Path:
    return root / f'{date_yymmdd}{RUN_DIR_SUFFIX}'


def read_state(run_dir: Path) -> dict:
    p = run_dir / STATE_FILE
    if not p.exists():
        return {}
    try:
        data = json.loads(p.read_text(encoding='utf-8'))
    except Exception:
        return {}
    return data if isinstance(data, dict) else {}


def done_matches_scope(run_dir: Path) -> bool:
    """Only a completed HK/RU run can suppress the replacement schedule."""
    try:
        payload = json.loads((run_dir / DONE_MARKER).read_text(encoding='utf-8'))
    except (OSError, ValueError):
        return False
    sites = tuple(str(payload.get('sites', '')).lower().split(','))
    output = Path(str(payload.get('output_path', '')))
    return (sites == SITES and payload.get('scope') == 'hk_ru_2site'
            and output.name.endswith('_trip_mode2_daily_2site.xlsx') and output.is_file())


def delivery_matches_source(run_dir: Path) -> bool:
    """A collection marker alone is not delivery success.

    The delivery helper owns the stronger source-hash receipt validation; this
    inexpensive gate only decides whether the launcher must wake that helper
    without creating a new collection.
    """
    try:
        marker = json.loads((run_dir / DONE_MARKER).read_text(encoding='utf-8'))
        receipt = json.loads((run_dir / '.delivery_done').read_text(encoding='utf-8'))
    except (OSError, ValueError):
        return False
    return (not (run_dir / '.pending_delivery.json').exists()
            and receipt.get('scope') == 'hk_ru_2site'
            and receipt.get('source_path') == marker.get('output_path')
            and receipt.get('target') == 'telegram:-5186417467'
            and bool(receipt.get('completed_at'))
            and bool(receipt.get('artifacts', {}).get('report_xlsx'))
            and bool(receipt.get('messages')))


def write_state_atomic(run_dir: Path, state: dict) -> Path:
    target = run_dir / STATE_FILE
    tmp = run_dir / f'.{STATE_FILE}.{os.getpid()}.tmp'
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as f:
            f.write(json.dumps(state, ensure_ascii=False, indent=2))
            f.flush()
            os.fsync(f.fileno())
        os.chmod(tmp, 0o600)
        os.replace(tmp, target)
    except BaseException:
        try:
            tmp.unlink()
        except FileNotFoundError:
            pass
        raise
    return target


def pid_is_live(pid) -> bool:
    try:
        pid = int(pid)
    except (TypeError, ValueError):
        return False
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def launch(collector: Path, run_dir: Path, log_path: Path, root: Path, date_yymmdd: str,
           ready_path: Path, popen=subprocess.Popen) -> int:
    """Start ``/bin/bash <collector>`` detached; return its PID without waiting."""
    log_fd = os.open(log_path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        environment = os.environ.copy()
        # The collector is a shell entrypoint, so explicit launcher overrides
        # must cross the process boundary rather than merely naming its cwd.
        environment.update({
            'TRIP_MODE2_ROOT': str(root),
            'TRIP_MODE2_DATE': date_yymmdd,
            # The collector waits for this one-shot handshake before it can
            # write a terminal state.  That makes the PID state publication
            # ordered, rather than a read-then-write race.
            'TRIP_MODE2_LAUNCH_READY_FILE': str(ready_path),
        })
        proc = popen(
            ['/bin/bash', str(collector)],
            stdin=subprocess.DEVNULL,
            stdout=log_fd,
            stderr=log_fd,
            cwd=str(run_dir),
            start_new_session=True,
            close_fds=True,
            env=environment,
        )
    finally:
        os.close(log_fd)
    return int(proc.pid)


def run(argv: list[str] | None = None, popen=subprocess.Popen, now=time.time) -> dict:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--collector', default=os.environ.get('TRIP_MODE2_COLLECTOR') or str(DEFAULT_COLLECTOR))
    parser.add_argument('--root', default=os.environ.get('TRIP_MODE2_ROOT') or str(DEFAULT_ROOT))
    parser.add_argument('--date', default=os.environ.get('TRIP_MODE2_DATE') or None, help='YYMMDD; default today')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args(argv)

    collector = Path(args.collector)
    root = Path(args.root).resolve()
    date_yymmdd = args.date or time.strftime('%y%m%d', time.localtime(now()))
    if not re.fullmatch(r'\d{6}', date_yymmdd):
        parser.error('--date must be YYMMDD')
    run_dir = run_dir_for(root, date_yymmdd)
    log_path = run_dir / LOG_FILE

    result = {
        'run_id': run_dir.name,
        'run_dir': str(run_dir),
        'collector': str(collector),
        'log_path': str(log_path),
        'sites': ','.join(SITES),
        'scope': 'hk_ru_2site',
        # ``ok`` answers whether this launcher operation succeeded.  These
        # fields deliberately answer the separate business-completion question.
        'launch_status': 'not_started',
        'business_status': 'not_started',
        'business_complete': False,
    }

    if (run_dir / DONE_MARKER).exists() and done_matches_scope(run_dir) and delivery_matches_source(run_dir):
        result.update({'result': 'skipped_done', 'ok': True, 'launch_status': 'skipped_done',
                       'business_status': 'complete', 'business_complete': True,
                       'message': f'{DONE_MARKER} and source-bound delivery receipt confirm HK/RU completion'})
        return result

    prev = read_state(run_dir)
    if prev.get('status') == 'running' and pid_is_live(prev.get('pid')):
        result.update({'result': 'already_running', 'ok': True, 'launch_status': 'already_running',
                       'business_status': 'in_progress', 'business_complete': False, 'pid': int(prev['pid']),
                       'started_at': prev.get('started_at'),
                       'message': f'collector PID {prev["pid"]} is live'})
        return result

    if not collector.is_file():
        result.update({'result': 'collector_missing', 'ok': False, 'launch_status': 'failed',
                       'message': f'collector not found: {collector}'})
        return result

    if args.dry_run:
        result.update({'result': 'dry_run', 'ok': True, 'launch_status': 'dry_run',
                       'command': ['/bin/bash', str(collector)],
                       'stale_state_replaced': bool(prev),
                       'message': 'would launch detached collector; nothing started'})
        return result

    run_dir.mkdir(parents=True, exist_ok=True)
    ready_path = run_dir / f'.launcher_ready.{os.getpid()}.{time.time_ns()}'
    pid = launch(collector, run_dir, log_path, root, date_yymmdd, ready_path, popen=popen)
    started = now()
    state = {
        'status': 'running',
        'pid': pid,
        'started_at': time.strftime('%Y-%m-%d %H:%M:%S %z', time.localtime(started)),
        'started_epoch': started,
        'collector': str(collector),
        'run_dir': str(run_dir),
        'log_path': str(log_path),
        'launcher': str(Path(__file__).resolve()),
        'launcher_pid': os.getpid(),
        'sites': ','.join(SITES),
        'scope': 'hk_ru_2site',
        'launch_status': 'launched',
        'business_status': 'in_progress',
        'business_complete': False,
    }
    if prev:
        state['replaced_stale'] = {'pid': prev.get('pid'), 'started_at': prev.get('started_at')}
    if (run_dir / DONE_MARKER).exists() and done_matches_scope(run_dir):
        state['resume_delivery_only'] = True
    # The child cannot write a terminal state until the ready file exists.
    # Publish the PID first, then release it; after this point the launcher
    # never rewrites state, so a fast terminal result always wins.
    state_path = write_state_atomic(run_dir, state)
    ready_path.touch(mode=0o600, exist_ok=False)
    result.update({'result': 'launched', 'ok': True, 'launch_status': 'launched',
                   'business_status': 'in_progress', 'business_complete': False, 'pid': pid,
                   'started_at': state['started_at'], 'state_path': str(state_path),
                   'message': ('Mode2 delivery resume launched detached PID ' if state.get('resume_delivery_only')
                               else 'Mode2 collector launched detached PID ') + str(pid)})
    return result


def main(argv: list[str] | None = None) -> int:
    result = run(argv)
    print(json.dumps(result, ensure_ascii=False))
    return 0 if result.get('ok') else 1


if __name__ == '__main__':
    sys.exit(main())
