#!/usr/bin/env python3
"""Daily browserless Trip HK sales collector from a persisted Outerlist table."""
from __future__ import annotations

import argparse
import concurrent.futures as futures
import json
import math
import os
import re
import signal
import subprocess
import time
import urllib.request
from dataclasses import dataclass
from datetime import date, datetime, timedelta
from decimal import Decimal, InvalidOperation
from pathlib import Path

import openpyxl
from openpyxl.styles import Alignment, Font

REPO_ROOT = Path(__file__).resolve().parents[1]
OUTER_ROOT = Path('/Users/example/Documents/tripcom-outerlist-scraping')
ATTR = Path('/Users/example/Documents/trip-operations/vendor-attribution/manual_vendor_attribution_latest.json')
DEFAULT_ROOT = Path('/Users/example/Documents/trip-sku-price-scrape')
SITES = [('tw', 'zh-TW', 'TWD'), ('hk', 'zh-HK', 'HKD'), ('ru', 'ru-RU', 'RUB'), ('jp', 'ja-JP', 'JPY'), ('vn', 'vi-VN', 'VND'), ('th', 'th-TH', 'THB')]
OUTER_HEADERS = ['厂商', '商品ID', 'URL', '商品名称', 'Outerlist总分', 'Outerlist评价数', 'Outerlist价格', 'Outerlist标签']
HEADERS = ['厂商', '商品ID', 'URL', '商品名称', '商品资讯', '总促销标签', '销量增量', '月销量', '年销量', '总销量', '评价数', '总分', '评分变化', '好评率', '客户服务', '方便程度', '讯号状态', '中差评价数', '抓取备注']
SPACE = re.compile(r'\s+')
RED = '00FF0000'       # existing Outerlist report convention: increase = red
GREEN = '00008000'     # decrease = green


class UnsupportedPolicyError(ValueError):
    """A valid collector policy that this bounded stage cannot execute yet."""


class TrackExplicitValue(argparse.Action):
    """Keep a safe default while recording when a deferred option was requested."""

    def __call__(self, parser, namespace, values, option_string=None):
        setattr(namespace, self.dest, values)
        setattr(namespace, f'{self.dest}_explicit', True)


@dataclass(frozen=True)
class CollectorConfig:
    """Validated policy shared by CLI parsing and collector execution."""

    workers: int
    sites: tuple[str, ...]
    output_root: Path
    baseline_workbook: Path | None
    run_suffix: str
    mode2_fallback: str
    mode2_workers: int
    mode2_workers_explicit: bool
    api_retries: int
    api_retry_interval_sec: float
    resume_run_dir: Path | None
    failed_only: bool


def positive_int(value):
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise argparse.ArgumentTypeError('must be a positive integer') from exc
    if parsed <= 0:
        raise argparse.ArgumentTypeError('must be a positive integer')
    return parsed


def mode2_worker_count(value):
    parsed = positive_int(value)
    if parsed > 5:
        raise argparse.ArgumentTypeError('must be between 1 and 5')
    return parsed


def nonnegative_int(value):
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise argparse.ArgumentTypeError('must be a nonnegative integer') from exc
    if parsed < 0:
        raise argparse.ArgumentTypeError('must be a nonnegative integer')
    return parsed


def nonnegative_float(value):
    try:
        parsed = float(value)
    except (TypeError, ValueError) as exc:
        raise argparse.ArgumentTypeError('must be a finite nonnegative number') from exc
    if not math.isfinite(parsed) or parsed < 0:
        raise argparse.ArgumentTypeError('must be a finite nonnegative number')
    return parsed


def build_parser():
    parser = argparse.ArgumentParser()
    parser.set_defaults(mode2_workers_explicit=False)
    parser.add_argument('--workers', type=positive_int, default=45)
    parser.add_argument('--sites', default='hk')
    parser.add_argument('--output-root', default=str(DEFAULT_ROOT))
    parser.add_argument('--baseline-workbook', help='指定兼容的 HK/六站 XLSX 作为本次销量和评分变化基线')
    parser.add_argument('--run-suffix', default='', help='仅手动补跑使用；为当天运行目录追加唯一后缀')
    parser.add_argument('--mode2-fallback', choices=('auto', 'off', 'required'), default='auto')
    parser.add_argument('--mode2-workers', type=mode2_worker_count, default=5, action=TrackExplicitValue)
    parser.add_argument('--api-retries', type=nonnegative_int, default=0,
                        help='在原有 API 尝试次数之外追加的重试次数')
    parser.add_argument('--api-retry-interval-sec', type=nonnegative_float, default=0,
                        help='API 重试固定间隔；0 保留原有递增间隔')
    parser.add_argument('--resume-run-dir')
    parser.add_argument('--failed-only', action='store_true')
    return parser


