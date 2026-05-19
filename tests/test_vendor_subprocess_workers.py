import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from models import BrowserOptions, ScrapeItem, TaskRequest
import service
from service import _build_worker_command, _scrape_item_from_dict, _scrape_item_to_dict


class VendorSubprocessWorkersTest(unittest.TestCase):
    def test_scrape_item_payload_round_trips_vendor_metadata(self):
        item = ScrapeItem(
            url="https://www.trip.com/example",
            vendor="Vendor A",
            source_column=5,
            source_row=9,
            input_order=3,
        )

        restored = _scrape_item_from_dict(_scrape_item_to_dict(item))

        self.assertEqual(restored.url, item.url)
        self.assertEqual(restored.vendor, item.vendor)
        self.assertEqual(restored.source_column, item.source_column)
        self.assertEqual(restored.source_row, item.source_row)
        self.assertEqual(restored.input_order, item.input_order)

    def test_worker_command_uses_hidden_mode_and_isolates_worker_files(self):
        request = TaskRequest(
            rating_only=False,
            extract_real_price=True,
            extract_product_info=False,
            browser_options=BrowserOptions(
                headless=False,
                session_file="/tmp/session.json",
                chrome_path="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
                chrome_version_main=123,
                driver_executable_path="/tmp/chromedriver",
                memory_guard_enabled=False,
                memory_limit_mb=4096,
                restart_every=7,
            ),
        )

        command = _build_worker_command(
            request=request,
            vendor="Vendor A",
            items_file="/tmp/items.json",
            output_path="/tmp/output.xlsx",
            result_file="/tmp/result.json",
            session_file="/tmp/session_worker.json",
        )

        self.assertIn("--worker-items-file", command)
        self.assertIn("/tmp/items.json", command)
        self.assertIn("--worker-output", command)
        self.assertIn("/tmp/output.xlsx", command)
        self.assertIn("--worker-result-file", command)
        self.assertIn("/tmp/result.json", command)
        self.assertNotIn("--worker-log-file", command)
        self.assertIn("--session-file", command)
        self.assertIn("/tmp/session_worker.json", command)
        self.assertIn("--no-headless", command)
        self.assertIn("--real-price", command)
        self.assertIn("--no-product-info", command)
        self.assertIn("--disable-browser-memory-guard", command)
        self.assertIn("--worker-driver-executable-path", command)
        self.assertIn("/tmp/chromedriver", command)

    def test_worker_result_json_is_lightweight_without_rows_data(self):
        class FakeScraper:
            def __init__(self, request, reporter, vendor, worker_id, global_total):
                self.request = request

            def prepare_environment(self):
                return None

            def start_browser(self):
                return None

            def scrape_items(self, items, rows=None):
                return [{"厂商": items[0].vendor, "URL": items[0].url, "外显价格": "10"}]

            def close(self):
                return None

        with TemporaryDirectory() as tmp:
            items_file = Path(tmp) / "items.json"
            output_path = Path(tmp) / "out.xlsx"
            result_file = Path(tmp) / "result.json"
            items_file.write_text(
                '[{"url":"https://www.trip.com/example","vendor":"Vendor A","input_order":0}]',
                encoding="utf-8",
            )
            request = TaskRequest(
                extract_real_price=False,
                browser_options=BrowserOptions(session_file=str(Path(tmp) / "session.json")),
            )

            with (
                patch.object(service, "TripCliScraper", FakeScraper),
                patch.object(service, "export_rows", return_value=str(output_path)),
            ):
                exit_code = service.run_vendor_worker_from_files(
                    items_file=str(items_file),
                    output_path=str(output_path),
                    result_file=str(result_file),
                    vendor="Vendor A",
                    request=request,
                )

            result = service._read_json(result_file)
            self.assertEqual(exit_code, 0)
            self.assertEqual(result["rows"], 1)
            self.assertNotIn("rows_data", result)


if __name__ == "__main__":
    unittest.main()
