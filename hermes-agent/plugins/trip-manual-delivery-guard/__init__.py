"""Hermes final-response guard for manual Trip product scrapes."""

from __future__ import annotations

import os
import re
import time
import zipfile
from datetime import datetime
from pathlib import Path
from typing import NamedTuple
from urllib.parse import parse_qs, urlparse
from xml.etree import ElementTree


DEFAULT_OUTPUT_ROOT = Path(
    os.environ.get(
        "TRIP_MANUAL_OUTPUT_ROOT",
        "/Users/example/Documents/trip-sku-price-scrape",
    )
)
NATIVE_WORKBOOK_PATTERN = "trip_scoreandprice_results_*.xlsx"
FRESHNESS_GRACE_SECONDS = 0.0

_PRODUCT_ID_RE = re.compile(r"(?<!\d)(\d{7,12})(?!\d)")
_TRIP_DETAIL_RE = re.compile(
    r"https?://[^\s/]*trip\.com/things-to-do/detail/(\d{7,12})(?:[/\s?]|$)",
    re.IGNORECASE,
)
_MANUAL_INTENT_RE = re.compile(
    r"(?:請|请)?(?:抓取|抓)(?:以下|這些|这些|這個|这个)?(?:產品|产品)?(?:的)?(?:價格|价格)",
    re.IGNORECASE,
)
_ID_LIST_SEPARATORS_RE = re.compile(r"[\s,，、;；|]+")
_SKU_CLICK_FAILURE_RE = re.compile(
    r"SKU[^\n。；;]{0,40}(?:層級|层级)?[^\n。；;]{0,20}(?:點擊失敗|点击失败)",
    re.IGNORECASE,
)
_WORKBOOK_TIMESTAMP_RE = re.compile(
    r"^trip_scoreandprice_results_(\d{6}-\d{4})\.xlsx$"
)
_URL_SPAN_RE = re.compile(r"https?://[^\s]+", re.IGNORECASE)
_AMBIGUOUS_MARKET_RE = re.compile(r"美元区|歐洲價格|欧洲价格|几个主要市场|幾個主要市場|無法一一對應|无法一一对应|\s或\s|或者|或")
_FAILURE_RE = re.compile(r"失敗|失败|超時|超时|中斷|中断|blocked|block|risk|异常|異常", re.I)
_XML_MAIN = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
_XML_REL = "{http://schemas.openxmlformats.org/officeDocument/2006/relationships}"
_XML_PACKAGE_REL = "{http://schemas.openxmlformats.org/package/2006/relationships}"


class RequestedMarket(NamedTuple):
    """A resolved public Trip site context; URL input hosts are never inferred."""

    code: str
    host: str
    locale: str
    currency: str
    chinese_names: tuple[str, ...] = ()


