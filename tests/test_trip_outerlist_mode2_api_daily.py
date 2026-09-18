import json
from contextlib import contextmanager
from pathlib import Path
from types import SimpleNamespace

import openpyxl
import pytest

from scripts import trip_outerlist_mode2_api_daily as collector


def test_parse_config_preserves_legacy_defaults():
    config = collector.parse_config([])

    assert config.workers == 45
    assert config.sites == ('hk',)
    assert config.mode2_fallback == 'auto'
    assert config.mode2_workers == 5
    assert config.mode2_workers_explicit is False
    assert config.api_retries == 0
    assert config.api_retry_interval_sec == 0.0
    assert config.resume_run_dir is None
    assert config.failed_only is False


def test_parse_config_accepts_valid_policy_values(tmp_path):
    resume_dir = tmp_path / 'prior-run'
    config = collector.parse_config([
        '--workers', '8',
        '--sites', 'hk,tw',
        '--output-root', str(tmp_path),
        '--baseline-workbook', 'baseline.xlsx',
        '--run-suffix', 'manual',
        '--mode2-fallback', 'off',
        '--mode2-workers', '3',
        '--api-retries', '2',
        '--api-retry-interval-sec', '0.25',
        '--resume-run-dir', str(resume_dir),
        '--failed-only',
    ])

    assert config.workers == 8
    assert config.sites == ('hk', 'tw')
    assert config.output_root == tmp_path
    assert config.baseline_workbook == Path('baseline.xlsx')
    assert config.run_suffix == 'manual'
    assert config.mode2_fallback == 'off'
    assert config.mode2_workers == 3
    assert config.mode2_workers_explicit is True
    assert config.api_retries == 2
    assert config.api_retry_interval_sec == 0.25
    assert config.resume_run_dir == resume_dir
    assert config.failed_only is True


@pytest.mark.parametrize(
    ('option', 'value'),
    [
        ('--workers', '0'),
        ('--workers', '-1'),
        ('--mode2-workers', '0'),
        ('--mode2-workers', '-1'),
        ('--mode2-workers', '6'),
        ('--api-retries', '-1'),
        ('--api-retry-interval-sec', '-0.1'),
        ('--api-retry-interval-sec', 'nan'),
        ('--api-retry-interval-sec', 'inf'),
    ],
)
def test_parse_config_rejects_invalid_numeric_values(option, value):
    with pytest.raises(SystemExit):
        collector.parse_config([option, value])


def test_parse_config_rejects_invalid_mode2_policy():
    with pytest.raises(SystemExit):
        collector.parse_config(['--mode2-fallback', 'sometimes'])


def test_runtime_policy_accepts_mode2_and_still_rejects_resume_requests(tmp_path):
    required = collector.parse_config(['--mode2-fallback', 'required'])
    collector.validate_runtime_policy(required)

    explicit_workers = collector.parse_config(['--mode2-workers', '3'])
    collector.validate_runtime_policy(explicit_workers)

    resume = collector.parse_config(['--resume-run-dir', str(tmp_path), '--failed-only'])
    with pytest.raises(collector.UnsupportedPolicyError, match='resume'):
        collector.validate_runtime_policy(resume)


def test_build_run_result_is_serializable_text_compatible_and_credential_free(tmp_path):
    result = collector.build_run_result(
        status='failed',
        phase='api_collection',
        run_id='260908-mode2-outerlist-api-daily',
        outerlist_count=12,
        api_success_count=10,
        mode2_recovered_count=0,
        failed_ids=['HK/123', 456],
        baseline_path=tmp_path / 'baseline.xlsx',
        workbook=tmp_path / 'daily.xlsx',
        errors=['Authorization: Bearer secret-token; api_key=hunter2'],
    )

    encoded = json.dumps(result)
    assert result['status'] == 'failed'
    assert result['phase'] == 'api_collection'
    assert result['run_id'] == '260908-mode2-outerlist-api-daily'
    assert result['outerlist_count'] == '12'
    assert result['api_success_count'] == '10'
    assert result['mode2_recovered_count'] == '0'
    assert result['failed_ids'] == ['HK/123', '456']
    assert result['delta_basis'] == 'twoYearSales'
    assert 'yearlySales' not in encoded
    assert result['baseline_path'] == str(tmp_path / 'baseline.xlsx')
    assert result['workbook'] == str(tmp_path / 'daily.xlsx')
    assert 'secret-token' not in encoded
    assert 'hunter2' not in encoded


