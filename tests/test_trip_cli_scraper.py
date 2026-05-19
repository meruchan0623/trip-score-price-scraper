import unittest

from models import ScrapeItem, TaskRequest
from trip_cli_scraper import TripCliScraper


class FakeTripCliScraper(TripCliScraper):
    def __init__(self, request):
        super().__init__(request, lambda _event: None, vendor="测试厂商", worker_id=1, global_total=1)

    def _extract_base_row(self, item):
        return {
            "厂商": item.vendor,
            "商品ID": "12345",
            "URL": item.url,
            "商品名称": "测试商品",
            "预定数": "10",
            "评价数": "5",
            "评分": "4.8",
            "客户服务": "4.7",
            "方便程度": "4.6",
            "讯号状态": "4.5",
            "中差评价数": "1",
            "商品资讯": "info",
        }

    def _extract_all_sku_prices(self, stop_event=None):
        self.max_sku_layers = 2
        return [
            {
                "combination": "1日 | 4G",
                "currency": "HKD",
                "price": "10",
                "real_price": "8",
                "promo_tag": "Save 20%",
            },
            {
                "combination": "2日 | 5G",
                "currency": "HKD",
                "price": "20",
                "real_price": "18",
                "promo_tag": "",
            },
        ]


class TripCliScraperSkuRowsTest(unittest.TestCase):
    def test_extract_rows_returns_one_row_per_sku_combination(self):
        request = TaskRequest(extract_real_price=True, extract_product_info=True)
        scraper = FakeTripCliScraper(request)

        rows = scraper._extract_rows(ScrapeItem("https://example.test/detail/12345", vendor="供应商A"))

        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[0]["商品ID"], "12345")
        self.assertEqual(rows[0]["SKU层级1"], "1日")
        self.assertEqual(rows[0]["SKU层级2"], "4G")
        self.assertEqual(rows[0]["外显价格"], "10")
        self.assertEqual(rows[0]["实价"], "8")
        self.assertEqual(rows[0]["促销标签"], "Save 20%")
        self.assertEqual(rows[1]["商品ID"], "")
        self.assertEqual(rows[1]["URL"], "")
        self.assertEqual(rows[1]["商品资讯"], "")
        self.assertEqual(rows[1]["SKU层级1"], "2日")
        self.assertEqual(rows[1]["SKU层级2"], "5G")
        self.assertEqual(rows[1]["实价"], "18")

    def test_no_real_price_keeps_sku_rows_without_real_price_field(self):
        request = TaskRequest(extract_real_price=False, extract_product_info=True)
        scraper = FakeTripCliScraper(request)

        rows = scraper._extract_rows(ScrapeItem("https://example.test/detail/12345"))

        self.assertEqual(len(rows), 2)
        self.assertNotIn("实价", rows[0])
        self.assertNotIn("实价", rows[1])
        self.assertEqual(rows[0]["SKU层级2"], "4G")

    def test_split_price_supports_hk_dollar_text(self):
        request = TaskRequest()
        scraper = FakeTripCliScraper(request)

        self.assertEqual(scraper._split_price("HK$1,234.50"), ("HKD", "1234.50"))

    def test_price_bundle_js_reads_display_real_and_promo(self):
        class FakeDriver:
            def __init__(self):
                self.include_real = None

            def execute_script(self, _script, _display_selectors, _real_selector, _promo_selector, include_real):
                self.include_real = include_real
                return {
                    "display_price": "HK$100",
                    "real_price": "HK$88",
                    "promo_tag": "減 HK$12",
                }

        request = TaskRequest(extract_real_price=True)
        scraper = FakeTripCliScraper(request)
        scraper.driver = FakeDriver()

        bundle = scraper._get_current_price_bundle()

        self.assertTrue(scraper.driver.include_real)
        self.assertEqual(bundle["display_price"], "HK$100")
        self.assertEqual(bundle["real_price"], "HK$88")
        self.assertEqual(bundle["promo_tag"], "減 HK$12")

    def test_price_bundle_js_skips_real_price_when_disabled(self):
        class FakeDriver:
            def __init__(self):
                self.include_real = None

            def execute_script(self, _script, _display_selectors, _real_selector, _promo_selector, include_real):
                self.include_real = include_real
                return {"display_price": "HK$100", "real_price": "", "promo_tag": ""}

        request = TaskRequest(extract_real_price=False)
        scraper = FakeTripCliScraper(request)
        scraper.driver = FakeDriver()

        bundle = scraper._get_current_price_bundle()

        self.assertFalse(scraper.driver.include_real)
        self.assertEqual(bundle["display_price"], "HK$100")
        self.assertEqual(bundle["real_price"], "")


if __name__ == "__main__":
    unittest.main()