def parse_config(argv=None):
    args = build_parser().parse_args(argv)
    return CollectorConfig(
        workers=args.workers,
        sites=tuple(value.strip().lower() for value in args.sites.split(',') if value.strip()),
        output_root=Path(args.output_root).expanduser(),
        baseline_workbook=Path(args.baseline_workbook).expanduser() if args.baseline_workbook else None,
        run_suffix=args.run_suffix.strip(),
        mode2_fallback=args.mode2_fallback,
        mode2_workers=args.mode2_workers,
        mode2_workers_explicit=args.mode2_workers_explicit,
        api_retries=args.api_retries,
        api_retry_interval_sec=args.api_retry_interval_sec,
        resume_run_dir=Path(args.resume_run_dir).expanduser() if args.resume_run_dir else None,
        failed_only=args.failed_only,
    )


def validate_runtime_policy(config):
    """Reject valid options whose execution path is intentionally deferred."""
    if config.resume_run_dir is not None or config.failed_only:
        raise UnsupportedPolicyError('--resume-run-dir/--failed-only resume execution is unavailable in stage 1')


SAFE_RESULT_ERRORS = frozenset({
    'Mode2 recovery build failed',
    'Mode2 recovery worker failed',
    'Mode2 recovery output missing',
    'Mode2 recovery workbook invalid',
    'Mode2 recovery incomplete; failed API rows remain',
})
SAFE_EXCEPTION_TYPE = re.compile(r'^exception_type=[A-Za-z_][A-Za-z0-9_]*$')


def safe_error(value):
    """Only serialize allowlisted diagnostics; arbitrary error text may contain credentials."""
    message = text(value)
    if message in SAFE_RESULT_ERRORS or SAFE_EXCEPTION_TYPE.fullmatch(message):
        return message
    return '[REDACTED]'


def build_run_result(*, status, phase, run_id, outerlist_count=0, api_success_count=0,
                     mode2_recovered_count=0, failed_ids=(), baseline_path=None,
                     workbook=None, errors=()):
    """Build the credential-free, JSON-safe contract for one collector run."""
    return {
        'status': text(status),
        'phase': text(phase),
        'run_id': text(run_id),
        'outerlist_count': text(outerlist_count),
        'api_success_count': text(api_success_count),
        'mode2_recovered_count': text(mode2_recovered_count),
        'failed_ids': [text(value) for value in failed_ids],
        'baseline_path': text(baseline_path) or None,
        'delta_basis': 'twoYearSales',
        'workbook': text(workbook) or None,
        'errors': [safe_error(value) for value in errors],
    }


def write_run_result(run, result):
    """Atomically persist a run result without changing completion markers."""
    target = run / 'run_result.json'
    temporary = run / '.run_result.json.tmp'
    temporary.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    temporary.replace(target)


def clear_completion_marker(run):
    """A failed terminal path must never retain a marker eligible for baselines."""
    marker = run / '.collection_done'
    try:
        marker.unlink()
    except FileNotFoundError:
        pass


def text(value):
    return '' if value is None else str(value)


def run_day(path):
    try:
        return datetime.strptime(path.name[:6], '%y%m%d').date()
    except ValueError:
        return None


def daily_output_filename(started):
    """Name each daily workbook with its actual collection start time."""
    return started.strftime('%y%m%d-%H%M') + '_Trip_DailySalesVolume_FromOuterlist.xlsx'


def load_items(path):
    file = path / 'items.jsonl'
    if not file.exists():
        return []
    try:
        return [json.loads(line) for line in file.read_text(encoding='utf-8').splitlines() if line.strip()]
    except Exception:
        return []