SUPPORTED_MARKETS = (
    RequestedMarket("global", "www.trip.com", "en-XX", "USD", ("全球", "国际", "國際")),
    RequestedMarket("hk", "hk.trip.com", "zh-HK", "HKD", ("香港", "港澳")),
    RequestedMarket("sg", "sg.trip.com", "en-SG", "SGD", ("新加坡",)),
    RequestedMarket("us", "us.trip.com", "en-US", "USD", ("美国", "美國", "美国站", "美國站")),
    RequestedMarket("tw", "tw.trip.com", "zh-TW", "TWD", ("台湾", "台灣")),
    RequestedMarket("jp", "jp.trip.com", "ja-JP", "JPY", ("日本",)),
    RequestedMarket("kr", "kr.trip.com", "ko-KR", "KRW", ("韩国", "韓國", "南韩", "南韓")),
    RequestedMarket("th", "th.trip.com", "th-TH", "THB", ("泰国", "泰國")),
    RequestedMarket("vn", "vn.trip.com", "vi-VN", "VND", ("越南",)),
    RequestedMarket("id", "id.trip.com", "id-ID", "IDR", ("印尼", "印度尼西亚", "印度尼西亞")),
    RequestedMarket("my", "my.trip.com", "ms-MY", "MYR", ("马来西亚", "馬來西亞")),
    RequestedMarket("au", "au.trip.com", "en-AU", "AUD", ("澳大利亚", "澳洲", "澳大利亞")),
    RequestedMarket("uk", "uk.trip.com", "en-GB", "GBP", ("英国", "英國")),
    RequestedMarket("de", "de.trip.com", "de-DE", "EUR", ("德国", "德國")),
    RequestedMarket("fr", "fr.trip.com", "fr-FR", "EUR", ("法国", "法國")),
    RequestedMarket("es", "es.trip.com", "es-ES", "EUR", ("西班牙",)),
    RequestedMarket("it", "it.trip.com", "it-IT", "EUR", ("意大利", "義大利")),
    RequestedMarket("nl", "nl.trip.com", "nl-NL", "EUR", ("荷兰", "荷蘭")),
    RequestedMarket("pt", "pt.trip.com", "pt-PT", "EUR", ("葡萄牙",)),
    RequestedMarket("br", "br.trip.com", "pt-BR", "BRL", ("巴西",)),
    RequestedMarket("ca", "ca.trip.com", "en-CA", "CAD", ("加拿大",)),
    RequestedMarket("ae", "ae.trip.com", "en-AE", "AED", ("阿联酋", "阿聯酋", "迪拜", "杜拜")),
    RequestedMarket("sa", "sa.trip.com", "ar-SA", "SAR", ("沙特", "沙烏地")),
    RequestedMarket("in", "in.trip.com", "en-IN", "INR", ("印度",)),
    RequestedMarket("ph", "ph.trip.com", "en-PH", "PHP", ("菲律宾", "菲律賓")),
    RequestedMarket("ua", "ua.trip.com", "uk-UA", "UAH", ("乌克兰", "烏克蘭")),
    RequestedMarket("dk", "dk.trip.com", "da-DK", "DKK", ("丹麦", "丹麥")),
    RequestedMarket("pl", "pl.trip.com", "pl-PL", "PLN", ("波兰", "波蘭")),
    RequestedMarket("fi", "fi.trip.com", "fi-FI", "EUR", ("芬兰", "芬蘭")),
    RequestedMarket("se", "se.trip.com", "sv-SE", "SEK", ("瑞典",)),
    RequestedMarket("tr", "tr.trip.com", "tr-TR", "TRY", ("土耳其",)),
    RequestedMarket("gr", "gr.trip.com", "el-GR", "EUR", ("希腊", "希臘")),
    RequestedMarket("ru", "ru.trip.com", "ru-RU", "RUB", ("俄罗斯", "俄羅斯")),
)
_MARKET_BY_CODE = {market.code: market for market in SUPPORTED_MARKETS}
_CURRENCY_RECOMMENDATIONS = {
    "USD": "us", "HKD": "hk", "SGD": "sg", "TWD": "tw", "JPY": "jp", "KRW": "kr",
    "THB": "th", "VND": "vn", "IDR": "id", "MYR": "my", "AUD": "au", "GBP": "uk",
    "EUR": "de", "BRL": "br", "CAD": "ca", "AED": "ae", "SAR": "sa", "INR": "in",
    "PHP": "ph", "UAH": "ua", "DKK": "dk", "PLN": "pl", "SEK": "se", "TRY": "tr", "RUB": "ru",
}
_CURRENCY_NAMES = {
    "USD": ("美元",), "HKD": ("港币", "港幣"), "SGD": ("新币", "新幣", "新加坡元"),
    "TWD": ("台币", "台幣", "新台币", "新台幣"), "JPY": ("日元", "日圓"), "KRW": ("韩元", "韓元"),
    "THB": ("泰铢", "泰銖"), "VND": ("越南盾",), "IDR": ("印尼盾",), "MYR": ("马币", "馬幣"),
    "AUD": ("澳元",), "GBP": ("英镑", "英鎊"), "EUR": ("欧元", "歐元"), "BRL": ("巴西雷亚尔", "巴西雷亞爾"),
    "CAD": ("加元",), "AED": ("迪拉姆",), "SAR": ("里亚尔", "里亞爾"), "INR": ("印度卢比", "印度盧比"),
    "PHP": ("菲律宾比索", "菲律賓比索"), "UAH": ("格里夫纳", "格里夫納"), "DKK": ("丹麦克朗", "丹麥克朗"),
    "PLN": ("兹罗提", "茲羅提"), "SEK": ("瑞典克朗",), "TRY": ("土耳其里拉",), "RUB": ("卢布", "盧布"),
}


