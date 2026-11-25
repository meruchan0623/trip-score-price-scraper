#!/usr/bin/env python3
"""Run the HK collector once and deliver only its verified, fresh output."""
from __future__ import annotations

import argparse
import json
import os
import signal
import subprocess
import time
import uuid
import zipfile
from xml.etree import ElementTree
from pathlib import Path

ROOT = Path('/Users/tsimclaw/Documents/trip-sku-price-scrape')
COLLECTOR = Path('/Users/tsimclaw/Develop/trip-score-price-scraper/scripts/trip_outerlist_mode2_api_daily.py')
DELIVER = Path('/Users/tsimclaw/Develop/trip-score-price-scraper/trip_deliver.py')
DEFAULT_TARGET = os.environ.get('TRIP_HK_DELIVERY_TARGET')
PYTHON = '/usr/local/bin/python3'
# Must stay below the Hermes cron timeout for this job (3600s): a timeout at the
# cron layer kills the wrapper before it can report why, which is how previous
# failures surfaced as an opaque "timed out after 3600s".
COLLECTOR_TIMEOUT_SEC = 2700
DELIVERY_TIMEOUT_SEC = 600
LOCK_STALE_SEC = COLLECTOR_TIMEOUT_SEC + DELIVERY_TIMEOUT_SEC + 300
class SignalInterrupted(Exception):
    """Raised by the SIGTERM handler so normal cleanup runs."""


PHASE_LABELS = {
    'complete': '完成',
    'outerlist_persistence': '外列持久化',
    'api_collection': '接口采集',
    'mode2_recovery': 'Mode2回补',
    'workbook_finalization': '工作簿生成',
}


def fail(reason: str) -> int:
    print('❌ Trip HK 销量日报执行失败：' + reason)
    return 1


def read_json_object(path: Path) -> dict | None:
    try:
        value = json.loads(path.read_text(encoding='utf-8'))
    except (OSError, ValueError, TypeError):
        return None
    return value if isinstance(value, dict) else None


def outcome_details(result: dict | None) -> tuple[str, int | None]:
    if result is None:
        return '未知', None
    phase = result.get('phase')
    failed_ids = result.get('failed_ids')
    safe_phase = PHASE_LABELS.get(phase, '未知') if isinstance(phase, str) else '未知'
    failed_count = len(failed_ids) if isinstance(failed_ids, list) else None
    return safe_phase, failed_count


def collector_failure(message: str, result: dict | None) -> int:
    phase, failed_count = outcome_details(result)
    count_text = str(failed_count) if failed_count is not None else '未知'
    hint = ''
    errors = result.get('errors') if isinstance(result, dict) else None
    if isinstance(errors, list) and 'risk_circuit_opened' in errors:
        # The 销量 interface was blocked by the anti-bot shield; the wrapper must
        # say so instead of reporting a bare failure count.
        hint = '，原因：接口被盾拦截（HTTP 430）'
    return fail(f'{message}（阶段：{phase}，失败数：{count_text}{hint}）。')


def process_is_live(pid: object) -> bool:
    if not isinstance(pid, int) or pid <= 0:
        return False
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    return True


def lock_is_live(lock_path: Path) -> bool:
    details = read_json_object(lock_path)
    try:
        age = time.time() - lock_path.stat().st_mtime
    except OSError:
        return True
    if age >= LOCK_STALE_SEC:
        return False
    return process_is_live(details.get('pid')) if details is not None else True


def acquire_lock(lock_path: Path):
    for _ in range(2):
        try:
            lock = lock_path.open('x', encoding='utf-8')
        except FileExistsError:
            if lock_is_live(lock_path):
                return None
            try:
                lock_path.unlink()
            except OSError:
                return None
            continue
        except OSError:
            return None
        try:
            json.dump({'pid': os.getpid(), 'started_at': int(time.time())}, lock)
            lock.flush()
            return lock
        except OSError:
            lock.close()
            try:
                lock_path.unlink()
            except OSError:
                pass
            return None
    return None


def terminate_process_group(process: subprocess.Popen) -> None:
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except OSError:
        return
    time.sleep(1)
    try:
        os.killpg(process.pid, 0)
    except OSError:
        return
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except OSError:
        pass