def latest_outerlist():
    candidates = []
    for path in OUTER_ROOT.iterdir():
        if not path.is_dir():
            continue
        manifest = path / 'manifest.json'
        items = load_items(path)
        if not manifest.exists() or not items:
            continue
        try:
            info = json.loads(manifest.read_text(encoding='utf-8'))
            expected = int(info.get('processed_count') or 0)
        except Exception:
            continue
        if expected and len(items) != expected:
            continue
        candidates.append((info.get('started_at') or path.name, path, items))
    if not candidates:
        raise RuntimeError('未找到完整的 trip-outerlist-daily 结果')
    _, source_dir, items = max(candidates, key=lambda item: item[0])
    return source_dir, items


def outerlist_rows(source):
    lookup = json.loads(ATTR.read_text(encoding='utf-8'))['lookup_by_product_id']
    seen, rows = set(), []
    for item in source:
        product_id = text(item.get('id')).strip()
        if not product_id or product_id in seen:
            continue
        seen.add(product_id)
        vendor = text(lookup.get(product_id, {}).get('vendor_name')).strip() or '未标注'
        tags = item.get('tags') or []
        rows.append({
            '厂商': vendor,
            '商品ID': product_id,
            'URL': text(item.get('url')),
            '商品名称': text(item.get('name')),
            'Outerlist总分': text(item.get('rating')),
            'Outerlist评价数': text(item.get('reviews_count')),
            'Outerlist价格': text(item.get('price')),
            'Outerlist标签': '｜'.join(text(tag) for tag in tags),
        })
    return rows


def create_outerlist_book(output, day, source_dir, rows):
    """Persist the Outerlist result first; later API work reads this sheet back."""
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.title = 'Outerlist商品清单'
    title = '抓取日期：' + day.strftime('%Y年%m月%d日') + '｜Outerlist来源：' + source_dir.name
    ws.merge_cells(start_row=1, start_column=1, end_row=1, end_column=len(OUTER_HEADERS))
    cell = ws.cell(1, 1, title)
    cell.font = Font(bold=True)
    cell.alignment = Alignment(horizontal='left', vertical='center')
    ws.append(OUTER_HEADERS)
    for cell in ws[2]:
        cell.font = Font(bold=True)
        cell.alignment = Alignment(horizontal='center', vertical='center')
    for row in rows:
        ws.append([text(row.get(header, '')) for header in OUTER_HEADERS])
    ws.freeze_panes = 'A3'
    ws.auto_filter.ref = f'A2:H{ws.max_row}'
    for column, width in enumerate([12, 14, 52, 60, 14, 16, 16, 35], 1):
        ws.column_dimensions[openpyxl.utils.get_column_letter(column)].width = width
    wb.save(output)


def products_from_outerlist_book(output):
    """The persisted Outerlist sheet is the only product input for API collection."""
    wb = openpyxl.load_workbook(output, read_only=True, data_only=True)
    ws = wb['Outerlist商品清单']
    headers = {text(cell.value): index for index, cell in enumerate(ws[2])}
    required = {'厂商', '商品ID'}
    if not required.issubset(headers):
        raise RuntimeError('Outerlist商品清单缺少商品ID或厂商列')
    products = []
    for row in ws.iter_rows(min_row=3, values_only=True):
        product_id = text(row[headers['商品ID']] if len(row) > headers['商品ID'] else '').strip()
        if product_id:
            products.append({'id': product_id, 'vendor': text(row[headers['厂商']] if len(row) > headers['厂商'] else '') or '未标注'})
    wb.close()
    if not products:
        raise RuntimeError('Outerlist商品清单没有可抓取商品')
    return products


def brief_info(add_infos):
    allowed = {'EffectiveDate', 'FeeInclude', 'FeeExclude', 'NetworkType', 'ApplicableArea'}
    result = []
    for item in add_infos or []:
        if item.get('code') not in allowed or not item.get('title'):
            continue
        values = [SPACE.sub(' ', text(value.get('value')).strip()) for value in item.get('contents', []) if value.get('value')]
        if values:
            result.append('• ' + text(item['title']).strip() + '：' + ' '.join(values))
    return ' '.join(result)


