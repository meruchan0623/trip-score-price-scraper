"""Hermes final-response guard for manual Trip product scrapes."""

from __future__ import annotations

import os
import re
import time
from datetime import datetime
from pathlib import Path
from typing import NamedTuple


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
    r"(?:請|请)?抓取(?:以下|這些|这些|這個|这个)?(?:產品|产品)?(?:的)?(?:價格|价格)",
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


class ManualTurn(NamedTuple):
    product_ids: tuple[str, ...]
    output_dir: Path
    started_at: float


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

    output_dir = Path(output_root) / f"manual-{'-'.join(product_ids)}"
    return ManualTurn(
        product_ids=product_ids,
        output_dir=output_dir,
        started_at=time.time() if started_at is None else started_at,
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


def filter_manual_response(response_text: str, turn: ManualTurn) -> str:
    """Replace a manual scrape response with an allowlisted warning and attachment."""

    workbook = _fresh_native_workbook(turn)
    if workbook is None:
        return f"抓取失败：未生成原生Excel（{turn.output_dir}）"

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
    return (
        "本轮是单个或数个指定Trip产品的价格抓取。完整读取并遵守："
        f"{skill_path}。使用其中固定的可视浏览器、fast、product-info、"
        "no-quality-rerun和output-dir参数。完成后只交付原生Excel附件；"
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