class ManualTurn(NamedTuple):
    product_ids: tuple[str, ...]
    output_dir: Path
    started_at: float
    markets: tuple[RequestedMarket, ...] = ()
    multi_market: bool = False
    clarification: str | None = None


def _message_text(message) -> str:
    content = message.get("content", "") if isinstance(message, dict) else ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(
            item.get("text", "")
            for item in content
            if isinstance(item, dict) and isinstance(item.get("text"), str)
        )
    return ""


def _ordered_ids(text: str) -> tuple[str, ...]:
    url_ids = _TRIP_DETAIL_RE.findall(text)
    if url_ids:
        return tuple(url_ids)
    return tuple(_PRODUCT_ID_RE.findall(text))


def _is_id_only_message(text: str, product_ids: tuple[str, ...]) -> bool:
    if not product_ids:
        return False
    remainder = _PRODUCT_ID_RE.sub("", text)
    return _ID_LIST_SEPARATORS_RE.sub("", remainder) == ""


def _history_has_manual_intent(conversation_history: list) -> bool:
    for message in reversed(conversation_history[-8:]):
        if not isinstance(message, dict) or message.get("role") != "user":
            continue
        text = _message_text(message)
        return bool(_MANUAL_INTENT_RE.search(text)) and not _ordered_ids(text)
    return False


def _market_mentions(text: str) -> list[tuple[int, int, str, RequestedMarket | str]]:
    """Find explicit site/currency mentions in user order, without URL-host inference."""

    mentions: list[tuple[int, int, str, RequestedMarket | str]] = []
    for market in SUPPORTED_MARKETS:
        for name in (market.code, market.host, *market.chinese_names):
            pattern = re.escape(name)
            if name.isascii() and name.isalpha():
                pattern = rf"(?<![a-z0-9]){pattern}(?![a-z0-9])"
            for match in re.finditer(pattern, text, re.I):
                mentions.append((match.start(), match.end(), "site", market))
    for currency, names in _CURRENCY_NAMES.items():
        for name in (currency, *names):
            pattern = re.escape(name)
            if name.isascii():
                pattern = rf"(?<![a-z0-9]){pattern}(?![a-z0-9])"
            for match in re.finditer(pattern, text, re.I):
                mentions.append((match.start(), match.end(), "currency", currency))
    selected: list[tuple[int, int, str, RequestedMarket | str]] = []
    for candidate in sorted(mentions, key=lambda item: (-(item[1] - item[0]), item[0], item[2])):
        if any(candidate[0] < chosen[1] and chosen[0] < candidate[1] for chosen in selected):
            continue
        selected.append(candidate)
    return sorted(selected, key=lambda item: (item[0], item[1], item[2]))


def _same_phrase(text: str, earlier_end: int, later_start: int) -> bool:
    between = text[earlier_end:later_start]
    return len(between) <= 12 and not re.search(r"[,，、;；。.!！？\n]", between)


def _replace_currency(market: RequestedMarket, currency: str) -> RequestedMarket:
    return RequestedMarket(market.code, market.host, market.locale, currency, market.chinese_names)