def fetch(site, locale, currency, item, api_retries=0, api_retry_interval_sec=0):
    product_id = item['id']
    payload = {
        'clientInfo': {'currency': currency, 'locale': locale, 'pageId': '10650010429', 'channelId': 120, 'platformId': 26, 'oriSyscode': '09', 'syscode': '09'},
        'enviroment': 'PROD',
        'searchParams': [{'id': product_id, 'idType': 1}],
        'needBasicInfo': True, 'needCommentInfo': True, 'needSalesVolume': True,
        'needAddInfos': True, 'needResourceInfos': True, 'needPackageInfos': True,
        'needMediaInfo': True, 'needVendorInfo': True, 'needTagInfos': True,
    }
    request = urllib.request.Request(
        f'https://{site}.trip.com/restapi/soa2/21052/getProductInfo',
        data=json.dumps(payload, separators=(',', ':')).encode(), method='POST',
        headers={'Content-Type': 'application/json', 'Accept': 'application/json', 'Accept-Language': locale, 'User-Agent': 'Mozilla/5.0'},
    )
    error = ''
    total_attempts = 3 + api_retries
    for attempt in range(total_attempts):
        try:
            with urllib.request.urlopen(request, timeout=35) as response:
                data = json.load(response)
            if data.get('ResponseStatus', {}).get('Ack') != 'Success':
                errors = data.get('ResponseStatus', {}).get('Errors') or [{}]
                raise RuntimeError(errors[0].get('Message', 'API failed'))
            product = (data.get('productInfos') or [{}])[0]
            if text(product.get('productId')) != product_id:
                raise RuntimeError('returned product ID mismatch')
            sales = product.get('salesVolume') or {}
            comment = product.get('commentInfo') or {}
            basic = product.get('basicInfo') or {}
            # Total score is sourced from the public response commentInfo.commentScore, never the page DOM.
            return {
                '厂商': item['vendor'], '商品ID': product_id,
                'URL': f'https://{site}.trip.com/things-to-do/detail/{product_id}/?curr={currency}&locale={locale}',
                '商品名称': text(basic.get('productName')), '商品资讯': brief_info(product.get('addInfos')),
                '月销量': text(sales.get('monthlySales')), '年销量': text(sales.get('yearlySales')), '总销量': text(sales.get('twoYearSales')),
                '评价数': text(comment.get('commentCount')), '总分': text(comment.get('commentScore')),
            }
        except Exception as exc:
            error = f'{type(exc).__name__}: {exc}'
            if attempt < total_attempts - 1:
                delay = api_retry_interval_sec or (1.5 * (attempt + 1))
                time.sleep(delay)
    return {'厂商': item['vendor'], '商品ID': product_id, 'URL': f'https://{site}.trip.com/things-to-do/detail/{product_id}/?curr={currency}&locale={locale}', '抓取备注': '接口请求失败：' + error}


def as_int(value):
    try:
        return int(str(value).replace(',', '').strip())
    except (ValueError, TypeError):
        return None


def as_decimal(value):
    try:
        return Decimal(str(value).strip())
    except (InvalidOperation, ValueError, TypeError):
        return None


def metrics_from_book(book):
    """Read a compatible HK/6-site workbook as a sales-and-score baseline."""
    previous = {}
    wb = openpyxl.load_workbook(book, read_only=True, data_only=True)
    for ws in wb.worksheets:
        header = None
        for row_number, row in enumerate(ws.iter_rows(values_only=True), start=1):
            values = {text(value): index for index, value in enumerate(row) if value is not None}
            if '商品ID' in values and '总销量' in values:
                header, start = values, row_number + 1
                break
            if row_number >= 5:
                break
        if not header:
            continue
        score_key = '总分' if '总分' in header else '评分' if '评分' in header else None
        for row in ws.iter_rows(min_row=start, values_only=True):
            product_id = text(row[header['商品ID']] if len(row) > header['商品ID'] else '').strip()
            if not product_id:
                continue
            total = as_int(row[header['总销量']] if len(row) > header['总销量'] else None)
            score = as_decimal(row[header[score_key]] if score_key and len(row) > header[score_key] else None)
            if total is not None or score is not None:
                previous[(ws.title.upper(), product_id)] = {'total': total, 'score': score}
    wb.close()
    return previous