def run_bounded(command: list[str], timeout: int) -> int | None:
    """Run one child; on timeout/error terminate every member of its session."""
    process: subprocess.Popen | None = None
    try:
        process = subprocess.Popen(
            command,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        return process.wait(timeout=timeout)
    except (OSError, subprocess.TimeoutExpired):
        if process is not None:
            terminate_process_group(process)
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                pass
        return None
    except (KeyboardInterrupt, SignalInterrupted):
        if process is not None:
            terminate_process_group(process)
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                pass
        raise


def find_run_by_suffix(root: Path, suffix: str) -> Path | None:
    ending = '-' + suffix
    try:
        runs = [child.resolve() for child in root.iterdir() if child.is_dir() and child.name.endswith(ending)]
    except OSError:
        return None
    return runs[0] if len(runs) == 1 else None


def validate_workbook(result: dict, run: Path) -> tuple[Path | None, str | None]:
    workbook = result.get('workbook')
    if not isinstance(workbook, str) or not workbook:
        return None, '未生成有效输出文件'
    raw_output = Path(workbook)
    try:
        output = raw_output.resolve()
    except OSError:
        return None, '输出文件路径无法解析'
    if not (
        raw_output.is_absolute()
        and output.suffix.lower() == '.xlsx'
        and output.parent == run
        and output.is_file()
        and output.stat().st_size > 0
    ):
        return None, '输出文件缺失、为空或路径无效'

    # This wrapper is launched by Hermes's runtime Python, which intentionally
    # does not install collector-only packages such as openpyxl. Validate XLSX
    # with the standard library instead of turning that missing dependency into
    # a false "unreadable output" failure after collection has succeeded.
    try:
        with zipfile.ZipFile(output) as archive:
            if archive.testzip() is not None:
                return None, '输出文件 XLSX 压缩包校验失败'
            workbook_xml = archive.read('xl/workbook.xml')
        root = ElementTree.fromstring(workbook_xml)
        sheet_names = {
            node.attrib.get('name', '')
            for node in root.findall('{http://schemas.openxmlformats.org/spreadsheetml/2006/main}sheets/{http://schemas.openxmlformats.org/spreadsheetml/2006/main}sheet')
        }
    except FileNotFoundError:
        return None, '输出文件 XLSX 结构缺少工作簿定义'
    except (OSError, zipfile.BadZipFile, ElementTree.ParseError):
        return None, '输出文件不是有效的 XLSX 工作簿'
    if not {'Outerlist商品清单', 'HK'}.issubset(sheet_names):
        return None, '输出文件缺少必要工作表'
    return output, None


def run_locked(root: Path, target: str, resume_run_dir: str | None = None) -> int:
    run_suffix = 'cron-' + uuid.uuid4().hex
    command = [PYTHON, str(COLLECTOR), '--output-root', str(root), '--run-suffix', run_suffix,
               '--data-source', 'detail']
    if resume_run_dir:
        command.extend(['--resume-run-dir', resume_run_dir, '--failed-only'])
    collected = run_bounded(
        command,
        COLLECTOR_TIMEOUT_SEC,
    )
    if collected is None:
        return fail('采集命令超时或无法执行。')

    run = find_run_by_suffix(root, run_suffix)
    if run is None:
        return fail('未找到唯一的本次采集结果。')
    result = read_json_object(run / 'run_result.json')
    if result is None:
        return collector_failure('未生成有效采集结果', result)
    if collected != 0 or result.get('status') != 'success':
        return collector_failure('采集未成功', result)
    failed_ids = result.get('failed_ids')
    if not isinstance(failed_ids, list) or failed_ids:
        return collector_failure('采集结果仍有失败项', result)

    output, workbook_error = validate_workbook(result, run)
    if workbook_error is not None:
        return collector_failure(workbook_error, result)
    assert output is not None

    marker = read_json_object(run / '.collection_done')
    if marker is None:
        return collector_failure('采集完成标记缺失或无效', result)
    if (
        marker.get('sites') != 'hk'
        or marker.get('failed_count') != 0
        or marker.get('output_path') != str(output)
    ):
        return collector_failure('HK 采集完成校验未通过', result)

    receipt = run / '.delivered.json'
    try:
        if receipt.exists():
            receipt.unlink()
    except OSError:
        return fail('无法清理旧投递回执。')
    delivered = run_bounded(
        [PYTHON, str(DELIVER), str(output), '--to', target, '--subject', 'Trip HK Daily Sales Volume'],
        DELIVERY_TIMEOUT_SEC,
    )
    if delivered is None:
        return fail('XLSX 投递命令超时或无法执行。')
    proof = read_json_object(receipt)
    expected_proof = {'status': 'delivered', 'path': str(output), 'target': target}
    if delivered != 0:
        return fail('XLSX 投递未完成。')
    if proof != expected_proof:
        return fail('未取得与本次 HK 文件及目标匹配的投递回执。')
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument('--root', default=str(ROOT))
    parser.add_argument('--target', default=DEFAULT_TARGET, required=not DEFAULT_TARGET,
                        help='Delivery destination (or set TRIP_HK_DELIVERY_TARGET)')
    parser.add_argument('--resume-run-dir', help='explicit same-day failed run to recover')
    args = parser.parse_args()
    root = Path(args.root)
    try:
        root.mkdir(parents=True, exist_ok=True)
    except OSError:
        return fail('无法创建输出目录。')
    lock_path = root / '.trip_outerlist_hk_daily.lock'
    lock = acquire_lock(lock_path)
    if lock is None:
        return fail('已有任务正在执行或运行锁不可用。')

    code = 1
    cleanup_failed = False
    try:
        code = run_locked(root, args.target, args.resume_run_dir)
    finally:
        try:
            lock.close()
            lock_path.unlink()
        except OSError:
            cleanup_failed = True
    if cleanup_failed:
        return fail('运行锁清理失败。')
    return code


def _sigterm_handler(_signum, _frame) -> None:
    raise SignalInterrupted()


def entrypoint() -> int:
    previous_handler = signal.signal(signal.SIGTERM, _sigterm_handler)
    try:
        return main()
    except (KeyboardInterrupt, SignalInterrupted):
        return fail('任务被中断。')
    except Exception:
        return fail('执行异常。')
    finally:
        signal.signal(signal.SIGTERM, previous_handler)


if __name__ == '__main__':
    raise SystemExit(entrypoint())
