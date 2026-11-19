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

    def make_semantic_workbook(
        self, root, product_ids, *, directory=None, age_seconds=0, batch=None
    ):
        output_dir = directory or root / f"manual-{'-'.join(product_ids)}"
        output_dir.mkdir(parents=True, exist_ok=True)
        stamp = datetime.now().strftime("%Y%m%d-%H%M")
        batch = "-".join(product_ids) if batch is None else batch
        workbook = output_dir / (
            f"Trip_Mode1_指定产品抓价_{batch}_{stamp}.xlsx"
        )
        workbook.write_bytes(b"renamed native workbook")
        modified_at = time.time() - age_seconds
        os.utime(workbook, (modified_at, modified_at))
        return workbook

    @staticmethod
    def write_receipt(workbook, *, target="telegram:offline_test_chat"):
        (workbook.parent / ".delivered.json").write_text(
            '{"status":"delivered","path":"' + str(workbook) + '","target":"'
            + target + '"}',
            encoding="utf-8",
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

    def test_semantic_renamed_delivered_receipt_is_checked_before_native_glob(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workbook = self.make_semantic_workbook(root, ("92237157",))
            self.write_receipt(workbook)
            turn = self.plugin.ManualTurn(("92237157",), workbook.parent, time.time() - 10)

            self.assertEqual(
                self.plugin.filter_manual_response(
                    str(workbook), turn, expected_target="telegram:offline_test_chat"
                ),
                "附件已发送。",
            )

    def test_semantic_renamed_undelivered_workbook_is_returned(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workbook = self.make_semantic_workbook(root, ("92237157",))
            turn = self.plugin.ManualTurn(("92237157",), workbook.parent, time.time() - 10)
            self.assertEqual(self.plugin.filter_manual_response(str(workbook), turn), str(workbook))

    def test_stale_semantic_workbook_and_valid_receipt_are_not_reused(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workbook = self.make_semantic_workbook(root, ("92237157",), age_seconds=60)
            self.write_receipt(workbook)
            turn = self.plugin.ManualTurn(("92237157",), workbook.parent, time.time())
            self.assertTrue(
                self.plugin.filter_manual_response(str(workbook), turn).startswith("抓取失败")
            )

    def test_stale_valid_receipt_does_not_block_a_new_workbook_in_reused_directory(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            product_ids = ("92237157",)
            old = self.make_semantic_workbook(root, product_ids, age_seconds=60)
            self.write_receipt(old)
            stamp = datetime.now().strftime("%y%m%d-%H%M")
            fresh = old.parent / f"trip_scoreandprice_results_{stamp}.xlsx"
            fresh.write_bytes(b"new native workbook")
            turn = self.plugin.ManualTurn(product_ids, fresh.parent, time.time() - 10)

            self.assertEqual(self.plugin.filter_manual_response(str(fresh), turn), str(fresh))

    def test_malformed_receipt_is_anomaly_not_stale_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workbook = self.make_semantic_workbook(root, ("92237157",))
            (workbook.parent / ".delivered.json").write_text("[]", encoding="utf-8")
            turn = self.plugin.ManualTurn(("92237157",), workbook.parent, time.time() - 10)
            self.assertEqual(
                self.plugin.filter_manual_response(str(workbook), turn),
                "抓取结果异常：原生Excel交付回执或识别异常。",
            )

    def test_receipt_target_mismatch_does_not_suppress_when_hook_target_is_known(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workbook = self.make_semantic_workbook(root, ("92237157",))
            self.write_receipt(workbook, target="telegram:wrong")
            turn = self.plugin.ManualTurn(("92237157",), workbook.parent, time.time() - 10)
            self.assertEqual(
                self.plugin.filter_manual_response(
                    str(workbook), turn, expected_target="telegram:offline_test_chat"
                ),
                "抓取结果异常：原生Excel交付回执或识别异常。",
            )

    def test_timestamped_rerun_requires_its_response_workbook_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = root / "manual-92237157"
            rerun = root / "manual-92237157-rerun-260930-1522"
            workbook = self.make_semantic_workbook(root, ("92237157",), directory=rerun)
            turn = self.plugin.ManualTurn(("92237157",), base, time.time() - 10)
            self.assertTrue(self.plugin.filter_manual_response("done", turn).startswith("抓取失败"))
            self.assertEqual(self.plugin.filter_manual_response(f"MEDIA:{workbook}", turn), str(workbook))

    def test_timestamped_rerun_delivered_receipt_suppresses_duplicate_delivery(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            base = root / "manual-92237157"
            rerun = root / "manual-92237157-rerun-260930-1522"
            workbook = self.make_semantic_workbook(root, ("92237157",), directory=rerun)
            self.write_receipt(workbook)
            turn = self.plugin.ManualTurn(("92237157",), base, time.time() - 10)

            self.assertEqual(
                self.plugin.filter_manual_response(f"MEDIA:{workbook}", turn), "附件已发送。"
            )

    def test_wrong_batch_workbook_is_not_recognized_as_current_batch(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            directory = root / "manual-92237157"
            wrong = self.make_semantic_workbook(
                root, ("109223304",), directory=directory
            )
            turn = self.plugin.ManualTurn(("92237157",), directory, time.time() - 10)
            self.assertEqual(
                self.plugin.filter_manual_response(str(wrong), turn),
                "抓取结果异常：原生Excel交付回执或识别异常。",
            )

    def test_large_batch_requires_exact_documented_count_label(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            product_ids = tuple(str(10000000 + number) for number in range(6))
            workbook = self.make_semantic_workbook(root, product_ids, batch="5产品批次")
            turn = self.plugin.ManualTurn(product_ids, workbook.parent, time.time() - 10)
            self.assertEqual(
                self.plugin.filter_manual_response(str(workbook), turn),
                "抓取结果异常：原生Excel交付回执或识别异常。",
            )

    def test_fresh_unsupported_xlsx_is_recognition_anomaly(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            directory = root / "manual-92237157"
            directory.mkdir()
            workbook = directory / "unexpected.xlsx"
            workbook.write_bytes(b"fresh unsupported workbook")
            turn = self.plugin.ManualTurn(("92237157",), directory, time.time() - 10)
            self.assertEqual(
                self.plugin.filter_manual_response(str(workbook), turn),
                "抓取结果异常：原生Excel交付回执或识别异常。",
            )

    def test_empty_workbook_is_not_discovered(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            directory = root / "manual-92237157"
            directory.mkdir()
            stamp = datetime.now().strftime("%y%m%d-%H%M")
            (directory / f"trip_scoreandprice_results_{stamp}.xlsx").touch()
            turn = self.plugin.ManualTurn(("92237157",), directory, time.time() - 10)
            self.assertTrue(self.plugin.filter_manual_response("done", turn).startswith("抓取失败"))

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

    def test_old_timestamp_workbook_is_recognition_anomaly_after_being_touched(self):
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

            self.assertEqual(result, "抓取结果异常：原生Excel交付回执或识别异常。")

    def test_invalid_timestamp_workbook_is_recognition_anomaly(self):
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

            self.assertEqual(result, "抓取结果异常：原生Excel交付回执或识别异常。")

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

    def test_short_price_request_with_id_is_a_manual_scrape_trigger(self):
        with tempfile.TemporaryDirectory() as tmp:
            turn = self.plugin.detect_manual_request(
                "抓價格 ID: 109223304（新加坡站連結）",
                [],
                output_root=Path(tmp),
                started_at=100.0,
            )

            self.assertEqual(turn.product_ids, ("109223304",))

    def test_quoted_reply_and_injected_context_are_not_a_new_scrape_request(self):
        quoted = (
            '[Replying to: "请抓取产品价格 https://tw.trip.com/things-to-do/detail/116063167?locale=zh-TW"]'
            "\n\n先下一单，用保险箱里的信用卡支付\n\n"
            "本轮是单个或数个指定Trip产品的价格抓取。请抓取价格 116063167"
        )
        with tempfile.TemporaryDirectory() as tmp:
            self.assertIsNone(
                self.plugin.detect_manual_request(quoted, [], output_root=Path(tmp))
            )

    def test_own_text_after_reply_quote_can_still_trigger(self):
        text = '[Replying to: "好的"]\n\n请抓取产品价格116063167'
        with tempfile.TemporaryDirectory() as tmp:
            turn = self.plugin.detect_manual_request(text, [], output_root=Path(tmp))
        self.assertEqual(turn.product_ids, ("116063167",))

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

    def test_untracked_manual_retry_media_is_suppressed_by_delivery_receipt(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            context = FakePluginContext()
            self.plugin.register(context, output_root=output_root)
            workbook = self.make_workbook(output_root, ("71827107",))
            (workbook.parent / ".delivered.json").write_text(
                '{"status":"delivered","path":"' + str(workbook) + '"}',
                encoding="utf-8",
            )

            result = context.hooks["transform_llm_output"](
                response_text=f"MEDIA:{workbook}",
                session_id="continuation-without-product-ids",
            )

            self.assertEqual(result, "附件已发送。")

    def test_tracked_delivery_receipt_never_returns_empty_response(self):
        with tempfile.TemporaryDirectory() as tmp:
            output_root = Path(tmp)
            workbook = self.make_workbook(output_root, ("71827107",))
            (workbook.parent / ".delivered.json").write_text(
                '{"status":"delivered","path":"' + str(workbook) + '"}',
                encoding="utf-8",
            )
            turn = self.plugin.ManualTurn(("71827107",), workbook.parent, 0.0)

            self.assertEqual(
                self.plugin.filter_manual_response(f"MEDIA:{workbook}", turn),
                "附件已发送。",
            )


if __name__ == "__main__":
    unittest.main()