@pytest.mark.parametrize('error', [
    "headers={'Authorization': 'Bearer real-secret'}",
    'https://example.invalid/path?api_key=real-secret',
    'Bearer real-secret',
    'access_token=real-secret',
    'refresh_token=real-secret',
    'client_secret=real-secret',
    'X-Amz-Credential=real-secret',
    'X-Amz-Signature=real-secret',
    'timeout while connecting',
])
def test_run_result_allows_only_reviewed_diagnostics(error):
    result = collector.build_run_result(status='failed', phase='api_collection', run_id='run', errors=[error])

    encoded = json.dumps(result)
    assert result['errors'] == ['[REDACTED]']
    assert 'real-secret' not in encoded


def test_run_result_preserves_allowlisted_diagnostics():
    result = collector.build_run_result(
        status='failed', phase='api_collection', run_id='run',
        errors=['Mode2 recovery incomplete; failed API rows remain',
                'exception_type=TimeoutError'],
    )

    assert result['errors'] == [
        'Mode2 recovery incomplete; failed API rows remain',
        'exception_type=TimeoutError',
    ]


def test_clear_completion_marker_removes_stale_marker_from_failed_terminal_path(tmp_path):
    marker = tmp_path / '.collection_done'
    marker.write_text('{"status": "success"}', encoding='utf-8')

    collector.clear_completion_marker(tmp_path)

    assert not marker.exists()


def test_previous_metrics_ignores_failed_completed_marker_but_accepts_legacy(tmp_path, monkeypatch):
    target_day = collector.date(2026, 9, 8)
    legacy = tmp_path / '260907-legacy'
    failed = tmp_path / '260907-failed'
    for run in (legacy, failed):
        run.mkdir()
        (run / '.collection_done').write_text('{}', encoding='utf-8')
        (run / '260907-0600_Trip_DailySalesVolume_FromOuterlist.xlsx').write_bytes(b'placeholder')
    (failed / 'run_result.json').write_text(json.dumps({'status': 'failed'}), encoding='utf-8')
    monkeypatch.setattr(collector, 'metrics_from_book', lambda book: {'selected': book.parent.name})

    metrics, selected_run = collector.previous_metrics(tmp_path, target_day)

    assert selected_run == legacy
    assert metrics == {'selected': '260907-legacy'}


def test_fetch_keeps_yearly_sales_separate_from_two_year_delta_basis(monkeypatch):
    response_data = {
        'ResponseStatus': {'Ack': 'Success'},
        'productInfos': [{
            'productId': '123',
            'salesVolume': {
                'monthlySales': 7,
                'yearlySales': 99,
                'twoYearSales': 200,
            },
            'commentInfo': {},
            'basicInfo': {},
        }],
    }

    class Response:
        def __enter__(self):
            return self

        def __exit__(self, *_args):
            return None

    @contextmanager
    def fake_urlopen(_request, timeout):
        assert timeout == 35
        response = Response()
        response.read = lambda: json.dumps(response_data).encode()
        yield response

    monkeypatch.setattr(collector.urllib.request, 'urlopen', fake_urlopen)
    row = collector.fetch('hk', 'zh-HK', 'HKD', {'id': '123', 'vendor': 'vendor'})

    assert row['年销量'] == '99'
    assert row['总销量'] == '200'


