import importlib.util
import os
import tempfile
import time
import unittest
import zipfile
from datetime import datetime
from pathlib import Path
from xml.sax.saxutils import escape


ROOT = Path(__file__).resolve().parents[1]
PLUGIN_PATH = (
    ROOT
    / "hermes-agent"
    / "plugins"
    / "trip-manual-delivery-guard"
    / "__init__.py"
)
FIXTURE_PATH = ROOT / "tests" / "fixtures" / "hermes_five_product_plaintext.txt"


def load_plugin():
    spec = importlib.util.spec_from_file_location("trip_manual_delivery_guard", PLUGIN_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load plugin: {PLUGIN_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class FakePluginContext:
    def __init__(self):
        self.hooks = {}

    def register_hook(self, name, callback):
        self.hooks[name] = callback


class TripManualDeliveryGuardTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.plugin = load_plugin()

    def make_workbook(
        self,
        root,
        product_ids,
        age_seconds=0,
        filename_age_seconds=0,
        markets=None,
        invalid=None,
    ):
        output_dir = root / f"manual-{'-'.join(product_ids)}"
        output_dir.mkdir(parents=True)
        filename_time = time.time() - filename_age_seconds
        timestamp = datetime.fromtimestamp(filename_time).strftime("%y%m%d-%H%M")
        workbook = output_dir / f"trip_scoreandprice_results_{timestamp}.xlsx"
        markets = markets or (("Trip商品数据", "hk.trip.com", "zh-HK", "HKD"),)
        self.write_native_workbook(workbook, product_ids, markets, invalid=invalid)
        modified_at = time.time() - age_seconds
        os.utime(workbook, (modified_at, modified_at))
        return workbook

    def write_native_workbook(self, workbook, product_ids, markets, invalid=None):
        """Create a minimal, real XLSX fixture using only ZIP/XML stdlib APIs."""

        headers = [
            "商品ID", "URL", "商品名称", "SKU层级1", "货币", "外显价格", "实价",
            "促销标签", "价格可信标签", "商品资讯", "总促销标签", "月销量", "年销量",
            "总销量", "评价数", "评分", "好评率", "客户服务", "方便程度", "讯号状态", "中差评价数",
        ]
        if invalid == "reordered":
            headers[3], headers[4] = headers[4], headers[3]
        if invalid == "missing":
            headers.remove("价格可信标签")
        if invalid == "extra":
            headers.append("最低价")
        if invalid == "truncated":
            headers = headers[:6]
        if invalid == "raw_capture":
            headers.insert(headers.index("价格可信标签") + 1, "原始捕获价格")

        def column_name(index):
            name = ""
            while index:
                index, remainder = divmod(index - 1, 26)
                name = chr(65 + remainder) + name
            return name

        def sheet_xml(product_id, host, locale, currency):
            values = [
                product_id,
                f"https://{host}/things-to-do/detail/{product_id}/?locale={locale}&curr={currency}",
                "Native product",
                "One day",
                "CNY" if invalid == "currency_mismatch" else currency,
                "" if invalid == "empty_price" else "100",
                "",
                "",
                "risk_blocked" if invalid == "risk" else (
                    "untrusted_price_missing" if invalid == "untrusted" else "trusted_base"
                ),
                "100" if invalid == "raw_capture" else "",
                "",
                "",
                "", "", "", "", "", "", "", "", "", "",
            ]
            if invalid == "summary":
                values[2] = "产品价格摘要"
            rows = [headers, values]
            rendered_rows = []
            for row_number, row in enumerate(rows, 1):
                cells = []
                for col_number, value in enumerate(row, 1):
                    cells.append(
                        f'<c r="{column_name(col_number)}{row_number}" t="inlineStr"><is><t>{escape(value)}</t></is></c>'
                    )
                rendered_rows.append(f'<row r="{row_number}">{"".join(cells)}</row>')
            return (
                '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">'
                f'<sheetData>{"".join(rendered_rows)}</sheetData></worksheet>'
            )

        sheet_entries = list(markets)
        if invalid == "extra_sheet":
            sheet_entries.append(("摘要", "hk.trip.com", "zh-HK", "HKD"))
        if invalid == "summary":
            sheet_entries[0] = ("摘要", *sheet_entries[0][1:])
        workbook_xml_sheets = "".join(
            f'<sheet name="{escape(sheet[0])}" sheetId="{index}" r:id="rId{index}"/>'
            for index, sheet in enumerate(sheet_entries, 1)
        )
        workbook_xml = (
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" '
            'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
            f'<sheets>{workbook_xml_sheets}</sheets></workbook>'
        )
        relationships = "".join(
            f'<Relationship Id="rId{index}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" '
            f'Target="worksheets/sheet{index}.xml"/>'
            for index in range(1, len(sheet_entries) + 1)
        )
        content_types = "".join(
            f'<Override PartName="/xl/worksheets/sheet{index}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
            for index in range(1, len(sheet_entries) + 1)
        )
        with zipfile.ZipFile(workbook, "w", zipfile.ZIP_DEFLATED) as archive:
            archive.writestr(
                "[Content_Types].xml",
                '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
                '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
                '<Default Extension="xml" ContentType="application/xml"/>'
                '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
                f'{content_types}</Types>',
            )
            archive.writestr(
                "_rels/.rels",
                '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>'
                '</Relationships>',
            )
            archive.writestr("xl/workbook.xml", workbook_xml)
            archive.writestr(
                "xl/_rels/workbook.xml.rels",
                '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
                '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                f'{relationships}</Relationships>',
            )
            for index, (_, host, locale, currency) in enumerate(sheet_entries, 1):
                product_id = product_ids[min(index - 1, len(product_ids) - 1)]
                archive.writestr(
                    f"xl/worksheets/sheet{index}.xml",
                    sheet_xml(product_id, host, locale, currency),
                )

    def test_single_product_success_returns_only_native_workbook_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",))
            turn = self.plugin.detect_manual_request(
                "【請抓取產品價格71827107】",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(
                f"抓取成功，共68个SKU。\nMEDIA:{workbook}", turn
            )

            self.assertEqual(result, str(workbook))

    def test_five_product_summary_keeps_only_normalized_warning_and_workbook(self):
        product_ids = ("105592323", "91391102", "53820320", "54247970", "86626309")
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, product_ids)
            summary = FIXTURE_PATH.read_text(encoding="utf-8")
            turn = self.plugin.detect_manual_request(
                "請抓取產品價格 " + " ".join(product_ids),
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(
                f"{summary}\nMEDIA:{workbook}", turn
            )

            self.assertEqual(
                result,
                "抓取重要警告：54247970 部分SKU层级点击失败，结果可能不完整。"
                f"\n{workbook}",
            )
            for forbidden in ("SKU价格汇总", "HK$0.72", "起价对比", "共462个SKU"):
                self.assertNotIn(forbidden, result)

    def test_missing_workbook_returns_fixed_failure_only(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(
                "抓取完成，共68个SKU，最低价HK$1.20。", turn
            )

            self.assertEqual(
                result,
                f"抓取失败：未生成原生Excel（{output_root / 'manual-71827107'}）",
            )

    def test_excel_validation_failure_uses_result_anomaly_prefix(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",))
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(
                "抓取结果异常：原生Excel校验未通过，缺少Trip商品数据工作表。",
                turn,
            )

            self.assertEqual(
                result,
                f"抓取结果异常：原生Excel校验未通过。\n{workbook}",
            )

    def test_api_blocking_is_normalized_without_business_summary(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",))
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(
                "warning: API blocked for 71827107；成功SKU数68；最低价HK$1.20。",
                turn,
            )

            self.assertEqual(
                result,
                f"抓取重要警告：检测到反爬/API拦截，结果可能不完整。\n{workbook}",
            )

    def test_stale_workbook_is_not_reused(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",), age_seconds=1)
            turn = self.plugin.detect_manual_request(
                "請抓取產品價格71827107",
                [],
                output_root=output_root,
                started_at=time.time(),
            )

            result = self.plugin.filter_manual_response(str(workbook), turn)

            self.assertTrue(result.startswith("抓取失败：未生成原生Excel"))

    def test_old_timestamp_workbook_is_not_reused_after_being_touched(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(
                output_root,
                ("71827107",),
                filename_age_seconds=3600,
            )
            turn = self.plugin.detect_manual_request(
                "請抓取產品價格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(str(workbook), turn)

            self.assertTrue(result.startswith("抓取失败：未生成原生Excel"))

    def test_invalid_timestamp_workbook_is_ignored(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            output_dir = output_root / "manual-71827107"
            output_dir.mkdir()
            workbook = output_dir / "trip_scoreandprice_results_999999-9999.xlsx"
            workbook.write_bytes(b"invalid timestamp")
            turn = self.plugin.detect_manual_request(
                "請抓取產品價格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(str(workbook), turn)

            self.assertTrue(result.startswith("抓取失败：未生成原生Excel"))

    def test_id_only_followup_requires_prior_manual_scrape_intent(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            history = [{"role": "user", "content": "請抓取以下產品價格"}]

            detected = self.plugin.detect_manual_request(
                "105592323 91391102",
                history,
                output_root=output_root,
                started_at=100.0,
            )
            unrelated = self.plugin.detect_manual_request(
                "105592323 91391102",
                [],
                output_root=output_root,
                started_at=100.0,
            )

            self.assertEqual(detected.product_ids, ("105592323", "91391102"))
            self.assertIsNone(unrelated)

    def test_id_only_followup_requires_immediately_previous_user_declaration(self):
        history = [
            {"role": "user", "content": "請抓取以下產品價格"},
            {"role": "assistant", "content": "請提供產品ID"},
            {"role": "user", "content": "稍后再处理，先查询订单"},
        ]

        detected = self.plugin.detect_manual_request(
            "105592323",
            history,
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertIsNone(detected)

    def test_generic_anomaly_prefix_is_normalized_without_business_summary(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",))
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            result = self.plugin.filter_manual_response(
                "抓取结果异常：价格字段不完整，成功SKU数68，最低价HK$1.20。",
                turn,
            )

            self.assertEqual(
                result,
                f"抓取结果异常：抓取结果可能不完整。\n{workbook}",
            )

    def test_trip_detail_url_is_a_manual_scrape_trigger(self):
        with tempfile.TemporaryDirectory() as tmp:
            turn = self.plugin.detect_manual_request(
                "请抓取这个链接的价格 https://hk.trip.com/things-to-do/detail/109223304/",
                [],
                output_root=Path(tmp),
                started_at=100.0,
            )

            self.assertEqual(turn.product_ids, ("109223304",))

    def test_trip_url_host_is_ignored_unless_the_market_is_explicit_outside_the_url(self):
        url_only = self.plugin.detect_manual_request(
            "请抓取价格 https://sg.trip.com/things-to-do/detail/109223304/",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )
        explicit_host = self.plugin.detect_manual_request(
            "请抓取价格 https://hk.trip.com/things-to-do/detail/109223304/，使用 sg.trip.com 市场",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertEqual(url_only.markets[0].code, "hk")
        self.assertEqual(explicit_host.markets[0].code, "sg")

    def test_overlapping_market_aliases_choose_the_longest_non_overlapping_match(self):
        turn = self.plugin.detect_manual_request(
            "请抓取产品价格71827107 的印度尼西亚价格",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertEqual([(market.code, market.currency) for market in turn.markets], [("id", "IDR")])

    def test_short_price_request_with_id_is_a_manual_scrape_trigger(self):
        with tempfile.TemporaryDirectory() as tmp:
            turn = self.plugin.detect_manual_request(
                "抓價格 ID: 109223304（新加坡站連結）",
                [],
                output_root=Path(tmp),
                started_at=100.0,
            )

            self.assertEqual(turn.product_ids, ("109223304",))

    def test_market_intent_defaults_to_hong_kong_and_keeps_single_market_flags(self):
        turn = self.plugin.detect_manual_request(
            "请抓取产品价格 71827107",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertEqual(turn.markets[0].code, "hk")
        self.assertEqual(turn.markets[0].host, "hk.trip.com")
        self.assertEqual(turn.markets[0].locale, "zh-HK")
        self.assertEqual(turn.markets[0].currency, "HKD")
        self.assertFalse(turn.multi_market)
        context = self.plugin._manual_context(turn)
        self.assertNotIn("--market", context)
        self.assertNotIn("--site", context)

    def test_currency_only_market_uses_recommended_site_and_explicit_site_overrides_currency(self):
        usd_turn = self.plugin.detect_manual_request(
            "抓取产品价格 71827107 的美元价格",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )
        jp_usd_turn = self.plugin.detect_manual_request(
            "抓取产品价格 71827107 的日本站美元价格",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertEqual((usd_turn.markets[0].code, usd_turn.markets[0].currency), ("us", "USD"))
        self.assertEqual((jp_usd_turn.markets[0].code, jp_usd_turn.markets[0].currency), ("jp", "USD"))
        self.assertIn("--site jp --locale ja-JP --currency USD", self.plugin._manual_context(jp_usd_turn))

    def test_multi_market_preserves_appearance_order_and_deduplicates_exact_contexts(self):
        turn = self.plugin.detect_manual_request(
            "请抓取产品价格71827107 的日本、香港、日本价格",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertEqual([market.code for market in turn.markets], ["jp", "hk"])
        self.assertTrue(turn.multi_market)
        context = self.plugin._manual_context(turn)
        self.assertIn("--market jp:ja-JP:JPY --market hk:zh-HK:HKD", context)
        self.assertNotIn("--site", context)

    def test_multi_currency_contexts_keep_same_site_when_the_currency_differs(self):
        turn = self.plugin.detect_manual_request(
            "请抓取产品价格71827107 的日本日元、日本美元价格",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertEqual(
            [(market.code, market.currency) for market in turn.markets],
            [("jp", "JPY"), ("jp", "USD")],
        )
        self.assertIn("--market jp:ja-JP:JPY --market jp:ja-JP:USD", self.plugin._manual_context(turn))

    def test_ambiguous_market_prompt_injects_clarification_and_never_a_launch_command(self):
        turn = self.plugin.detect_manual_request(
            "请抓取产品价格71827107 的美元区或欧洲价格",
            [],
            output_root=Path("/tmp"),
            started_at=100.0,
        )

        self.assertTrue(turn.clarification)
        context = self.plugin._manual_context(turn)
        self.assertIn("请明确", context)
        self.assertNotIn("./trip-score-price-scraper", context)
        self.assertNotIn("--market", context)

    def test_supported_market_registry_contains_all_33_code_host_and_chinese_name_entries(self):
        self.assertEqual(len(self.plugin.SUPPORTED_MARKETS), 33)
        for market in self.plugin.SUPPORTED_MARKETS:
            self.assertTrue(market.code)
            self.assertTrue(market.host.endswith("trip.com"))
            self.assertTrue(market.chinese_names)

    def test_real_native_xlsx_requires_expected_market_sheets_and_trusted_rows(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(
                output_root,
                ("71827107",),
                markets=(
                    ("HK-HKD", "hk.trip.com", "zh-HK", "HKD"),
                    ("JP-JPY", "jp.trip.com", "ja-JP", "JPY"),
                ),
            )
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107 的香港、日本价格",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            self.assertEqual(self.plugin.filter_manual_response("完成", turn), str(workbook))

    def test_real_native_xlsx_rejects_structural_and_price_trust_violations(self):
        for invalid in (
            "extra_sheet", "reordered", "missing", "extra", "truncated", "empty_price", "summary",
            "currency_mismatch", "risk", "untrusted",
        ):
            with self.subTest(invalid=invalid), tempfile.TemporaryDirectory() as tmp:
                output_root = Path(tmp)
                workbook = self.make_workbook(output_root, ("71827107",), invalid=invalid)
                turn = self.plugin.detect_manual_request(
                    "请抓取产品价格71827107",
                    [],
                    output_root=output_root,
                    started_at=time.time() - 10,
                )

                result = self.plugin.filter_manual_response("完成", turn)

                self.assertEqual(result, "抓取结果异常：原生Excel校验未通过。\n" + str(workbook))

    def test_native_validator_allows_existing_raw_capture_column_without_exposing_it(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",), invalid="raw_capture")
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            valid, reason = self.plugin.validate_native_workbook(workbook, turn)

            self.assertTrue(valid, reason)
            self.assertEqual(self.plugin.filter_manual_response("完成", turn), str(workbook))

    def test_partial_market_delivery_requires_ordered_nonempty_subset_and_matching_failure_label(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(
                output_root,
                ("71827107",),
                markets=(("HK-HKD", "hk.trip.com", "zh-HK", "HKD"),),
            )
            turn = self.plugin.detect_manual_request(
                "请抓取产品价格71827107 的香港、日本价格",
                [],
                output_root=output_root,
                started_at=time.time() - 10,
            )

            allowed = self.plugin.filter_manual_response("JP-JPY 部分产品抓取失败", turn)
            no_label = self.plugin.filter_manual_response("部分产品抓取失败", turn)
            wrong_label = self.plugin.filter_manual_response("HK-HKD 部分产品抓取失败", turn)

            self.assertEqual(
                allowed,
                "抓取重要警告：JP-JPY 抓取失败，附件仅包含成功市场\n" + str(workbook),
            )
            self.assertEqual(no_label, "抓取结果异常：原生Excel校验未通过。\n" + str(workbook))
            self.assertEqual(wrong_label, "抓取结果异常：原生Excel校验未通过。\n" + str(workbook))

    def test_registered_hooks_filter_only_matching_turn(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            context = FakePluginContext()
            self.plugin.register(context, output_root=output_root)

            injected = context.hooks["pre_llm_call"](
                session_id="telegram-session",
                user_message="【請抓取產品價格71827107】",
                conversation_history=[],
                model="test-model",
                platform="telegram",
            )
            workbook = self.make_workbook(output_root, ("71827107",))
            filtered = context.hooks["transform_llm_output"](
                response_text=f"产品价格汇总 HK$1.20\n{workbook}",
                session_id="telegram-session",
                model="test-model",
                platform="telegram",
            )
            untouched = context.hooks["transform_llm_output"](
                response_text="普通对话",
                session_id="another-session",
                model="test-model",
                platform="telegram",
            )

            self.assertIn("只交付原生Excel附件", injected["context"])
            self.assertEqual(filtered, str(workbook))
            self.assertIsNone(untouched)

    def test_registered_hooks_keep_concurrent_sessions_isolated(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            context = FakePluginContext()
            self.plugin.register(context, output_root=output_root)
            context.hooks["pre_llm_call"](
                session_id="manual-a",
                user_message="請抓取產品價格71827107",
                conversation_history=[],
            )
            context.hooks["pre_llm_call"](
                session_id="manual-b",
                user_message="請抓取產品價格109223304",
                conversation_history=[],
            )
            workbook_a = self.make_workbook(output_root, ("71827107",))
            workbook_b = self.make_workbook(output_root, ("109223304",))

            result_b = context.hooks["transform_llm_output"](
                response_text="B产品汇总",
                session_id="manual-b",
            )
            result_a = context.hooks["transform_llm_output"](
                response_text="A产品汇总",
                session_id="manual-a",
            )

            self.assertEqual(result_b, str(workbook_b))
            self.assertEqual(result_a, str(workbook_a))


if __name__ == "__main__":
    unittest.main()