def _resolve_requested_markets(text: str) -> tuple[tuple[RequestedMarket, ...], str | None]:
    market_text = _URL_SPAN_RE.sub(lambda match: " " * len(match.group()), text)
    if _AMBIGUOUS_MARKET_RE.search(market_text):
        return (), "市场范围不明确。请明确逐一指定站点或币种（例如“香港、 日本”或“JPY、USD”）后再抓取。"

    resolved: list[tuple[RequestedMarket, int]] = []
    for start, end, kind, value in _market_mentions(market_text):
        if kind == "site":
            market = value
            assert isinstance(market, RequestedMarket)
            resolved.append((market, end))
            continue
        currency = value
        assert isinstance(currency, str)
        if resolved and _same_phrase(market_text, resolved[-1][1], start):
            resolved[-1] = (_replace_currency(resolved[-1][0], currency), end)
        else:
            resolved.append((_MARKET_BY_CODE[_CURRENCY_RECOMMENDATIONS[currency]], end))

    if not resolved:
        return (_MARKET_BY_CODE["hk"],), None
    unique: list[RequestedMarket] = []
    for market, _ in resolved:
        identity = (market.code, market.locale, market.currency)
        if not any((item.code, item.locale, item.currency) == identity for item in unique):
            unique.append(market)
    return tuple(unique), None


def detect_manual_request(
    user_message: str,
    conversation_history: list,
    *,
    output_root: Path = DEFAULT_OUTPUT_ROOT,
    started_at: float | None = None,
) -> ManualTurn | None:
    """Return turn metadata only for a selected-product price scrape request."""

    product_ids = _ordered_ids(user_message)
    if not product_ids:
        return None

    has_direct_intent = bool(_MANUAL_INTENT_RE.search(user_message))
    has_trip_url_intent = bool(_TRIP_DETAIL_RE.search(user_message)) and bool(
        re.search(r"抓|價格|价格", user_message)
    )
    is_intent_followup = _is_id_only_message(user_message, product_ids) and (
        _history_has_manual_intent(conversation_history)
    )
    if not (has_direct_intent or has_trip_url_intent or is_intent_followup):
        return None

    markets, clarification = _resolve_requested_markets(user_message)
    output_dir = Path(output_root) / f"manual-{'-'.join(product_ids)}"
    return ManualTurn(
        product_ids=product_ids,
        output_dir=output_dir,
        started_at=time.time() if started_at is None else started_at,
        markets=markets,
        multi_market=len(markets) > 1,
        clarification=clarification,
    )


def _fresh_native_workbook(turn: ManualTurn) -> Path | None:
    cutoff = turn.started_at - FRESHNESS_GRACE_SECONDS
    cutoff_minute = datetime.fromtimestamp(cutoff).replace(second=0, microsecond=0)
    candidates: list[tuple[float, Path]] = []
    if turn.output_dir.is_dir():
        for path in turn.output_dir.glob(NATIVE_WORKBOOK_PATTERN):
            try:
                match = _WORKBOOK_TIMESTAMP_RE.match(path.name)
                if not match:
                    continue
                filename_time = datetime.strptime(match.group(1), "%y%m%d-%H%M")
                modified_at = path.stat().st_mtime
                if (
                    path.is_file()
                    and modified_at >= cutoff
                    and filename_time >= cutoff_minute
                ):
                    candidates.append((modified_at, path))
            except (OSError, ValueError):
                continue
    if not candidates:
        return None
    return max(candidates, key=lambda item: (item[0], item[1].name))[1]


def _affected_product_id(response_text: str, turn: ManualTurn) -> str | None:
    for line in response_text.splitlines():
        if not _SKU_CLICK_FAILURE_RE.search(line):
            continue
        for product_id in turn.product_ids:
            if product_id in line:
                return product_id
    return None