def _run_offline_main(monkeypatch, tmp_path, api_rows, mode2_rows, *, policy, workers=5, previous=None):
    product_ids = list(api_rows)
    source = tmp_path / 'outerlist-source'
    outer_rows = [
        {
            '厂商': 'Vendor',
            '商品ID': product_id,
            'URL': f'https://hk.trip.com/things-to-do/detail/{product_id}/',
            '商品名称': f'Outer {product_id}',
        }
        for product_id in product_ids
    ]
    monkeypatch.setattr(collector, 'latest_outerlist', lambda: (source, [{}]))
    monkeypatch.setattr(collector, 'outerlist_rows', lambda _items: outer_rows)
    monkeypatch.setattr(collector, 'previous_metrics', lambda _root, _day: (previous or {}, None))

    def fake_fetch(_site, _locale, _currency, item, **_kwargs):
        row = {
            '厂商': item['vendor'],
            '商品ID': item['id'],
            'URL': f'https://hk.trip.com/things-to-do/detail/{item["id"]}/',
        }
        row.update(api_rows[item['id']])
        return row

    monkeypatch.setattr(collector, 'fetch', fake_fetch)
    commands = []

    def fake_mode2_command(command, _timeout_sec):
        command = list(command)
        commands.append(command)
        if 'build' in command:
            binary = Path(command[command.index('-o') + 1])
            binary.parent.mkdir(parents=True, exist_ok=True)
            binary.touch()
        if '-output' in command:
            output = Path(command[command.index('-output') + 1])
            output.parent.mkdir(parents=True, exist_ok=True)
            output.touch()
        return 0

    monkeypatch.setattr(collector, '_run_mode2_command', fake_mode2_command)

    def fake_mode2_read(output):
        urls = (Path(output).parent / 'urls.txt').read_text(encoding='utf-8').splitlines()
        ids = [url.rstrip('/').rsplit('/', 1)[-1] for url in urls]
        return {product_id: dict(mode2_rows[product_id]) for product_id in ids if product_id in mode2_rows}

    monkeypatch.setattr(collector, 'read_mode2_rows', fake_mode2_read, raising=False)
    args = [
        '--output-root', str(tmp_path),
        '--mode2-fallback', policy,
        '--mode2-workers', str(workers),
    ]
    return commands, args


def _only_run(tmp_path):
    return next(tmp_path.glob('*-mode2-outerlist-api-daily'))


def _sheet_rows(workbook, sheet):
    wb = openpyxl.load_workbook(workbook, read_only=True, data_only=True)
    ws = wb[sheet]
    headers = ['销量增量' if str(cell.value).startswith('销量增量') else cell.value for cell in ws[2]]
    rows = {
        str(values[headers.index('商品ID')]): {
            header: '' if values[index] is None else str(values[index])
            for index, header in enumerate(headers)
        }
        for values in ws.iter_rows(min_row=3, values_only=True)
    }
    wb.close()
    return rows


def _header_row(workbook, sheet):
    wb = openpyxl.load_workbook(workbook, read_only=True, data_only=True)
    headers = [cell.value for cell in wb[sheet][2]]
    wb.close()
    return headers


def _write_baseline_book(path, total):
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.title = 'HK'
    ws.append(['抓取日期'])
    ws.append(['商品ID', '总销量', '总分'])
    ws.append(['1', total, 4.5])
    path.parent.mkdir(parents=True, exist_ok=True)
    wb.save(path)


def test_sales_delta_header_uses_current_and_explicit_baseline_dates(tmp_path):
    day = collector.date(2026, 9, 14)
    baseline = tmp_path / '260913-mode2-outerlist-api-daily' / '260913-0800_Trip_DailySalesVolume_FromOuterlist.xlsx'
    _write_baseline_book(baseline, 100)
    output = tmp_path / 'out.xlsx'
    collector.create_outerlist_book(output, day, tmp_path, [])
    rows = {'HK': [{'厂商': 'V', '商品ID': '1', '总销量': '150', '总分': '4.7'}]}

    collector.add_sales_sheets(output, day, rows, collector.metrics_from_book(baseline), collector.baseline_day(baseline))

    headers = _header_row(output, 'HK')
    assert headers[6] == '销量增量（0914-0913）'
    assert headers[:6] == collector.HEADERS[:6] and headers[7:] == collector.HEADERS[7:]
    assert _sheet_rows(output, 'HK')['1']['销量增量'] == '50'


def test_sales_delta_header_reflects_explicit_baseline_from_a_different_day(tmp_path):
    day = collector.date(2026, 9, 14)
    baseline = tmp_path / '260901-1200_Trip_DailySalesVolume_FromOuterlist.xlsx'
    _write_baseline_book(baseline, 100)
    output = tmp_path / 'out.xlsx'
    collector.create_outerlist_book(output, day, tmp_path, [])

    collector.add_sales_sheets(output, day, {'HK': [{'商品ID': '1', '总销量': '120'}]}, collector.metrics_from_book(baseline), collector.baseline_day(baseline))

    assert _header_row(output, 'HK')[6] == '销量增量（0914-0901）'
    assert collector.baseline_day(tmp_path / '260913-mode2-outerlist-api-daily') == collector.date(2026, 9, 13)