def successful_collection_run(path):
    """Accept legacy completion markers, but never use an explicitly failed run as a baseline."""
    if not (path / '.collection_done').exists():
        return False
    result = path / 'run_result.json'
    if not result.exists():
        return True
    try:
        return json.loads(result.read_text(encoding='utf-8')).get('status') == 'success'
    except (OSError, ValueError, json.JSONDecodeError):
        return False


def previous_metrics(root, target_day):
    wanted = target_day - timedelta(days=1)
    runs = [path for path in root.iterdir() if path.is_dir() and run_day(path) == wanted and successful_collection_run(path)]
    if not runs:
        return {}, None
    run = max(runs, key=lambda path: (path / '.collection_done').stat().st_mtime)
    books = sorted(run.glob('*_Trip_DailySalesVolume_FromOuterlist.xlsx'))
    if not books:
        return {}, None
    return metrics_from_book(books[0]), run


def format_score_delta(current, previous):
    if current is None or previous is None:
        return '', None
    change = current - previous
    if change == 0:
        return '0', None
    text_delta = format(change.normalize(), 'f').rstrip('0').rstrip('.')
    return ('+' if change > 0 else '') + text_delta, RED if change > 0 else GREEN


def baseline_day(baseline):
    """Date of the actual selected baseline: a run directory or a daily workbook inside one."""
    if baseline is None:
        return None
    baseline = Path(baseline)
    return run_day(baseline) or run_day(baseline.parent)


def sales_delta_header(day, compared_day):
    if compared_day is None:
        return '销量增量（无对比基线）'
    return f'销量增量（{day.strftime("%m%d")}-{compared_day.strftime("%m%d")}）'


def sales_headers(day, compared_day):
    return [sales_delta_header(day, compared_day) if column == '销量增量' else column for column in HEADERS]


def add_sales_sheets(output, day, by_site, previous, compared_day=None):
    wb = openpyxl.load_workbook(output)
    title = '抓取日期：' + day.strftime('%Y年%m月%d日')
    headers = sales_headers(day, compared_day)
    for site, rows in by_site.items():
        if site in wb.sheetnames:
            del wb[site]
        ws = wb.create_sheet(site)
        ws.merge_cells(start_row=1, start_column=1, end_row=1, end_column=len(HEADERS))
        cell = ws.cell(1, 1, title)
        cell.font = Font(bold=True)
        cell.alignment = Alignment(horizontal='left', vertical='center')
        ws.append(headers)
        for cell in ws[2]:
            cell.font = Font(bold=True)
            cell.alignment = Alignment(horizontal='center', vertical='center')
        score_delta_index = HEADERS.index('评分变化') + 1
        score_index = HEADERS.index('总分') + 1
        for row in rows:
            old = previous.get((site, row.get('商品ID', '')), {})
            total = as_int(row.get('总销量'))
            old_total = old.get('total')
            row['销量增量'] = '' if total is None or old_total is None else str(total - old_total)
            delta, color = format_score_delta(as_decimal(row.get('总分')), old.get('score'))
            row['评分变化'] = delta
            ws.append([text(row.get(column, '')) for column in HEADERS])
            if color:
                ws.cell(ws.max_row, score_index).font = Font(color=color)
                ws.cell(ws.max_row, score_delta_index).font = Font(color=color)
        ws.freeze_panes = 'A3'
        ws.auto_filter.ref = f'A2:{openpyxl.utils.get_column_letter(len(HEADERS))}{ws.max_row}'
        widths = [12, 14, 52, 55, 70, 40, 12, 12, 12, 12, 12, 10, 12, 12, 12, 12, 12, 14, 45]
        for index, width in enumerate(widths, 1):
            ws.column_dimensions[openpyxl.utils.get_column_letter(index)].width = width
    wb.save(output)


MODE2_FIELDS = {
    'URL': 'URL',
    '商品名称': '商品名称',
    '商品资讯': '商品资讯',
    '总促销标签': '总促销标签',
    '月销量': '月销量',
    '年销量': '年销量',
    '总销量': '总销量',
    '评价数': '评价数',
    '评分': '总分',
    '好评率': '好评率',
    '客户服务': '客户服务',
    '方便程度': '方便程度',
    '讯号状态': '讯号状态',
    '中差评价数': '中差评价数',
}
MODE2_BUILD_TIMEOUT_SEC = 120
MODE2_WORKER_TIMEOUT_SEC = 45 * 60
MODE2_TERMINATE_GRACE_SEC = 5


