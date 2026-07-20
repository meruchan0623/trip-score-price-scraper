import importlib.util
import os
import tempfile
import time
import unittest
from datetime import datetime
from pathlib import Path


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
    ):
        output_dir = root / f"manual-{'-'.join(product_ids)}"
        output_dir.mkdir(parents=True)
        filename_time = time.time() - filename_age_seconds
        timestamp = datetime.fromtimestamp(filename_time).strftime("%y%m%d-%H%M")
        workbook = output_dir / f"trip_scoreandprice_results_{timestamp}.xlsx"
        workbook.write_bytes(b"native workbook placeholder")
        modified_at = time.time() - age_seconds
        os.utime(workbook, (modified_at, modified_at))
        return workbook

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