def test_sales_delta_header_without_baseline_does_not_invent_a_date(tmp_path):
    day = collector.date(2026, 9, 14)
    output = tmp_path / 'out.xlsx'
    collector.create_outerlist_book(output, day, tmp_path, [])

    collector.add_sales_sheets(output, day, {'HK': [{'商品ID': '1', '总销量': '120'}]}, {}, collector.baseline_day(None))

    headers = _header_row(output, 'HK')
    assert headers[6] == '销量增量（无对比基线）'
    assert '0913' not in headers[6]
    assert len(headers) == len(collector.HEADERS)
    assert _sheet_rows(output, 'HK')['1']['销量增量'] == ''


def test_mode2_off_never_invokes_go_and_keeps_failed_run_incomplete(tmp_path, monkeypatch):
    commands, args = _run_offline_main(
        monkeypatch,
        tmp_path,
        {'1': {'抓取备注': 'api failed'}},
        {},
        policy='off',
    )

    with pytest.raises(SystemExit, match='部分失败'):
        collector.main(args)

    run = _only_run(tmp_path)
    result = json.loads((run / 'run_result.json').read_text(encoding='utf-8'))
    assert commands == []
    assert result['status'] == 'failed'
    assert result['mode2_recovered_count'] == '0'
    assert result['failed_ids'] == ['HK/1']
    assert not (run / '.collection_done').exists()


def test_mode2_auto_partitions_only_failed_ids_and_merges_without_zeroing_sales(tmp_path, monkeypatch):
    api_rows = {
        '1': {'商品名称': 'API success', '总销量': '100'},
        '2': {'商品名称': 'API old name', '总销量': '250', '抓取备注': 'api failed'},
        '3': {'抓取备注': 'api failed'},
        '4': {'抓取备注': 'api failed'},
    }
    mode2_rows = {
        '2': {
            '商品ID': '2',
            'URL': 'https://hk.trip.com/mode2/detail/2/',
            '商品名称': 'Mode2 name',
            '总销量': '',
            '评分': '4.7',
            '抓取备注': '',
        },
        '3': {'商品ID': '3', '商品名称': 'Recovered no sales', '总销量': '', '抓取备注': ''},
        '4': {'商品ID': '4', '总销量': '400', '抓取备注': ''},
    }
    previous = {
        ('HK', '2'): {'total': 200, 'score': None},
        ('HK', '3'): {'total': 10, 'score': None},
    }
    commands, args = _run_offline_main(
        monkeypatch,
        tmp_path,
        api_rows,
        mode2_rows,
        policy='auto',
        workers=2,
        previous=previous,
    )

    collector.main(args)

    run = _only_run(tmp_path)
    result = json.loads((run / 'run_result.json').read_text(encoding='utf-8'))
    worker_commands = [command for command in commands if '-run-mode' in command]
    sent_urls = []
    for command in worker_commands:
        assert command[command.index('-run-mode') + 1] == 'mode2'
        assert command[command.index('-delay-profile') + 1] == 'normal'
        assert command[command.index('-workers') + 1] == '1'
        assert '-no-headless' in command
        urls_file = Path(command[command.index('-urls-file') + 1])
        sent_urls.extend(urls_file.read_text(encoding='utf-8').splitlines())
    assert commands[0][1:] == ['build', '-o', commands[0][3], './cmd/trip-score-cdp']
    assert len(worker_commands) == 2
    assert sorted(sent_urls) == [
        'https://hk.trip.com/things-to-do/detail/2/',
        'https://hk.trip.com/things-to-do/detail/3/',
        'https://hk.trip.com/things-to-do/detail/4/',
    ]
    assert result['status'] == 'success'
    assert result['api_success_count'] == '1'
    assert result['mode2_recovered_count'] == '3'
    assert result['failed_ids'] == []
    assert (run / '.collection_done').exists()

    rows = _sheet_rows(Path(result['workbook']), 'HK')
    assert rows['2']['URL'] == 'https://hk.trip.com/mode2/detail/2/'
    assert rows['2']['商品名称'] == 'Mode2 name'
    assert rows['2']['总销量'] == '250'
    assert rows['2']['销量增量'] == '50'
    assert rows['2']['总分'] == '4.7'
    assert rows['3']['总销量'] == ''
    assert rows['3']['销量增量'] == ''
    assert rows['4']['总销量'] == '400'
    note = (run / '任务说明.md').read_text(encoding='utf-8')
    assert 'API-only 全部成功时不启动浏览器' in note
    assert 'API 失败且尝试恢复时，可能启动 headed Go Mode2' in note