def _normalized_warning(response_text: str, turn: ManualTurn) -> str | None:
    if re.search(
        r"(?:原生)?Excel[^\n。；;]{0,40}(?:校驗|校验|驗證|验证)(?:失敗|失败|異常|异常|未通過|未通过)",
        response_text,
        re.I,
    ):
        return "抓取结果异常：原生Excel校验未通过。"
    if re.search(r"抓取结果异常[:：]", response_text):
        return "抓取结果异常：抓取结果可能不完整。"

    warnings = []

    if _SKU_CLICK_FAILURE_RE.search(response_text):
        product_id = _affected_product_id(response_text, turn)
        subject = f"{product_id} " if product_id else ""
        warnings.append(f"{subject}部分SKU层级点击失败，结果可能不完整")

    if re.search(
        r"反爬|anti[- ]?bot|API(?:接口)?\s*(?:拦截|阻断|block(?:ed|ing)?)",
        response_text,
        re.I,
    ):
        warnings.append("检测到反爬/API拦截，结果可能不完整")
    if re.search(r"超時|超时|timed?\s*out|timeout", response_text, re.I):
        warnings.append("抓取发生超时，结果可能不完整")
    if re.search(r"中斷|中断|interrupted", response_text, re.I):
        warnings.append("抓取被中断，结果可能不完整")
    if re.search(r"部分(?:產品|产品)(?:抓取)?(?:失敗|失败)", response_text):
        warnings.append("部分产品抓取失败，结果可能不完整")
    if not warnings and re.search(r"結果可能不完整|结果可能不完整", response_text):
        warnings.append("抓取结果可能不完整")
    if not warnings and re.search(r"抓取重要警告[:：]", response_text):
        warnings.append("抓取结果可能不完整")

    unique_warnings = list(dict.fromkeys(warnings))
    if not unique_warnings:
        return None
    return f"抓取重要警告：{'；'.join(unique_warnings)}。"


def _sheet_names(turn: ManualTurn) -> list[str]:
    if not turn.multi_market:
        return ["Trip商品数据"]
    bases = [f"{market.code.upper()}-{market.currency.upper()}" for market in turn.markets]
    return [
        f"{base}-{market.locale}" if bases.count(base) > 1 else base
        for market, base in zip(turn.markets, bases)
    ]


def _xlsx_shared_strings(archive: zipfile.ZipFile) -> list[str]:
    try:
        root = ElementTree.fromstring(archive.read("xl/sharedStrings.xml"))
    except (KeyError, ElementTree.ParseError):
        return []
    return ["".join(node.itertext()) for node in root.findall(f"{_XML_MAIN}si")]


def _xlsx_cell_value(cell: ElementTree.Element, shared_strings: list[str]) -> str:
    cell_type = cell.attrib.get("t")
    if cell_type == "inlineStr":
        return "".join(cell.itertext())
    value = cell.findtext(f"{_XML_MAIN}v", default="")
    if cell_type == "s":
        try:
            return shared_strings[int(value)]
        except (IndexError, ValueError):
            return ""
    return value


def _xlsx_rows(archive: zipfile.ZipFile, path: str, shared_strings: list[str]) -> list[list[str]]:
    root = ElementTree.fromstring(archive.read(path))
    rows: list[list[str]] = []
    for row in root.findall(f".//{_XML_MAIN}row"):
        values: list[str] = []
        for cell in row.findall(f"{_XML_MAIN}c"):
            reference = cell.attrib.get("r", "A1")
            letters = "".join(character for character in reference if character.isalpha())
            column = 0
            for character in letters:
                column = column * 26 + ord(character.upper()) - ord("A") + 1
            while len(values) < column:
                values.append("")
            values[column - 1] = _xlsx_cell_value(cell, shared_strings).strip()
        rows.append(values)
    return rows