def read_mode2_rows(path):
    wb = openpyxl.load_workbook(path, read_only=True, data_only=True)
    try:
        ws = wb['Trip评分数据']
        headers = [text(cell.value).strip() for cell in ws[1]]
        if '商品ID' not in headers:
            raise RuntimeError('Mode2 workbook is missing product IDs')
        rows = {}
        for values in ws.iter_rows(min_row=2, values_only=True):
            row = {
                header: text(values[index] if index < len(values) else '')
                for index, header in enumerate(headers)
                if header
            }
            product_id = row.get('商品ID', '').strip()
            if product_id:
                rows[product_id] = row
        return rows
    finally:
        wb.close()


def _signal_mode2_process_group(process, signal_number):
    """Signal only the worker session, falling back to its direct process."""
    try:
        os.killpg(process.pid, signal_number)
    except ProcessLookupError:
        pass
    except OSError:
        try:
            if signal_number == signal.SIGTERM:
                process.terminate()
            else:
                process.kill()
        except OSError:
            pass


def _stop_timed_out_mode2_process(process):
    """Bounded TERM/KILL cleanup for a worker and its browser descendants."""
    _signal_mode2_process_group(process, signal.SIGTERM)
    try:
        process.wait(timeout=MODE2_TERMINATE_GRACE_SEC)
        return
    except (OSError, subprocess.TimeoutExpired):
        pass
    _signal_mode2_process_group(process, signal.SIGKILL)
    try:
        process.wait(timeout=MODE2_TERMINATE_GRACE_SEC)
    except (OSError, subprocess.TimeoutExpired):
        pass


