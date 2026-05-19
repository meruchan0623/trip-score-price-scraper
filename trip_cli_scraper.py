from __future__ import annotations

import os
import platform
import re
import shutil
import subprocess
import tempfile
import time
import traceback
import uuid
from pathlib import Path
from typing import Any, Callable

import undetected_chromedriver as uc
from selenium.common.exceptions import TimeoutException, WebDriverException
from selenium.webdriver.common.by import By
from selenium.webdriver.support import expected_conditions as EC
from selenium.webdriver.support.ui import WebDriverWait

from browser_startup import chrome_major_version, detect_chrome_path
from models import ScrapeItem, TaskRequest
from session_store import SessionStore

EventSink = Callable[[dict[str, Any]], None]


class StopRequested(Exception):
    pass


class TripCliScraper:
    def __init__(
        self,
        request: TaskRequest,
        emit: EventSink,
        *,
        vendor: str,
        worker_id: int,
        global_total: int,
    ) -> None:
        self.request = request
        self.emit = emit
        self.vendor = vendor
        self.worker_id = worker_id
        self.global_total = global_total
        self.driver = None
        self.user_data_dir: str | None = None
        self.session = SessionStore(request.browser_options.session_file)
        self.max_sku_layers = 0

    def prepare_environment(self) -> None:
        self.emit({"event": "worker_ready", "message": "worker ready"})
        self.emit({"event": "chrome_detect_started", "stage": "chrome_detect", "message": "detecting Chrome"})
        chrome_path = self.request.browser_options.chrome_path or self._chrome_path()
        if not chrome_path:
            self.emit(
                {
                    "event": "chrome_detect_failed",
                    "stage": "chrome_detect",
                    "error_type": "ChromeNotFound",
                    "error": "Google Chrome not found",
                }
            )
            raise RuntimeError("Google Chrome not found")
        if os.path.sep in chrome_path and not os.access(chrome_path, os.X_OK):
            self.emit(
                {
                    "event": "chrome_detect_failed",
                    "stage": "chrome_detect",
                    "error_type": "ChromeNotExecutable",
                    "error": f"Chrome path is not executable: {chrome_path}",
                }
            )
            raise RuntimeError(f"Chrome path is not executable: {chrome_path}")
        version = self.request.browser_options.chrome_version_main or self._chrome_major_version()
        message = f"chrome={chrome_path} version_main={version if version else 'unknown'}"
        self.emit({"event": "chrome_detect_done", "stage": "chrome_detect", "message": message, "chrome_path": chrome_path, "version_main": version})
        if not version:
            self.emit({"event": "warning", "stage": "chrome_detect", "message": "Chrome version detection failed; continuing with auto version"})
        self.emit({"event": "browser_prepare", "stage": "chrome_detect", "message": f"chrome={chrome_path}"})

    def start_browser(self) -> None:
        self.emit({"event": "browser_start_started", "message": "starting Chrome"})
        started = time.monotonic()
        self.user_data_dir = os.path.join(tempfile.gettempdir(), f"trip_cli_chrome_{uuid.uuid4().hex[:12]}")
        os.makedirs(self.user_data_dir, exist_ok=True)

        options = uc.ChromeOptions()
        chrome_path = self.request.browser_options.chrome_path or self._chrome_path()
        if chrome_path:
            options.binary_location = chrome_path
        options.add_argument(f"--user-data-dir={self.user_data_dir}")
        options.add_argument("--no-first-run")
        options.add_argument("--no-default-browser-check")
        options.add_argument("--disable-background-networking")
        options.add_argument("--disable-default-apps")
        options.add_argument("--disable-sync")
        options.add_argument("--disable-translate")
        options.add_argument("--disable-extensions")
        options.add_argument("--disable-dev-shm-usage")
        options.add_argument("--disable-gpu")
        options.add_argument("--disable-component-update")
        options.add_argument("--disable-crash-reporter")
        options.add_argument("--disable-domain-reliability")
        options.add_argument("--disable-features=MediaRouter,OptimizationHints,Translate,AutofillServerCommunication")
        options.add_argument("--disable-hang-monitor")
        options.add_argument("--disable-ipc-flooding-protection")
        options.add_argument("--disable-notifications")
        options.add_argument("--disable-popup-blocking")
        options.add_argument("--disable-renderer-backgrounding")
        options.add_argument("--metrics-recording-only")
        options.add_argument("--no-report-upload")
        options.add_argument("--disable-blink-features=AutomationControlled")
        options.add_argument("--window-size=1920,1080")
        if self.request.browser_options.headless:
            options.add_argument("--headless=new")

        try:
            version_main = self.request.browser_options.chrome_version_main or self._chrome_major_version()
            driver_path = self.request.browser_options.driver_executable_path
            self.driver = uc.Chrome(
                options=options,
                version_main=version_main,
                driver_executable_path=driver_path,
                browser_executable_path=chrome_path,
                user_multi_procs=bool(driver_path),
                use_subprocess=True,
            )
            self.driver.set_page_load_timeout(30)
            self.driver.implicitly_wait(3)
            try:
                loaded = self.session.load(self.driver)
                self.emit(
                    {
                        "event": "log",
                        "stage": "session_load",
                        "message": "session loaded" if loaded else "session not loaded or unavailable",
                    }
                )
            except Exception as exc:
                self.emit(
                    {
                        "event": "log",
                        "stage": "session_load",
                        "message": f"session load skipped: {type(exc).__name__}: {exc}",
                        "error_type": type(exc).__name__,
                        "error": str(exc),
                    }
                )
            finally:
                try:
                    self.driver.set_page_load_timeout(30)
                except Exception:
                    pass
            self.emit(
                {
                    "event": "browser_start_done",
                    "message": "Chrome started",
                    "elapsed_ms": int((time.monotonic() - started) * 1000),
                }
            )
        except Exception as exc:
            self.emit(
                {
                    "event": "browser_start_failed",
                    "message": "Chrome start failed",
                    "stage": "browser_start",
                    "error_type": type(exc).__name__,
                    "error": str(exc),
                    "traceback": traceback.format_exc(),
                    "elapsed_ms": int((time.monotonic() - started) * 1000),
                }
            )
            raise

    def scrape_items(self, items: list[ScrapeItem], stop_event=None, rows: list[dict] | None = None) -> list[dict]:
        if not self.driver:
            raise RuntimeError("browser is not started")
        rows = rows if rows is not None else []
        for index, item in enumerate(items, 1):
            if self._stop_requested(stop_event):
                self._stop_browser_load()
                break
            try:
                rows.extend(self.scrape_one(item, index, len(items), stop_event=stop_event))
            except StopRequested:
                self._stop_browser_load()
                break
        try:
            self.session.save(self.driver)
        except Exception as exc:
            self.emit({"event": "warning", "message": f"session save failed: {exc}"})
        return rows

    def scrape_one(self, item: ScrapeItem, index: int, total: int, stop_event=None) -> list[dict]:
        started = time.monotonic()
        base = {
            "vendor": self.vendor,
            "worker_id": self.worker_id,
            "url_index": index,
            "url_total": total,
            "url": item.url,
            "global_total": self.global_total,
        }
        self._raise_if_stopped(stop_event)
        self.emit({"event": "url_started", **base})
        try:
            self.emit({"event": "log", "stage": "page_open", "message": "opening URL", **base})
            self._raise_if_stopped(stop_event)
            self.driver.get(item.url)
            self._wait_ready()
            self._raise_if_stopped(stop_event)
            self.emit({"event": "log", "stage": "lazy_load", "message": "scrolling page", **base})
            self._scroll_page(stop_event=stop_event)
            self._raise_if_stopped(stop_event)
            self.emit({"event": "log", "stage": "extract", "message": "extracting page", **base})
            rows = self._extract_rows(item, stop_event=stop_event)
            self._raise_if_stopped(stop_event)
            elapsed_ms = int((time.monotonic() - started) * 1000)
            self.emit({"event": "url_done", "rows": len(rows), "elapsed_ms": elapsed_ms, **base})
            return rows
        except StopRequested:
            self._stop_browser_load()
            raise
        except Exception as exc:
            elapsed_ms = int((time.monotonic() - started) * 1000)
            self.emit({"event": "url_failed", "error": str(exc), "elapsed_ms": elapsed_ms, **base})
            return [self._error_row(item, str(exc))]

    def close(self) -> None:
        if self.driver:
            try:
                self.driver.quit()
            except Exception:
                pass
            self.driver = None
        if self.user_data_dir:
            shutil.rmtree(self.user_data_dir, ignore_errors=True)
            self.user_data_dir = None

    def _extract_rows(self, item: ScrapeItem, stop_event=None) -> list[dict]:
        base_row = self._extract_base_row(item)
        if self.request.rating_only:
            self.emit({"event": "log", "stage": "sku", "message": "rating-only mode; skipping SKU price extraction"})
            return [base_row]

        old_max_layers = self.max_sku_layers
        sku_price_combinations = self._extract_all_sku_prices(stop_event=stop_event)
        if self.max_sku_layers > old_max_layers:
            self.emit(
                {
                    "event": "log",
                    "stage": "sku",
                    "message": f"SKU layers increased to {self.max_sku_layers}",
                }
            )

        if not sku_price_combinations:
            default_price_text = self._get_default_price()
            currency, price = self._split_price(default_price_text)
            row = dict(base_row)
            row.update(
                {
                    "货币": currency,
                    "外显价格": price,
                    "促销标签": self._promo_text(),
                }
            )
            if self.request.extract_real_price:
                row["实价"] = price
            for index in range(max(self.max_sku_layers, 1)):
                row[f"SKU层级{index + 1}"] = "无SKU选项" if index == 0 else ""
            self._ensure_price_fields(row)
            return [row]

        rows: list[dict] = []
        for index, sku_combo in enumerate(sku_price_combinations):
            self._raise_if_stopped(stop_event)
            row = self._merge_sku_row(base_row, sku_combo, index)
            rows.append(row)
        return rows

    def _extract_base_row(self, item: ScrapeItem) -> dict:
        product_id = self._product_id(item.url)
        title = self._first_text(
            [
                "h1",
                '[data-testid="product-title"]',
                '[class*="title"]',
            ]
        )
        row = {
            "厂商": item.vendor,
            "商品ID": product_id,
            "URL": item.url,
            "商品名称": title or self._title_from_document(),
            "预定数": self._booking_count(),
            "评价数": self._review_count(),
            "评分": self._first_text(['span[testid="productdetail-productInfo-commentScore"]', '[class*="commentScore"]']),
            "客户服务": self._score_near("Customer Service") or self._score_near("客户服务"),
            "方便程度": self._score_near("Convenience") or self._score_near("方便"),
            "讯号状态": self._score_near("Signal") or self._score_near("讯号"),
            "中差评价数": self._bad_review_count(),
        }
        if self.request.extract_product_info:
            row["商品资讯"] = self._product_info()
        return row

    def _merge_sku_row(self, base_row: dict, sku_combo: dict, index: int) -> dict:
        row = dict(base_row)
        if index > 0:
            for key in (
                "厂商",
                "商品ID",
                "URL",
                "商品名称",
                "评分",
                "评价数",
                "预定数",
                "客户服务",
                "方便程度",
                "讯号状态",
                "中差评价数",
                "货币",
            ):
                row[key] = ""
            if self.request.extract_product_info:
                row["商品资讯"] = ""

        row["货币"] = sku_combo.get("currency", "")
        row["外显价格"] = sku_combo.get("price", "")
        if self.request.extract_real_price:
            row["实价"] = sku_combo.get("real_price", "")
        row["促销标签"] = sku_combo.get("promo_tag", "")

        sku_parts = str(sku_combo.get("combination", "")).split(" | ") if sku_combo.get("combination") else []
        for layer_index in range(max(self.max_sku_layers, len(sku_parts), 1)):
            row[f"SKU层级{layer_index + 1}"] = sku_parts[layer_index] if layer_index < len(sku_parts) else ""
        self._ensure_price_fields(row)
        return row

    def _error_row(self, item: ScrapeItem, error: str) -> dict:
        row = {
            "厂商": item.vendor,
            "商品ID": self._product_id(item.url),
            "URL": item.url,
            "商品名称": "",
            "评分": f"处理错误: {error}",
            "评价数": "",
            "预定数": "",
            "客户服务": "",
            "方便程度": "",
            "讯号状态": "",
            "中差评价数": "",
        }
        if self.request.extract_product_info:
            row["商品资讯"] = ""
        if not self.request.rating_only:
            row.update({"SKU层级1": "", "货币": "", "外显价格": "", "促销标签": ""})
            if self.request.extract_real_price:
                row["实价"] = ""
        self._ensure_price_fields(row)
        return row

    def _ensure_price_fields(self, row: dict) -> None:
        if self.request.rating_only:
            return
        row.setdefault("SKU层级1", "")
        row.setdefault("货币", "")
        row.setdefault("外显价格", "")
        row.setdefault("促销标签", "")
        if self.request.extract_real_price:
            row.setdefault("实价", row.get("外显价格", ""))

    def _extract_all_sku_prices(self, stop_event=None) -> list[dict]:
        self.emit({"event": "log", "stage": "sku", "message": "starting SKU option and price extraction"})
        try:
            sku_container_selectors = [
                'div[testid="act_sku_wrapper"]',
                "div.style_sku_wrapper_container__Lq9RG",
                "div.style_m_container__ssZE4",
                'div[class*="style_sku_wrapper_container"]',
            ]
            sku_container = None
            for selector in sku_container_selectors:
                self._raise_if_stopped(stop_event)
                try:
                    sku_container = WebDriverWait(self.driver, 3).until(
                        EC.presence_of_element_located((By.CSS_SELECTOR, selector))
                    )
                    self.emit({"event": "log", "stage": "sku", "message": f"found SKU container: {selector}"})
                    break
                except TimeoutException:
                    continue

            if not sku_container:
                self.emit({"event": "log", "stage": "sku", "message": "SKU container not found; product may have no SKU options"})
                return []

            sku_layers = self._get_sku_layers_real_structure(sku_container)
            if not sku_layers:
                self.emit({"event": "log", "stage": "sku", "message": "SKU layers not found"})
                return []

            self.emit({"event": "log", "stage": "sku", "message": f"found {len(sku_layers)} SKU layers"})
            layer_info = []
            for index, layer in enumerate(sku_layers):
                layer_title = self._get_layer_title(layer)
                layer_info.append({"layer": layer, "title": layer_title, "index": index})
                self.emit({"event": "log", "stage": "sku", "message": f"layer {index + 1}: {layer_title}"})

            self.max_sku_layers = max(self.max_sku_layers, len(layer_info))
            combinations_result: list[dict] = []
            self._recursive_traverse_sku_layers(layer_info, 0, [], combinations_result, stop_event=stop_event)
            self.emit(
                {
                    "event": "log",
                    "stage": "sku",
                    "message": f"recursive SKU traversal complete; captured {len(combinations_result)} combinations",
                }
            )
            return combinations_result
        except StopRequested:
            raise
        except Exception as exc:
            self.emit({"event": "warning", "stage": "sku", "message": f"SKU extraction failed: {exc}"})
            return []

    def _get_sku_layers_real_structure(self, container) -> list[Any]:
        layer_selectors = [
            "div.sku_tab_ceil.style_m_sku_tab_ceil__D9OYQ",
            "div.xtaro-xview.xt-xview.sku_tab_ceil",
            'div[class*="sku_tab_ceil"]',
        ]
        for selector in layer_selectors:
            try:
                elements = container.find_elements(By.CSS_SELECTOR, selector)
                if elements and len(elements) <= 5:
                    layers = elements[:5]
                    self.emit({"event": "log", "stage": "sku", "message": f"selector {selector} found {len(layers)} layers"})
                    return layers
            except Exception:
                continue
        return []

    def _get_layer_title(self, layer) -> str:
        try:
            title_element = layer.find_element(By.CSS_SELECTOR, "span.style_m_tab_title_text__ldPLP")
            return title_element.text.strip()
        except Exception:
            return "未知层级"

    def _get_layer_options_real_structure(self, layer, layer_num: int) -> list[dict]:
        options = []
        try:
            options_container = layer.find_element(By.CSS_SELECTOR, "div.style_m_tab__SI4eq")
            option_elements = options_container.find_elements(By.CSS_SELECTOR, "div.m_ceil.style_m_ceil__mUygk")
            for element in option_elements:
                if not self._is_option_available_real_structure(element):
                    continue
                try:
                    text_element = element.find_element(By.CSS_SELECTOR, 'span[testid^="title_saleproperty_"]')
                    option_text = text_element.text.strip()
                    if option_text:
                        options.append(
                            {
                                "element": element,
                                "text": option_text,
                                "layer": layer_num,
                                "id": element.get_attribute("id"),
                            }
                        )
                except Exception as exc:
                    self.emit({"event": "log", "stage": "sku", "message": f"failed to read option text: {exc}"})
        except Exception as exc:
            self.emit({"event": "log", "stage": "sku", "message": f"failed to read layer {layer_num} options: {exc}"})
        return options

    def _is_option_available_real_structure(self, element) -> bool:
        try:
            class_name = element.get_attribute("class") or ""
            if "style_select_border__AaoE0" not in class_name:
                return False
            try:
                text_element = element.find_element(By.CSS_SELECTOR, 'span[testid^="title_saleproperty_"]')
                text_color = text_element.value_of_css_property("color")
                if "rgb(172, 180, 191)" in text_color or "rgb(206, 210, 217)" in text_color:
                    return False
            except Exception:
                pass
            element_color = element.value_of_css_property("color")
            if "rgb(206, 210, 217)" in element_color:
                return False
            return bool(element.is_displayed() and element.is_enabled())
        except Exception as exc:
            self.emit({"event": "log", "stage": "sku", "message": f"failed to check option availability: {exc}"})
            return False

    def _recursive_traverse_sku_layers(
        self,
        layer_info: list[dict],
        current_layer_index: int,
        selected_options: list[dict],
        combinations_result: list[dict],
        *,
        stop_event=None,
    ) -> None:
        self._raise_if_stopped(stop_event)
        if current_layer_index >= len(layer_info):
            combination_result = self._get_current_combination_price_fast(selected_options, stop_event=stop_event)
            if combination_result:
                combinations_result.append(combination_result)
            return

        try:
            current_layer_data = layer_info[current_layer_index]
            current_layer_element = current_layer_data["layer"]
            current_title = current_layer_data["title"]
            is_last_layer = current_layer_index == len(layer_info) - 1
            available_options = self._get_layer_options_real_structure(current_layer_element, current_layer_index + 1)
            if not available_options:
                self.emit(
                    {
                        "event": "log",
                        "stage": "sku",
                        "message": f"layer {current_layer_index + 1} {current_title} has no available options; skipping",
                    }
                )
                return

            self.emit(
                {
                    "event": "log",
                    "stage": "sku",
                    "message": f"traversing layer {current_layer_index + 1} {current_title}: {len(available_options)} options",
                }
            )
            for option in available_options:
                self._raise_if_stopped(stop_event)
                self._click_sku_option(option, current_layer_index + 1, stop_event=stop_event)
                selected_options.append(option)
                try:
                    self._raise_if_stopped(stop_event)
                    if is_last_layer:
                        combination_result = self._get_current_combination_price_fast(selected_options, stop_event=stop_event)
                        if combination_result:
                            combinations_result.append(combination_result)
                    else:
                        self._recursive_traverse_sku_layers(
                            layer_info,
                            current_layer_index + 1,
                            selected_options,
                            combinations_result,
                            stop_event=stop_event,
                        )
                finally:
                    selected_options.pop()
                    self._click_sku_option(option, current_layer_index + 1, is_deselect=True, stop_event=stop_event)
        except StopRequested:
            raise
        except Exception as exc:
            self.emit(
                {
                    "event": "warning",
                    "stage": "sku",
                    "message": f"recursive traversal failed at layer {current_layer_index + 1}: {exc}",
                }
            )

    def _click_sku_option(self, option: dict, layer_num: int, is_deselect: bool = False, max_retries: int = 3, stop_event=None) -> None:
        element = option["element"]
        action_type = "取消选择" if is_deselect else "选择"
        for retry in range(max_retries):
            self._raise_if_stopped(stop_event)
            try:
                if not self._is_element_valid(element):
                    self.emit({"event": "log", "stage": "sku", "message": "option element is stale; refreshing"})
                    element = self._refresh_option_element(option, layer_num)
                    if not element:
                        raise RuntimeError("unable to refresh option element")
                    option["element"] = element

                try:
                    border_color = element.value_of_css_property("border-color")
                    is_selected = "rgb(44, 97, 254)" in border_color
                    if not is_deselect and is_selected:
                        return
                    if is_deselect and not is_selected:
                        return
                except Exception:
                    pass

                self.driver.execute_script("arguments[0].scrollIntoView({block: 'center'});", element)
                time.sleep(0.05)
                self.driver.execute_script("arguments[0].click();", element)
                time.sleep(0.1)
                self.emit({"event": "log", "stage": "sku", "message": f"layer {layer_num} {action_type}: {option['text']}"})
                return
            except StopRequested:
                raise
            except Exception as exc:
                if retry < max_retries - 1:
                    self.emit(
                        {
                            "event": "log",
                            "stage": "sku",
                            "message": f"click layer {layer_num} option {option['text']} failed; retry {retry + 1}/{max_retries}: {exc}",
                        }
                    )
                    time.sleep(0.2)
                    continue
                self.emit(
                    {
                        "event": "warning",
                        "stage": "sku",
                        "message": f"click layer {layer_num} option {option['text']} failed: {exc}",
                    }
                )
                raise

    def _is_element_valid(self, element) -> bool:
        try:
            element.tag_name
            return bool(element.is_displayed() and element.is_enabled())
        except Exception:
            return False

    def _refresh_option_element(self, option: dict, layer_num: int):
        try:
            option_id = option.get("id")
            if option_id:
                new_element = self.driver.find_element(By.ID, option_id)
                if new_element.is_displayed():
                    option["element"] = new_element
                    return new_element
        except Exception:
            pass

        try:
            option_text = option["text"]
            elements = self.driver.find_elements(By.XPATH, f"//span[contains(text(), '{option_text}')]")
            for element in elements:
                parent = element.find_element(By.XPATH, "./ancestor::div[contains(@class, 'm_ceil')]")
                if parent.is_displayed() and self._is_option_available_real_structure(parent):
                    option["element"] = parent
                    return parent
        except Exception:
            pass
        return None

    def _get_current_combination_price_fast(self, selected_options: list[dict], max_retries: int = 3, stop_event=None) -> dict | None:
        if not selected_options:
            return None

        combo_texts = [option["text"] for option in selected_options]
        combination_text = " | ".join(combo_texts)
        for retry in range(max_retries):
            self._raise_if_stopped(stop_event)
            try:
                time.sleep(0.15)
                display_price_text = self._get_current_price_robust(stop_event=stop_event)
                display_currency, display_price = self._split_price(display_price_text)

                real_currency, real_price = "N/A", "N/A"
                if self.request.extract_real_price:
                    real_currency, real_price = self._extract_real_price()

                promo_tag = self._extract_sku_promotional_tag()
                if display_price and display_price != "价格未找到":
                    self.emit(
                        {
                            "event": "log",
                            "stage": "sku",
                            "message": (
                                f"combination price: {combination_text} = "
                                f"display:{display_currency}{display_price}, "
                                f"real:{real_currency}{real_price}, promo:{promo_tag}"
                            ),
                        }
                    )
                    return {
                        "combination": combination_text,
                        "currency": display_currency,
                        "price": display_price,
                        "real_currency": real_currency,
                        "real_price": real_price,
                        "promo_tag": promo_tag,
                    }
                if retry < max_retries - 1:
                    self.emit({"event": "log", "stage": "sku", "message": f"price capture failed; retry {retry + 1}/{max_retries}"})
                    time.sleep(0.2)
            except StopRequested:
                raise
            except Exception as exc:
                if retry < max_retries - 1:
                    self.emit({"event": "log", "stage": "sku", "message": f"price capture error; retry {retry + 1}/{max_retries}: {exc}"})
                    time.sleep(0.2)
                else:
                    self.emit({"event": "warning", "stage": "sku", "message": f"price capture failed: {exc}"})

        return {
            "combination": combination_text,
            "currency": "N/A",
            "price": "价格获取失败",
            "real_currency": "N/A",
            "real_price": "N/A",
            "promo_tag": "",
        }

    def _get_current_price_robust(self, max_attempts: int = 3, stop_event=None) -> str:
        price_selectors = [
            '#select_quantity div[class*="style_m_select_quantity_container__"] div div[class*="style_m_select_choice_option_wrapper__"] div[class*="style_m_people_price__"] div div span span',
            "#select_quantity > div.xtaro-xview > div > div.xtaro-xview > div.xtaro-xview > div > div > span > span",
            'div[class*="default_new_title_foot_warp"] div[class*="default_new_title_foot_warp_right"] div div div span',
            'span[currency="HKD"][testid*="price"]',
            "span.price_m_price_price__VrAgh",
            'div.price_m_price__FmWsJ span[currency="HKD"]',
            'span[testid="undefined-price"]',
            'span[currency="HKD"]',
        ]
        for attempt in range(max_attempts):
            self._raise_if_stopped(stop_event)
            try:
                for selector in price_selectors:
                    try:
                        elements = self.driver.find_elements(By.CSS_SELECTOR, selector)
                        for element in elements:
                            if not element.is_displayed():
                                continue
                            price_text = element.text.strip()
                            if price_text and "HK$" in price_text and len(price_text) > 3 and re.match(r"HK\$\d+(\.\d+)?", price_text):
                                self.emit({"event": "log", "stage": "sku", "message": f"found price {price_text} selector={selector[:50]}"})
                                return price_text
                    except Exception:
                        continue

                elements = self.driver.find_elements(By.XPATH, "//*[contains(text(), 'HK$') and not(contains(@style, 'display: none'))]")
                for element in elements:
                    try:
                        if not element.is_displayed():
                            continue
                        price_text = element.text.strip()
                        if price_text and price_text.startswith("HK$") and re.match(r"HK\$\d+(\.\d+)?", price_text):
                            self.emit({"event": "log", "stage": "sku", "message": f"found price by fallback: {price_text}"})
                            return price_text
                    except Exception:
                        continue
                if attempt < max_attempts - 1:
                    time.sleep(0.1)
            except StopRequested:
                raise
            except Exception as exc:
                if attempt < max_attempts - 1:
                    time.sleep(0.1)
                else:
                    self.emit({"event": "log", "stage": "sku", "message": f"price lookup failed: {exc}"})
        return "价格未找到"

    def _get_default_price(self) -> str:
        return self._get_current_price_robust()

    def _extract_real_price(self) -> tuple[str, str]:
        if not self.request.extract_real_price:
            return "N/A", "N/A"

        price_selector = 'div[class*="price-comp_m_footer_left_total"] div div div span span'
        try:
            elements = self.driver.find_elements(By.CSS_SELECTOR, price_selector)
            for element in elements:
                try:
                    if element.is_displayed():
                        price_text = element.text.strip()
                        if price_text:
                            return self._split_price(price_text)
                except Exception:
                    continue
        except Exception:
            pass
        return "N/A", ""

    def _extract_sku_promotional_tag(self) -> str:
        sku_promo_selector = 'div[class*="price-comp_m_footer_left"] div span'
        try:
            elements = self.driver.find_elements(By.CSS_SELECTOR, sku_promo_selector)
            for element in elements:
                try:
                    if not element.is_displayed():
                        continue
                    text = element.text.strip()
                    if text and "HK$" not in text and ("減" in text or "%" in text or "折" in text):
                        self.emit({"event": "log", "stage": "sku", "message": f"found SKU promo tag: {text}"})
                        return text
                except Exception:
                    continue
        except Exception as exc:
            self.emit({"event": "log", "stage": "sku", "message": f"SKU promo tag extraction failed: {exc}"})
        return ""

    def _wait_ready(self) -> None:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            try:
                if self.driver.execute_script("return document.readyState") in {"interactive", "complete"}:
                    return
            except WebDriverException:
                pass
            time.sleep(0.2)

    def _scroll_page(self, stop_event=None) -> None:
        for _ in range(4):
            self._raise_if_stopped(stop_event)
            try:
                self.driver.execute_script("window.scrollTo(0, document.body.scrollHeight);")
            except Exception:
                return
            time.sleep(0.6)
        try:
            self.driver.execute_script("window.scrollTo(0, 0);")
        except Exception:
            pass

    def _stop_requested(self, stop_event) -> bool:
        try:
            return bool(stop_event and stop_event.is_set())
        except Exception:
            return False

    def _raise_if_stopped(self, stop_event) -> None:
        if self._stop_requested(stop_event):
            raise StopRequested()

    def _stop_browser_load(self) -> None:
        if not self.driver:
            return
        try:
            self.driver.execute_script("window.stop();")
        except Exception:
            pass

    def _first_text(self, selectors: list[str]) -> str:
        for selector in selectors:
            try:
                for element in self.driver.find_elements(By.CSS_SELECTOR, selector):
                    text = (element.text or "").strip()
                    if text:
                        return " ".join(text.split())
            except Exception:
                continue
        return ""

    def _title_from_document(self) -> str:
        try:
            title = self.driver.execute_script("return document.title || ''")
            return str(title).split("|")[0].strip()
        except Exception:
            return ""

    def _page_text(self) -> str:
        try:
            return self.driver.find_element(By.TAG_NAME, "body").text
        except Exception:
            return ""

    def _booking_count(self) -> str:
        text = self._page_text()
        match = re.search(r"([\d,]+)\s*(?:次預訂|次预订|booked|Bookings?)", text, re.I)
        return match.group(1).replace(",", "") if match else ""

    def _review_count(self) -> str:
        text = self._page_text()
        match = re.search(r"([\d,]+)\s*(?:則評價|条评价|reviews?)", text, re.I)
        return match.group(1).replace(",", "") if match else ""

    def _bad_review_count(self) -> str:
        text = self._page_text()
        match = re.search(r"(?:中差|negative|bad)[^\d]{0,10}([\d,]+)", text, re.I)
        return match.group(1).replace(",", "") if match else ""

    def _score_near(self, label: str) -> str:
        text = self._page_text()
        pattern = re.escape(label) + r".{0,30}?(\d+(?:\.\d+)?)"
        match = re.search(pattern, text, re.I | re.S)
        return match.group(1) if match else ""

    def _product_info(self) -> str:
        text = self._page_text()
        for label in ("Product Info", "商品資訊", "商品资讯", "Details"):
            index = text.find(label)
            if index >= 0:
                return " ".join(text[index : index + 500].split())
        return ""

    def _promo_text(self) -> str:
        text = self._page_text()
        for line in text.splitlines():
            if any(token in line.lower() for token in ("off", "save", "优惠", "折", "減")):
                return line.strip()[:80]
        return ""

    def _split_price(self, value: str) -> tuple[str, str]:
        price_text = value or ""
        if not price_text or price_text in {"价格未找到", "未找到"}:
            return "N/A", "N/A"
        currency = "N/A"
        if "HK$" in price_text:
            currency = "HKD"
            price_text = price_text.replace("HK$", "").strip()
        elif "$" in price_text:
            currency = "USD"
            price_text = price_text.replace("$", "").strip()
        elif "¥" in price_text or "￥" in price_text or "CNY" in price_text:
            currency = "CNY"
            price_text = price_text.replace("¥", "").replace("￥", "").replace("CNY", "").strip()

        match = re.search(r"([0-9,]+\.?[0-9]*)", price_text)
        if not match:
            return currency, price_text
        return currency, match.group(1).replace(",", "")

    def _product_id(self, url: str) -> str:
        match = re.search(r"/detail/(\d+)", url)
        if match:
            return match.group(1)
        match = re.search(r"(\d{5,})", url)
        return match.group(1) if match else ""

    def _chrome_path(self) -> str | None:
        return detect_chrome_path()

    def _chrome_major_version(self) -> int | None:
        return chrome_major_version(self.request.browser_options.chrome_path or self._chrome_path())
