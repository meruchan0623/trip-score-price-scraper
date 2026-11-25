import hashlib
import json
from datetime import date
from pathlib import Path

import openpyxl
import pytest

from scripts import trip_outerlist_mode2_api_daily as collector


def test_detail_validates_real_page_model_common_header_path():
    page_model = {
        'ds': {'getProductInfo': [{'productId': 101}]},
        'commonHeaderData': {'site': 'HK', 'locale': 'zh-HK', 'currency': 'HKD'},
    }
    data = {'props': {'pageProps': {'initialState': {'__STATE_MAP__': {'PageModel': page_model}}}}}
    def html():
        return '<script id="__NEXT_DATA__" type="application/json">' + json.dumps(data) + '</script>'
    assert collector.detail_product_from_html(html(), 'hk', 'zh-HK', 'HKD')['productId'] == 101
    page_model['commonHeaderData']['currency'] = 'USD'
    with pytest.raises(RuntimeError, match='market context mismatch'):
        collector.detail_product_from_html(html(), 'hk', 'zh-HK', 'HKD')


def _source(tmp_path, *, corrupt=None):
    day = date.today()
    run = tmp_path / (day.strftime('%y%m%d') + '-mode2-outerlist-api-daily-source')
    run.mkdir()
    book = run / 'source.xlsx'
    items = [{'id': value, 'vendor': 'Vendor'} for value in ('101', '102', '103')]
    collector.create_outerlist_book(book, day, Path('original-outerlist'), [
        {'厂商': 'Vendor', '商品ID': item['id']} for item in items
    ])
    good = {'厂商': 'Vendor', '商品ID': '101', 'URL': 'https://hk.trip.com/things-to-do/detail/101/?curr=HKD&locale=zh-HK',
            '商品名称': 'original good', '月销量': '1', '年销量': '2', '总销量': '3', '评价数': '0', '总分': ''}
    missing = {'厂商': 'Vendor', '商品ID': '102', 'URL': 'https://hk.trip.com/things-to-do/detail/102/?curr=HKD&locale=zh-HK',
               '商品名称': '', '月销量': '2', '年销量': '3', '总销量': '4', '评价数': '', '总分': ''}
    noted = {'厂商': 'Vendor', '商品ID': '103', 'URL': 'https://hk.trip.com/things-to-do/detail/103/?curr=HKD&locale=zh-HK',
             '商品名称': 'must recover', '月销量': '3', '年销量': '4', '总销量': '5', '评价数': '0', '总分': '', '抓取备注': 'risk blocked'}
    collector.add_sales_sheets(book, day, {'HK': [good, missing, noted]}, {}, None)
    failed = ['HK/102', 'HK/103']
    if corrupt == 'duplicate':
        failed.append('HK/102')
    if corrupt == 'mismatch':
        failed = ['HK/101']
    (run / 'run_result.json').write_text(json.dumps({'status': 'failed', 'workbook': str(book), 'failed_ids': failed}), encoding='utf-8')
    return run, book


def test_resume_only_refetches_incomplete_or_noted_rows_and_preserves_source(tmp_path, monkeypatch):
    source, book = _source(tmp_path)
    before = hashlib.sha256(book.read_bytes()).hexdigest()
    calls = []
    monkeypatch.setattr(collector, 'previous_metrics', lambda *_: ({}, None))
    monkeypatch.setattr(collector, 'browser_recovery_replacements', lambda *_: (_ for _ in ()).throw(AssertionError('no browser')))
    def detail(site, locale, currency, item):
        calls.append(item['id'])
        return {'厂商': item['vendor'], '商品ID': item['id'], 'URL': f'https://{site}.trip.com/things-to-do/detail/{item["id"]}/?curr={currency}&locale={locale}',
                '商品名称': 'recovered ' + item['id'], '月销量': '7', '年销量': '8', '总销量': '9', '评价数': '0', '总分': ''}
    monkeypatch.setattr(collector, 'detail_fetch', detail)
    collector.main(['--output-root', str(tmp_path), '--run-suffix', 'recovery', '--resume-run-dir', str(source), '--failed-only', '--data-source', 'detail'])
    assert calls == ['102', '103']
    assert hashlib.sha256(book.read_bytes()).hexdigest() == before
    run = tmp_path / (date.today().strftime('%y%m%d') + '-mode2-outerlist-api-daily-recovery')
    result = json.loads((run / 'run_result.json').read_text())
    assert result['outerlist_count'] == '3'
    assert result['reused_count'] == '1' and result['newly_collected_count'] == '2' and result['recovery_success_count'] == '2'
    assert result['resume_source_run'] == source.name and result['data_source'] == 'detail'
    wb = openpyxl.load_workbook(result['workbook'], data_only=True)
    assert wb['Outerlist商品清单']['A1'].value == openpyxl.load_workbook(book, data_only=True)['Outerlist商品清单']['A1'].value
    rows = list(wb['HK'].iter_rows(min_row=3, values_only=True))
    assert [str(row[1]) for row in rows] == ['101', '102', '103']
    assert rows[0][3] == 'original good'


@pytest.mark.parametrize('corrupt', ['duplicate', 'mismatch'])
def test_corrupt_resume_rejected_before_any_network(tmp_path, monkeypatch, corrupt):
    source, _ = _source(tmp_path, corrupt=corrupt)
    monkeypatch.setattr(collector, 'detail_fetch', lambda *_: pytest.fail('network used'))
    with pytest.raises(SystemExit, match='failed IDs inconsistent'):
        collector.main(['--output-root', str(tmp_path), '--run-suffix', 'recovery', '--resume-run-dir', str(source), '--failed-only', '--data-source', 'detail'])
