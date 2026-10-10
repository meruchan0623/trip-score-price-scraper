from __future__ import annotations

import importlib.util
import os
from datetime import datetime as real_datetime
from pathlib import Path
from unittest.mock import patch

import pytest


SCRIPT = Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_mode2_direct_delivery.py"
SPEC = importlib.util.spec_from_file_location("trip_mode2_direct_delivery", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
MODULE.TARGET = "telegram:offline_test_chat"


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
        messages = MODULE.promo_messages(current_path, previous_path)

    assert len(messages) == 1
    message = messages[0]
    assert len(message) <= MODULE.MESSAGE_LIMIT <= 4096
    assert not message.startswith("（1/")
    assert "变化 1 产品／1 处：撤销 0｜力度变化 1｜新增 0" in message
    # Product 100 collapses to one grouped row under the exact vendor name:
    # slash-separated two-letter sites, discount codes, then the product IDs.
    assert "▼力度变化 1 处／1 组" in message
    assert "\n▪ TSIM 網址\nHK/RU｜8+50→5+50｜100\n" in message
    assert "200" not in message
    # Site/vendor tallies use literal names; there is no abbreviation legend.
    assert "站点 HK 1｜RU 1" in message
    assert "厂商 TSIM 網址 1" in message
    assert "缩写" not in message and "=" not in message
    assert "▪ S\n" not in message and "｜HR｜" not in message
    assert MODULE.ROW_FORMAT in message
    # Localized promotion wording stays in the workbook and never floods the message.
    assert "預訂" not in message and "скидка" not in message
    assert "未列示" not in message


def _group_lines(messages: list[str]) -> list[str]:
    """Return only real data group rows: `HK/VN/RU｜旧→新｜pid,pid,…`.

    Explanatory lines such as ROW_FORMAT also contain `｜` and `→`, so a row
    is only accepted when its two-letter sites and product IDs are well-formed.
    """
    lines: list[str] = []
    for message in messages:
        for line in message.splitlines():
            parts = line.split("｜")
            if len(parts) != 3 or "→" not in parts[1]:
                continue
            sites, _transition, pids = parts
            if not sites or any(site not in MODULE.SITES for site in sites.split("/")):
                continue
            if not pids or not all(pid.isdigit() for pid in pids.split(",")):
                continue
            lines.append(line)
    return lines


def _vendor_headings(messages: list[str]) -> list[str]:
    return [
        line[len(MODULE.VENDOR_PREFIX):]
        for message in messages
        for line in message.splitlines()
        if line.startswith(MODULE.VENDOR_PREFIX)
    ]


def _all_pids(messages: list[str]) -> set[str]:
    pids: set[str] = set()
    for line in _group_lines(messages):
        pids.update(line.split("｜")[2].split(","))
    return pids


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


def _grouped_change_pairs(groups: int, per_group: int, kind: str, offset: int = 0) -> tuple[dict, dict]:
    """Synthesize `groups` distinct compact groups of `per_group` products each.

    Groups differ by discount value (1..groups) and by the site subset, so the
    renderer cannot merge them further.
    """
    sites = ["HK", "TW", "JP", "TH", "VN", "RU"]
    current: dict = {}
    previous: dict = {}
    for index in range(groups):
        pct = index + 1
        subset = sites[: 1 + index % 6]
        for member in range(per_group):
            pid = str(700000000 + offset + index * per_group + member)
            for site in subset:
                if kind == "lost":
                    previous[(pid, site)] = (f"預訂指定套票減{pct}%", "商品", "BC")
                    current[(pid, site)] = ("無促銷", "商品", "BC")
                else:
                    current[(pid, site)] = (f"預訂指定套票減{pct}%", "商品", "BC")
                    previous[(pid, site)] = ("無促銷", "商品", "BC")
    return current, previous


def test_big_change_day_renders_exactly_two_complete_messages(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    gain_cur, gain_prev = _grouped_change_pairs(100, 3, "gain")
    lost_cur, lost_prev = _grouped_change_pairs(30, 3, "lost", 100000)
    current = {**gain_cur, **lost_cur}
    previous = {**gain_prev, **lost_prev}
    expected_pids = {pid for pid, _site in current}

    with patch.object(MODULE, "tags", side_effect=[current, previous]):
        messages = MODULE.promo_messages(current_path, previous_path)

    assert len(messages) == 2
    assert all(len(message) <= MODULE.MESSAGE_LIMIT <= 4096 for message in messages)
    assert messages[0].startswith("（1/2）\n📊 携程 Mode2")
    assert messages[1].startswith("（2/2）\n📊 携程 Mode2") and "（续）" in messages[1].splitlines()[1]
    # Both messages are self-describing: the row-format header on each, and
    # the vendor heading is repeated on the continuation message.
    assert all(MODULE.ROW_FORMAT in message for message in messages)
    assert all("▪ BC\n" in message for message in messages)
    assert set(_vendor_headings(messages)) == {"BC"}
    # Everything fits in two messages, so nothing is omitted and every group /
    # product ID is present with full site codes and discount codes.
    assert all("未列示" not in message for message in messages)
    assert _all_pids(messages) == expected_pids
    assert "▼撤销折扣 90 处／30 组" in messages[0]
    assert "▼新增折扣 300 处／100 组" in "\n".join(messages)
    assert "\nHK/TW/JP/TH/VN/RU｜6→-｜" in messages[0]
    assert "HTJAVR" not in "\n".join(messages)
    # The split happens at a group boundary: no group line is cut in half.
    for message in messages:
        for line in message.splitlines():
            if "｜" in line and "→" in line and not line.startswith("行格式"):
                assert line.count("｜") == 2, line


def test_priority_order_keeps_lost_promotions_before_new_ones(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    lost_cur, lost_prev = _change_pairs(2, "lost", 0)
    gain_cur, gain_prev = _change_pairs(2, "gain", 1000)
    shift_cur, shift_prev = _change_pairs(2, "shift", 2000)

    with patch.object(
        MODULE,
        "tags",
        side_effect=[{**gain_cur, **shift_cur, **lost_cur}, {**gain_prev, **shift_prev, **lost_prev}],
    ):
        messages = MODULE.promo_messages(current_path, previous_path)

    assert len(messages) == 1
    body = messages[0].splitlines()
    lost_index = next(i for i, line in enumerate(body) if line.startswith("▼撤销折扣"))
    shift_index = next(i for i, line in enumerate(body) if line.startswith("▼力度变化"))
    gain_index = next(i for i, line in enumerate(body) if line.startswith("▼新增折扣"))
    assert lost_index < shift_index < gain_index
    # 撤销 rows are inside the 撤销 section, before the 力度变化 section starts.
    assert body[lost_index + 1 : shift_index] == ["▪ BC", "HK/TW/JP/TH/VN/RU｜5+50→-｜70000000,70000001"]
    assert body[shift_index + 1 : gain_index] == ["▪ BC", "HK/TW/JP/TH/VN/RU｜8+50→5+50｜70002000,70002001"]
    assert body[gain_index + 1 : gain_index + 3] == ["▪ BC", "HK/TW/JP/TH/VN/RU｜-→5+50｜70001000,70001001"]


def test_overflow_day_stops_at_two_messages_with_exact_omission_tail(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    gain_cur, gain_prev = _grouped_change_pairs(400, 3, "gain")
    lost_cur, lost_prev = _grouped_change_pairs(30, 3, "lost", 100000)
    current = {**gain_cur, **lost_cur}
    previous = {**gain_prev, **lost_prev}
    all_pids = {pid for pid, _site in current}

    with patch.object(MODULE, "tags", side_effect=[current, previous]):
        messages = MODULE.promo_messages(current_path, previous_path)

    assert len(messages) == 2
    assert all(len(message) <= MODULE.MESSAGE_LIMIT <= 4096 for message in messages)
    # The actionable section is complete and listed first; the bulk one overflows.
    assert "▼撤销折扣 90 处／30 组" in messages[0]
    assert "未列示" not in messages[0]
    shown = _all_pids(messages)
    assert {pid for pid, _site in lost_cur} <= shown
    omitted_pids = all_pids - shown
    omitted_groups = 430 - len(_group_lines(messages))
    assert omitted_groups > 0
    tail = messages[1].splitlines()[-1]
    assert tail == (
        f"（未列示 {omitted_groups} 组／{len(omitted_pids)} 个产品ID：新增 {omitted_groups} 组；"
        "完整明细见同日 XLSX 的「总促销标签」标红行）"
    )


def test_omission_tail_counts_every_unlisted_section(tmp_path: Path) -> None:
    current_path = tmp_path / "a" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "b" / "trip_mode2_daily_6site.xlsx"
    lost_cur, lost_prev = _grouped_change_pairs(10, 1, "lost", 0)
    gain_cur, gain_prev = _grouped_change_pairs(10, 1, "gain", 1000)

    with patch.object(MODULE, "tags", side_effect=[{**lost_cur, **gain_cur}, {**lost_prev, **gain_prev}]):
        messages = MODULE.promo_messages(current_path, previous_path, limit=420)

    assert len(messages) == 2
    assert all(len(message) <= 420 for message in messages)
    tail = messages[1].splitlines()[-1]
    assert tail.startswith("（未列示"), tail
    assert "撤销" in tail and "新增" in tail
    assert "完整明细见同日 XLSX" in tail


def test_main_sends_each_rendered_message_without_chunking(tmp_path: Path) -> None:
    run_dir = tmp_path / "260920-mode2-daily"
    run_dir.mkdir()
    (run_dir / ".collection_done").write_text("{}")
    (run_dir / ".delivery_done").write_text("{}")
    (run_dir / "trip_mode2_daily_6site.xlsx").write_bytes(b"")
    baseline = tmp_path / "260919-mode2-daily"
    baseline.mkdir()
    (baseline / "trip_mode2_daily_6site.xlsx").write_bytes(b"")
    sent: list[str] = []

    class FakeNow:
        @staticmethod
        def now():
            # Must build a real datetime: MODULE.datetime is patched to this
            # class, so referring to it here would recurse into FakeNow(...).
            return real_datetime(2026, 9, 20, 4, 0, 0)

    with patch.object(MODULE, "ROOT", tmp_path), patch.object(MODULE, "datetime", FakeNow), patch.object(
        MODULE, "promo_messages", return_value=["（1/2）\nfirst", "（2/2）\nsecond"]
    ), patch.object(MODULE, "send", side_effect=sent.append), patch.object(
        MODULE, "split_plaintext", side_effect=AssertionError("chunk splitter must not be used")
    ):
        assert MODULE.main() == 0

    assert sent == ["（1/2）\nfirst", "（2/2）\nsecond"]
    assert (run_dir / ".promo_change_reported").exists()


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
        (message,) = MODULE.promo_messages(current_path, previous_path)

    assert "手动输入" not in message
    assert "手動輸入" not in message
    assert "\n▪ BC\nHK｜8→50｜100" in message
    assert "手动输入" not in _vendor_headings([message])


def test_multi_vendor_sites_render_exact_names_and_slash_sites(tmp_path: Path) -> None:
    """Vendor headings are the workbook names; sites are `HK/VN/RU`, never `HVR`."""
    current_path = tmp_path / "260818-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "260817-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    current = {
        ("100", "HK"): ("減5%", "商品", "TSIM 網址"),
        ("100", "VN"): ("減5%", "商品", "TSIM 網址"),
        ("100", "RU"): ("減5%", "商品", "TSIM 網址"),
        ("300", "TW"): ("減5%", "商品", "BC"),
        ("300", "TH"): ("減5%", "商品", "TGT"),
        ("400", "JP"): ("減5%", "商品", "其他"),
    }
    previous = {key: ("無促銷", "商品", value[2]) for key, value in current.items()}

    with patch.object(MODULE, "tags", side_effect=[current, previous]):
        (message,) = MODULE.promo_messages(current_path, previous_path)

    assert _vendor_headings([message]) == ["BC/TGT", "TSIM 網址", "其他"]
    assert "\n▪ BC/TGT\nTW/TH｜-→5｜300\n" in message
    assert "\n▪ TSIM 網址\nHK/VN/RU｜-→5｜100\n" in message
    assert "\n▪ 其他\nJP｜-→5｜400\n" in message
    assert "厂商 BC/TGT 1｜TSIM 網址 1｜其他 1" in message
    assert "站点 HK 1｜TW 1｜JP 1｜TH 1｜VN 1｜RU 1" in message
    for abbreviated in ("▪ S\n", "▪ BG\n", "▪ O\n", "｜HVR｜", "｜TA｜", "｜J｜"):
        assert abbreviated not in message


def test_deployed_delivery_script_matches_project_copy() -> None:
    """The collector runs the deployed copy; drift would silently drop the fixes."""
    deployed = Path.home() / ".hermes" / "scripts" / "trip_mode2_direct_delivery.py"
    if not deployed.is_file():
        return
    assert deployed.read_bytes() == SCRIPT.read_bytes(), (
        "deployed ~/.hermes/scripts/trip_mode2_direct_delivery.py is out of sync with "
        "scripts/hermes-cron/trip_mode2_direct_delivery.py; copy the project file over it"
    )


def test_missing_target_fails_before_delivery(monkeypatch):
    monkeypatch.setattr(MODULE, "TARGET", "")
    with pytest.raises(SystemExit, match="TRIP_DELIVERY_TARGET"):
        MODULE.main()