def test_mode2_required_records_partial_recovery_as_non_success_without_marker(tmp_path, monkeypatch):
    api_rows = {
        '10': {'抓取备注': 'api failed'},
        '20': {'抓取备注': 'api failed'},
    }
    mode2_rows = {
        '10': {'商品ID': '10', '总销量': '500', '抓取备注': ''},
        '20': {'商品ID': '20', '总销量': '600', '抓取备注': 'mode2 failed'},
    }
    commands, args = _run_offline_main(
        monkeypatch,
        tmp_path,
        api_rows,
        mode2_rows,
        policy='required',
        workers=5,
    )

    with pytest.raises(SystemExit, match='部分失败'):
        collector.main(args)

    run = _only_run(tmp_path)
    result = json.loads((run / 'run_result.json').read_text(encoding='utf-8'))
    assert any('-run-mode' in command for command in commands)
    assert result['status'] == 'failed'
    assert result['phase'] == 'mode2_recovery'
    assert result['mode2_recovered_count'] == '1'
    assert result['failed_ids'] == ['HK/20']
    assert result['errors'] == ['Mode2 recovery incomplete; failed API rows remain']
    assert not (run / '.collection_done').exists()



def test_run_mode2_command_timeout_terminates_then_kills_process_group(monkeypatch):
    waits = []
    group_signals = []
    popen_calls = []

    class FakeProcess:
        pid = 4242

        def wait(self, timeout):
            waits.append(timeout)
            raise collector.subprocess.TimeoutExpired(cmd=['mode2'], timeout=timeout)

        def terminate(self):
            raise AssertionError('process-group TERM should be used')

        def kill(self):
            raise AssertionError('process-group KILL should be used')

    process = FakeProcess()

    def fake_popen(command, **kwargs):
        popen_calls.append((list(command), kwargs))
        return process

    monkeypatch.setattr(collector.subprocess, 'Popen', fake_popen)
    monkeypatch.setattr(
        collector.os,
        'killpg',
        lambda pid, signal_number: group_signals.append((pid, signal_number)),
    )

    assert collector._run_mode2_command(['fake-mode2'], 17) is None
    assert waits == [
        17,
        collector.MODE2_TERMINATE_GRACE_SEC,
        collector.MODE2_TERMINATE_GRACE_SEC,
    ]
    assert group_signals == [
        (4242, collector.signal.SIGTERM),
        (4242, collector.signal.SIGKILL),
    ]
    assert popen_calls == [(['fake-mode2'], {
        'cwd': collector.REPO_ROOT,
        'stdin': collector.subprocess.DEVNULL,
        'stdout': collector.subprocess.DEVNULL,
        'stderr': collector.subprocess.DEVNULL,
        'start_new_session': True,
    })]


def test_mode2_recovery_reports_go_build_failure_without_subprocess(tmp_path, monkeypatch):
    calls = []

    def fake_command(command, timeout_sec):
        calls.append((list(command), timeout_sec))
        return 2

    monkeypatch.setattr(collector, '_run_mode2_command', fake_command)

    recovered, errors = collector.run_mode2_recovery(tmp_path, ['1'], workers=1)

    assert recovered == {}
    assert errors == ['Mode2 recovery build failed']
    assert calls[0][1] == collector.MODE2_BUILD_TIMEOUT_SEC
    assert all('-run-mode' not in command for command, _timeout in calls)


def test_mode2_recovery_reports_nonzero_worker_without_subprocess(tmp_path, monkeypatch):
    timeouts = []

    def fake_command(command, timeout_sec):
        timeouts.append(timeout_sec)
        return 0 if 'build' in command else 9

    monkeypatch.setattr(collector, '_run_mode2_command', fake_command)

    recovered, errors = collector.run_mode2_recovery(tmp_path, ['1'], workers=1)

    assert recovered == {}
    assert errors == ['Mode2 recovery worker failed']
    assert timeouts == [
        collector.MODE2_BUILD_TIMEOUT_SEC,
        collector.MODE2_WORKER_TIMEOUT_SEC,
    ]


def test_mode2_recovery_reports_missing_worker_output_without_subprocess(tmp_path, monkeypatch):
    monkeypatch.setattr(collector, '_run_mode2_command', lambda _command, _timeout: 0)

    recovered, errors = collector.run_mode2_recovery(tmp_path, ['1'], workers=1)

    assert recovered == {}
    assert errors == ['Mode2 recovery output missing']