def _native_headers_valid(headers: list[str]) -> bool:
    required_prefix = ["商品ID", "URL", "商品名称"]
    if headers[:3] != required_prefix:
        return False
    index = 3
    sku_count = 0
    while index < len(headers) and headers[index] == f"SKU层级{sku_count + 1}":
        sku_count += 1
        index += 1
    if sku_count == 0:
        return False
    required = ["货币", "外显价格"]
    if headers[index:index + len(required)] != required:
        return False
    index += len(required)
    optional_then_required = ("实价", "促销标签", "价格可信标签")
    if index < len(headers) and headers[index] == optional_then_required[0]:
        index += 1
    if headers[index:index + 2] != list(optional_then_required[1:]):
        return False
    index += 2
    if index < len(headers) and headers[index] == "原始捕获价格":
        index += 1
    if index < len(headers) and headers[index] == "商品资讯":
        index += 1
    tail = ["总促销标签", "月销量", "年销量", "总销量", "评价数", "评分", "好评率", "客户服务", "方便程度", "讯号状态", "中差评价数"]
    if headers[index:index + len(tail)] != tail:
        return False
    index += len(tail)
    return headers[index:] in ([], ["抓取备注"])


def _row_is_trusted(row: dict[str, str], market: RequestedMarket) -> bool:
    required = ("商品ID", "URL", "商品名称", "SKU层级1", "货币", "价格可信标签")
    if any(not row.get(key, "").strip() for key in required):
        return False
    if not (row.get("外显价格", "").strip() or row.get("实价", "").strip()):
        return False
    if row["价格可信标签"] not in {"trusted_base", "trusted_promo"}:
        return False
    if row["货币"].upper() != market.currency:
        return False
    parsed = urlparse(row["URL"])
    query = parse_qs(parsed.query)
    return (
        parsed.hostname == market.host
        and query.get("locale", [""])[0].lower() == market.locale.lower()
        and query.get("curr", [""])[0].upper() == market.currency
    )


def _workbook_sheet_paths(archive: zipfile.ZipFile) -> list[tuple[str, str]]:
    workbook = ElementTree.fromstring(archive.read("xl/workbook.xml"))
    relationships = ElementTree.fromstring(archive.read("xl/_rels/workbook.xml.rels"))
    rel_targets = {
        relationship.attrib["Id"]: relationship.attrib["Target"]
        for relationship in relationships.findall(f"{_XML_PACKAGE_REL}Relationship")
    }
    result = []
    for sheet in workbook.findall(f".//{_XML_MAIN}sheet"):
        target = rel_targets.get(sheet.attrib.get(f"{_XML_REL}id", ""), "")
        if not target or target.startswith("/") or ".." in Path(target).parts:
            raise ValueError("invalid worksheet relationship")
        result.append((sheet.attrib.get("name", ""), str(Path("xl") / target)))
    return result


def validate_native_workbook(
    workbook: Path,
    turn: ManualTurn,
    failed_market_labels: tuple[str, ...] = (),
) -> tuple[bool, str]:
    """Validate the native XLSX structure without using content for user-facing prose."""

    if turn.clarification:
        return False, "market-clarification-required"
    try:
        with zipfile.ZipFile(workbook) as archive:
            expected = _sheet_names(turn)
            sheet_paths = _workbook_sheet_paths(archive)
            actual = [name for name, _ in sheet_paths]
            if any(name not in expected for name in actual):
                return False, "unexpected-sheet"
            positions = [expected.index(name) for name in actual]
            if positions != sorted(positions) or len(set(actual)) != len(actual):
                return False, "sheet-order"
            missing = [name for name in expected if name not in actual]
            if tuple(missing) != failed_market_labels:
                return False, "partial-market-labels"
            if not actual:
                return False, "empty-workbook"
            shared_strings = _xlsx_shared_strings(archive)
            market_by_name = dict(zip(expected, turn.markets))
            for name, sheet_path in sheet_paths:
                rows = _xlsx_rows(archive, sheet_path, shared_strings)
                if len(rows) < 2 or not _native_headers_valid(rows[0]):
                    return False, "native-header"
                headers = rows[0]
                data_rows = [row for row in rows[1:] if any(value.strip() for value in row)]
                if not data_rows:
                    return False, "empty-market-sheet"
                for values in data_rows:
                    row = {header: values[index] if index < len(values) else "" for index, header in enumerate(headers)}
                    if not _row_is_trusted(row, market_by_name[name]):
                        return False, "untrusted-row"
    except (OSError, ValueError, zipfile.BadZipFile, ElementTree.ParseError):
        return False, "invalid-xlsx"
    return True, "ok"


