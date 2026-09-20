from __future__ import annotations

import importlib.util
from pathlib import Path
from unittest.mock import patch


SCRIPT = Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_mode2_direct_delivery.py"
SPEC = importlib.util.spec_from_file_location("trip_mode2_direct_delivery", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def test_promo_message_is_one_compact_message_without_raw_labels(tmp_path: Path) -> None:
    current_path = tmp_path / "260818-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "260817-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    current = {
        ("100", "HK"): ("預訂指定套票減50% | 預訂指定套票減5%", "哈薩克斯坦 eSIM｜1-30天｜QR code", "TSIM 網址"),
        ("100", "RU"): ("скидка 50 % | скидка 5 %", "Казахстан eSIM | QR", "TSIM 網址"),
        ("200", "HK"): ("指定產品減20%", "文案变化产品", "BC"),
    }
    previous = {
        ("100", "HK"): ("預訂指定套票減50% | 預訂指定套票減8%", "哈薩克斯坦 eSIM | old", "TSIM 網址"),
        ("100", "RU"): ("скидка 50 % | скидка 8 %", "Казахстан eSIM | old", "TSIM 網址"),
        # Different wording, identical percentage: must not be reported.
        ("200", "HK"): ("預訂指定套票省20%", "文案变化产品"),
    }

    with patch.object(MODULE, "tags", side_effect=[current, previous]):
        message = MODULE.promo_message(current_path, previous_path)

    assert len(message) <= MODULE.SINGLE_MESSAGE_LIMIT <= 4096
    assert "变化 1 产品／1 处：撤销 0｜力度变化 1｜新增 0" in message
    # Product 100 collapses to one row: vendor code, merged site codes, discount codes.
    assert "100｜S｜HR｜8+50→5+50" in message
    assert "200" not in message
    # Site/vendor tallies and the code legend are present.
    assert "站点 H1 R1" in message
    assert "厂商 S1" in message
    assert "H=HK" in message and "S=TSIM 網址" in message
    # Localized promotion wording stays in the workbook and never floods the message.
    assert "預訂" not in message and "скидка" not in message


def _change_pairs(count: int, kind: str, offset: int = 0) -> tuple[dict, dict]:
    sites = ["HK", "TW", "JP", "TH", "VN", "RU"]
    current: dict = {}
    previous: dict = {}
    for index in range(count):
        pid = str(70000000 + offset + index)
        for site in sites:
            if kind == "lost":
                previous[(pid, site)] = ("預訂指定套票減50% | 預訂指定套票減5%", "商品", "BC")
                current[(pid, site)] = ("無促銷", "商品", "BC")
            elif kind == "gain":
                current[(pid, site)] = ("預訂指定套票減50% | 預訂指定套票減5%", "商品", "BC")
                previous[(pid, site)] = ("無促銷", "商品", "BC")
            else:
                previous[(pid, site)] = ("預訂指定套票減50% | 預訂指定套票減8%", "商品", "BC")
                current[(pid, site)] = ("預訂指定套票減50% | 預訂指定套票減5%", "商品", "BC")
    return current, previous


def test_big_change_day_still_fits_a_single_telegram_message(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    current, previous = _change_pairs(400, "gain")
    lost_cur, lost_prev = _change_pairs(120, "lost", 100000)

    with patch.object(MODULE, "tags", side_effect=[{**current, **lost_cur}, {**previous, **lost_prev}]):
        message = MODULE.promo_message(current_path, previous_path)

    assert len(message) <= MODULE.SINGLE_MESSAGE_LIMIT
    assert len(message) <= 4096
    # The actionable section survives the budget cut, the bulk one is summarised.
    assert "▼撤销折扣 120" in message
    assert "未列示" in message and "完整明细见同日 XLSX" in message


def test_priority_order_keeps_lost_promotions_before_new_ones(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    lost_cur, lost_prev = _change_pairs(2, "lost", 0)
    gain_cur, gain_prev = _change_pairs(2, "gain", 1000)

    with patch.object(MODULE, "tags", side_effect=[{**lost_cur, **gain_cur}, {**lost_prev, **gain_prev}]):
        message = MODULE.promo_message(current_path, previous_path)

    body = message.splitlines()
    assert next(i for i, line in enumerate(body) if line.startswith("▼撤销折扣")) < next(
        i for i, line in enumerate(body) if line.startswith("▼新增折扣")
    )
    # 撤销 rows are inside the 撤销 section, before the 新增 section starts.
    lost_index = next(i for i, line in enumerate(body) if line.startswith("▼撤销折扣"))
    gain_index = next(i for i, line in enumerate(body) if line.startswith("▼新增折扣"))
    assert all("→-" in line for line in body[lost_index + 1 : gain_index])


def test_omission_tail_counts_every_unlisted_section(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    lost_cur, lost_prev = _change_pairs(10, "lost", 0)
    gain_cur, gain_prev = _change_pairs(10, "gain", 1000)

    with patch.object(MODULE, "tags", side_effect=[{**lost_cur, **gain_cur}, {**lost_prev, **gain_prev}]):
        message = MODULE.promo_message(current_path, previous_path, limit=400)

    tail = message.splitlines()[-1]
    assert tail.startswith("（未列示"), tail
    assert "撤销" in tail and "新增" in tail
    assert "完整明细见同日 XLSX" in tail


def test_split_plaintext_keeps_complete_lines() -> None:
    text = "标题\n" + "\n".join(f"• 产品{i}｜HK｜8% → 5%" for i in range(20))
    chunks = MODULE.split_plaintext(text, limit=80)
    assert len(chunks) > 1
    assert all(len(chunk) <= 80 for chunk in chunks)
    assert "\n".join(chunks) == text


def test_legacy_vendor_placeholder_never_reaches_the_report(tmp_path: Path) -> None:
    """A stale baseline workbook must not turn 厂商 into a fake vendor name."""
    current_path = tmp_path / "260818-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "260817-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    current = {("100", "HK"): ("預訂指定套票減50%", "哈薩克斯坦 eSIM", "BC")}
    # Yesterday's workbook still carries the Go CLI's unresolved placeholder.
    previous = {("100", "HK"): ("預訂指定套票減8%", "哈薩克斯坦 eSIM", "手动输入")}

    with patch.object(MODULE, "tags", side_effect=[current, previous]):
        message = MODULE.promo_message(current_path, previous_path)

    assert "手动输入" not in message
    assert "手動輸入" not in message
    assert "100｜B｜H｜8→50" in message


def test_deployed_delivery_script_matches_project_copy() -> None:
    """The collector runs the deployed copy; drift would silently drop the fixes."""
    deployed = Path.home() / ".hermes" / "scripts" / "trip_mode2_direct_delivery.py"
    if not deployed.is_file():
        return
    assert deployed.read_bytes() == SCRIPT.read_bytes(), (
        "deployed ~/.hermes/scripts/trip_mode2_direct_delivery.py is out of sync with "
        "scripts/hermes-cron/trip_mode2_direct_delivery.py; copy the project file over it"
    )