def _run_mode2_command(command, timeout_sec):
    process = None
    try:
        process = subprocess.Popen(
            command,
            cwd=REPO_ROOT,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        return process.wait(timeout=timeout_sec)
    except subprocess.TimeoutExpired:
        _stop_timed_out_mode2_process(process)
        return None
    except OSError:
        if process is not None:
            _stop_timed_out_mode2_process(process)
        return None


def run_mode2_recovery(run, failed_ids, workers):
    failed_ids = list(dict.fromkeys(text(value).strip() for value in failed_ids if text(value).strip()))
    if not failed_ids:
        return {}, []

    work = run / 'mode2-recovery'
    work.mkdir(parents=True, exist_ok=True)
    binary = work / 'trip-score-cdp'
    build = ['go', 'build', '-o', str(binary), './cmd/trip-score-cdp']
    if _run_mode2_command(build, MODE2_BUILD_TIMEOUT_SEC) != 0:
        return {}, ['Mode2 recovery build failed']

    command_count = min(workers, len(failed_ids))
    chunks = [failed_ids[index::command_count] for index in range(command_count)]
    jobs = []
    for index, chunk in enumerate(chunks, start=1):
        worker_dir = work / ('worker-' + str(index))
        worker_dir.mkdir(parents=True, exist_ok=True)
        urls_file = worker_dir / 'urls.txt'
        urls_file.write_text(
            ''.join('https://hk.trip.com/things-to-do/detail/' + product_id + '/\n' for product_id in chunk),
            encoding='utf-8',
        )
        output = worker_dir / 'mode2.xlsx'
        command = [
            str(binary),
            '-run-mode', 'mode2',
            '-delay-profile', 'normal',
            '-urls-file', str(urls_file),
            '-output', str(output),
            '-workers', '1',
            '-no-headless',
        ]
        jobs.append((chunk, output, command))

    with futures.ThreadPoolExecutor(max_workers=command_count) as pool:
        return_codes = list(pool.map(
            lambda job: _run_mode2_command(job[2], MODE2_WORKER_TIMEOUT_SEC),
            jobs,
        ))

    recovered = {}
    errors = []
    for (chunk, output, _command), return_code in zip(jobs, return_codes):
        if return_code != 0:
            errors.append('Mode2 recovery worker failed')
            continue
        if not output.is_file():
            errors.append('Mode2 recovery output missing')
            continue
        try:
            rows = read_mode2_rows(output)
        except Exception:
            errors.append('Mode2 recovery workbook invalid')
            continue
        for product_id in chunk:
            row = rows.get(product_id)
            if (row is not None
                    and not text(row.get('抓取备注')).strip()
                    and has_mode2_recovery_payload(row)):
                recovered[product_id] = row
    return recovered, list(dict.fromkeys(errors))


def has_mode2_recovery_payload(row):
    """Require scraped Mode2 data beyond echoed identifiers and status."""
    return any(
        text(row.get(source)).strip()
        for source in MODE2_FIELDS
        if source not in {'商品ID', 'URL'}
    )


def merge_mode2_rows(rows, recovered):
    recovered_ids = []
    for row in rows:
        if not text(row.get('抓取备注')).strip():
            continue
        product_id = text(row.get('商品ID')).strip()
        mode2_row = recovered.get(product_id)
        if (mode2_row is None
                or text(mode2_row.get('抓取备注')).strip()
                or not has_mode2_recovery_payload(mode2_row)):
            continue
        for source, target in MODE2_FIELDS.items():
            value = mode2_row.get(source)
            if text(value).strip():
                row[target] = text(value)
        row['抓取备注'] = ''
        recovered_ids.append(product_id)
    return recovered_ids


def failed_rows(by_site):
    return [
        site + '/' + text(row.get('商品ID')).strip()
        for site, rows in by_site.items()
        for row in rows
        if text(row.get('抓取备注')).strip()
    ]


def main(argv=None):
    config = parse_config(argv)
    try:
        validate_runtime_policy(config)
    except UnsupportedPolicyError as exc:
        raise SystemExit(str(exc)) from exc

    started, day = datetime.now(), date.today()
    root = config.output_root
    root.mkdir(parents=True, exist_ok=True)
    suffix = ('-' + config.run_suffix) if config.run_suffix else ''
    run = root / (day.strftime('%y%m%d') + '-mode2-outerlist-api-daily' + suffix)
    if run.exists():
        raise SystemExit('运行目录已存在，拒绝覆盖：' + str(run))
    selected = [item for item in SITES if item[0] in set(config.sites)]
    if not selected:
        raise SystemExit('未选择有效站点')
    # An explicit baseline supports same-day verification without changing the normal previous-day cron rule.
    if config.baseline_workbook:
        baseline = config.baseline_workbook
        if not baseline.is_file():
            raise SystemExit('指定的基线工作簿不存在：' + str(baseline))
        previous = metrics_from_book(baseline)
        if not previous:
            raise SystemExit('指定的基线工作簿未读到商品ID和总销量/总分：' + str(baseline))
    else:
        previous, baseline = previous_metrics(root, day)
    source_dir, source_items = latest_outerlist()
    run.mkdir()

    output = run / daily_output_filename(started)
    phase = 'outerlist_persistence'
    persisted_outerlist = []
    items = []
    failures = []
    api_success_count = 0
    mode2_recovered_count = 0
    mode2_attempted = False
    result_errors = []
    try:
        # Stage 1: persist the raw, de-duplicated Outerlist table.
        persisted_outerlist = outerlist_rows(source_items)
        create_outerlist_book(output, day, source_dir, persisted_outerlist)
        # Stage 2: read the persisted table as the authoritative API input.
        phase = 'api_collection'
        items = products_from_outerlist_book(output)
        by_site = {site.upper(): [] for site, _, _ in selected}
        tasks = [(site, locale, currency, item) for site, locale, currency in selected for item in items]

        def run_task(task):
            site, locale, currency, item = task
            row = fetch(
                site, locale, currency, item,
                api_retries=config.api_retries,
                api_retry_interval_sec=config.api_retry_interval_sec,
            )
            return site.upper(), row

        with futures.ThreadPoolExecutor(max_workers=config.workers) as pool:
            for site, row in pool.map(run_task, tasks):
                by_site[site].append(row)
                if not text(row.get('抓取备注')).strip():
                    api_success_count += 1

        failures = failed_rows(by_site)
        if failures and config.mode2_fallback != 'off':
            hk_rows = by_site.get('HK', [])
            failed_hk_ids = [
                text(row.get('商品ID')).strip()
                for row in hk_rows
                if text(row.get('抓取备注')).strip()
            ]
            if failed_hk_ids:
                phase = 'mode2_recovery'
                mode2_attempted = True
                recovered, recovery_errors = run_mode2_recovery(
                    run, failed_hk_ids, config.mode2_workers,
                )
                mode2_recovered_count = len(merge_mode2_rows(hk_rows, recovered))
                result_errors.extend(recovery_errors)
                failures = failed_rows(by_site)
            if failures:
                result_errors.append('Mode2 recovery incomplete; failed API rows remain')

        result_phase = 'complete'
        if failures:
            result_phase = 'mode2_recovery' if mode2_attempted else 'api_collection'
        phase = 'workbook_finalization'
        add_sales_sheets(output, day, by_site, previous, baseline_day(baseline))

        if failures:
            clear_completion_marker(run)
        run_result = build_run_result(
            status='failed' if failures else 'success',
            phase=result_phase,
            run_id=run.name,
            outerlist_count=len(persisted_outerlist),
            api_success_count=api_success_count,
            mode2_recovered_count=mode2_recovered_count,
            failed_ids=failures,
            baseline_path=baseline,
            workbook=output,
            errors=result_errors,
        )
        write_run_result(run, run_result)

        done = {
            'run_dir': str(run), 'output_path': str(output), 'source_outerlist': str(source_dir),
            'outerlist_table_sheet': 'Outerlist商品清单', 'input_products': len(items),
            'sites': ','.join(site for site, _, _ in selected), 'processed_count': len(items) * len(selected),
            'failed_count': len(failures), 'failed_samples': failures[:20], 'baseline_run': str(baseline) if baseline else None,
            'sales_delta_definition': '当次总销量-前一天总销量',
            'method': ('public_product_info_api_with_headed_mode2_recovery' if mode2_attempted
                       else 'public_product_info_api_no_browser'),
            'rating_source': 'productInfos[0].commentInfo.commentScore',
            'mode2_recovered_count': mode2_recovered_count,
            'started_at': started.isoformat(), 'completed_at': datetime.now().isoformat(),
        }
        (run / '任务说明.md').write_text(
            f'# 每日 Trip HK Outerlist 商品销量接口抓取\n\n'
            f'- 抓取日期：{day.isoformat()}\n- Outerlist表来源：{source_dir}\n'
            f'- 数据流：先输出 `Outerlist商品清单`，再从该表读取商品ID抓取 HK 销量和总分。\n'
            f'- 对比基准：{baseline or "无（销量及评分变化留空）"}\n'
            f'- 销量增量定义：当次`总销量`减前一天`总销量`。\n'
            f'- 销量、评价和总分接口：POST /restapi/soa2/21052/getProductInfo\n'
            f'- 总分路径：`productInfos[0].commentInfo.commentScore`。API-only 全部成功时不启动浏览器；API 失败且尝试恢复时，可能启动 headed Go Mode2。\n', encoding='utf-8')
        if not failures:
            (run / '.collection_done').write_text(json.dumps(done, ensure_ascii=False, indent=2), encoding='utf-8')
    except BaseException as exc:
        clear_completion_marker(run)
        failed_result = build_run_result(
            status='failed',
            phase=phase,
            run_id=run.name,
            outerlist_count=len(persisted_outerlist),
            api_success_count=api_success_count,
            mode2_recovered_count=mode2_recovered_count,
            failed_ids=failures,
            baseline_path=baseline,
            workbook=output if output.exists() else None,
            errors=[*result_errors, f'exception_type={type(exc).__name__}'],
        )
        try:
            write_run_result(run, failed_result)
        except Exception:
            pass
        raise

    if failures:
        raise SystemExit('❌ Trip HK 商品销量接口抓取部分失败：' + str(len(failures)) + '\nMEDIA:' + str(output))
    print(
        f'✅ Trip HK 商品销量与评分接口抓取完成｜{day.strftime("%Y年%m月%d日")}\n'
        f'Outerlist表：{len(persisted_outerlist)} 个去重商品｜HK接口记录：{len(items)}\n'
        f'销量及评分变化：基于{(baseline.name if baseline else "无昨日基准")}；评分取接口总分，变化列紧随总分。\n'
        f'MEDIA:{output}'
    )


if __name__ == '__main__':
    main()