def _failed_market_labels(response_text: str, turn: ManualTurn) -> tuple[str, ...]:
    if not _FAILURE_RE.search(response_text):
        return ()
    return tuple(name for name in _sheet_names(turn) if name.lower() in response_text.lower())


def filter_manual_response(response_text: str, turn: ManualTurn) -> str:
    """Replace a manual scrape response with an allowlisted warning and attachment."""

    workbook = _fresh_native_workbook(turn)
    if workbook is None:
        return f"抓取失败：未生成原生Excel（{turn.output_dir}）"

    failed_market_labels = _failed_market_labels(response_text, turn)
    valid, _ = validate_native_workbook(workbook, turn, failed_market_labels)
    if not valid:
        return f"抓取结果异常：原生Excel校验未通过。\n{workbook}"

    if failed_market_labels:
        labels = "、".join(failed_market_labels)
        return f"抓取重要警告：{labels} 抓取失败，附件仅包含成功市场\n{workbook}"

    warning = _normalized_warning(response_text, turn)
    if warning:
        return f"{warning}\n{workbook}"
    return str(workbook)


def _manual_context(turn: ManualTurn) -> str:
    project_root = Path(__file__).resolve().parents[3]
    skill_path = (
        project_root
        / "hermes-agent"
        / "skills"
        / "trip-sku-price-scraper-operations"
        / "SKILL.md"
    )
    if turn.clarification:
        return f"{turn.clarification} 在用户澄清前，禁止构造或启动任何抓取命令。"
    market_args = " ".join(
        f"--market {market.code}:{market.locale}:{market.currency}" for market in turn.markets
    )
    if not turn.multi_market:
        market = turn.markets[0]
        if market.code == "hk" and market.locale == "zh-HK" and market.currency == "HKD":
            market_args = ""
        else:
            market_args = f"--site {market.code} --locale {market.locale} --currency {market.currency}"
    return (
        "本轮是指定Trip产品价格抓取。完整读取并遵守："
        f"{skill_path}。固定使用可视浏览器、normal、product-info、no-quality-rerun和output-dir参数。"
        f"市场参数：{market_args or '默认 hk / zh-HK / HKD'}。"
        "输入URL的host只识别产品ID，绝不据此推断市场。完成后只交付原生Excel附件，且必须先通过XLSX结构校验；"
        "禁止输出确认、进度、产品信息、价格、SKU数量、对比、总结或二次整理表。"
        "只有失败或影响结果完整性的异常可以使用Skill规定的单行前缀。"
    )


def register(ctx, *, output_root: Path = DEFAULT_OUTPUT_ROOT):
    """Register task detection and final-response filtering hooks with Hermes."""

    active_turns: dict[str, ManualTurn] = {}

    def pre_llm_call(
        session_id: str,
        user_message: str,
        conversation_history: list,
        **kwargs,
    ):
        del kwargs
        turn = detect_manual_request(
            user_message,
            conversation_history,
            output_root=Path(output_root),
        )
        if turn is None:
            active_turns.pop(session_id, None)
            return None
        active_turns[session_id] = turn
        return {"context": _manual_context(turn)}

    def transform_llm_output(response_text: str, session_id: str, **kwargs):
        del kwargs
        turn = active_turns.pop(session_id, None)
        if turn is None:
            return None
        return filter_manual_response(response_text, turn)

    ctx.register_hook("pre_llm_call", pre_llm_call)
    ctx.register_hook("transform_llm_output", transform_llm_output)