def test_mode2_recovery_reports_malformed_workbook_without_subprocess(tmp_path, monkeypatch):
    def fake_command(command, _timeout):
        if '-output' in command:
            output = Path(command[command.index('-output') + 1])
            output.write_bytes(b'not an xlsx workbook')
        return 0

    monkeypatch.setattr(collector, '_run_mode2_command', fake_command)

    recovered, errors = collector.run_mode2_recovery(tmp_path, ['1'], workers=1)

    assert recovered == {}
    assert errors == ['Mode2 recovery workbook invalid']


def test_mode2_worker_timeout_leaves_credential_free_failed_run_result(tmp_path, monkeypatch):
    _commands, args = _run_offline_main(
        monkeypatch,
        tmp_path,
        {'1': {'抓取备注': 'api failed'}},
        {},
        policy='auto',
    )

    def fake_timeout(command, timeout_sec):
        if 'build' in command:
            assert timeout_sec == collector.MODE2_BUILD_TIMEOUT_SEC
            return 0
        assert timeout_sec == collector.MODE2_WORKER_TIMEOUT_SEC
        return None

    monkeypatch.setattr(collector, '_run_mode2_command', fake_timeout)

    with pytest.raises(SystemExit, match='部分失败'):
        collector.main(args)

    run = _only_run(tmp_path)
    result_text = (run / 'run_result.json').read_text(encoding='utf-8')
    result = json.loads(result_text)
    assert result['status'] == 'failed'
    assert result['phase'] == 'mode2_recovery'
    assert result['failed_ids'] == ['HK/1']
    assert result['errors'] == [
        'Mode2 recovery worker failed',
        'Mode2 recovery incomplete; failed API rows remain',
    ]
    assert 'api failed' not in result_text
    assert not (run / '.collection_done').exists()


def test_empty_id_only_mode2_payload_does_not_clear_api_failure(tmp_path, monkeypatch):
    _commands, args = _run_offline_main(
        monkeypatch,
        tmp_path,
        {
            '1': {
                '商品名称': 'Preserve API name',
                '总销量': '250',
                '抓取备注': 'api failed',
            },
        },
        {'1': {'商品ID': '1', '抓取备注': ''}},
        policy='auto',
    )

    with pytest.raises(SystemExit, match='部分失败'):
        collector.main(args)

    run = _only_run(tmp_path)
    result = json.loads((run / 'run_result.json').read_text(encoding='utf-8'))
    assert result['status'] == 'failed'
    assert result['mode2_recovered_count'] == '0'
    assert result['failed_ids'] == ['HK/1']
    assert result['errors'] == ['Mode2 recovery incomplete; failed API rows remain']
    assert not (run / '.collection_done').exists()

    rows = _sheet_rows(Path(result['workbook']), 'HK')
    assert rows['1']['商品名称'] == 'Preserve API name'
    assert rows['1']['总销量'] == '250'
    assert rows['1']['抓取备注'] == 'api failed'


def test_url_only_mode2_payload_does_not_clear_api_failure(tmp_path, monkeypatch):
    api_url = 'https://hk.trip.com/things-to-do/detail/1/'
    _commands, args = _run_offline_main(
        monkeypatch,
        tmp_path,
        {
            '1': {
                '商品名称': 'Preserve API name',
                '总销量': '250',
                '抓取备注': 'api failed',
            },
        },
        {'1': {'商品ID': '1', 'URL': api_url, '抓取备注': ''}},
        policy='auto',
    )

    with pytest.raises(SystemExit, match='部分失败'):
        collector.main(args)

    run = _only_run(tmp_path)
    result = json.loads((run / 'run_result.json').read_text(encoding='utf-8'))
    assert result['status'] == 'failed'
    assert result['mode2_recovered_count'] == '0'
    assert result['failed_ids'] == ['HK/1']
    assert result['errors'] == ['Mode2 recovery incomplete; failed API rows remain']
    assert not (run / '.collection_done').exists()

    rows = _sheet_rows(Path(result['workbook']), 'HK')
    assert rows['1']['URL'] == api_url
    assert rows['1']['商品名称'] == 'Preserve API name'
    assert rows['1']['总销量'] == '250'
    assert rows['1']['抓取备注'] == 'api failed'
