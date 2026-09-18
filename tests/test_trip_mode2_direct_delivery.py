from __future__ import annotations

import importlib.util
from pathlib import Path
from unittest.mock import patch


SCRIPT = Path(__file__).parents[1] / "scripts" / "hermes-cron" / "trip_mode2_direct_delivery.py"
SPEC = importlib.util.spec_from_file_location("trip_mode2_direct_delivery", SCRIPT)
assert SPEC and SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def test_promo_message_lists_changed_product_and_merged_sites_in_plaintext(tmp_path: Path) -> None:
    current_path = tmp_path / "260818-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    previous_path = tmp_path / "260817-mode2-daily" / "trip_mode2_daily_6site.xlsx"
    current = {
        ("100", "HK"): ("預訂指定套票減50% | 預訂指定套票減5%", "哈薩克斯坦 eSIM｜1-30天｜QR code", "TSIM 網點"),
        ("100", "RU"): ("скидка 50 % | скидка 5 %", "Казахстан eSIM | QR", "TSIM 網點"),
        ("200", "HK"): ("指定產品減20%", "文案变化产品", "BC"),
    }
    previous = {
        ("100", "HK"): ("預訂指定套票減50% | 預訂指定套票減8%", "哈薩克斯坦 eSIM | old", "TSIM 網點"),
        ("100", "RU"): ("скидка 50 % | скидка 8 %", "Казахстан eSIM | old", "TSIM 網點"),
        # Different wording, identical percentage: must not be reported.
        ("200", "HK"): ("預訂指定套票省20%", "文案变化产品"),
    }

    with patch.object(MODULE, "tags", side_effect=[current, previous]):
        message = MODULE.promo_message(current_path, previous_path)

    assert "变化产品：1 个" in message
    assert "• 100｜哈薩克斯坦 eSIM｜TSIM 網點｜HK/RU｜5% + 50% → 5% + 50%" not in message
    assert "• 100｜哈薩克斯坦 eSIM｜TSIM 網點｜HK/RU｜8% + 50% → 5% + 50%" in message
    assert "200" not in message
    assert "|---" not in message


def _run_main(tmp_path: Path, monkeypatch, env_force: str | None) -> tuple[list, list]:
    import json
    import subprocess
    from datetime import datetime

    today = datetime.now().strftime("%y%m%d")
    run_dir = tmp_path / f"{today}-mode2-daily"
    run_dir.mkdir()
    (run_dir / f"{today}-0300_trip_mode2_daily_6site.xlsx").write_bytes(b"x")
    (run_dir / ".collection_done").write_text(json.dumps({"processed_count": 966}))
    (run_dir / ".delivery_done").write_text("{}")
    (run_dir / ".promo_change_reported").write_text("{}")
    if env_force is None:
        monkeypatch.delenv("MODE2_FORCE_DELIVERY", raising=False)
    else:
        monkeypatch.setenv("MODE2_FORCE_DELIVERY", env_force)

    runs: list = []
    sends: list = []
    monkeypatch.setattr(MODULE, "ROOT", tmp_path)
    monkeypatch.setattr(subprocess, "run", lambda *a, **k: runs.append(a) or type("R", (), {"returncode": 0, "stdout": "", "stderr": ""})())
    monkeypatch.setattr(MODULE, "send", lambda text: sends.append(text))
    monkeypatch.setattr(MODULE, "promo_message", lambda today, previous: "msg")
    assert MODULE.main() == 0
    return runs, sends


def test_main_respects_markers_by_default(tmp_path: Path, monkeypatch) -> None:
    runs, sends = _run_main(tmp_path, monkeypatch, None)
    assert runs == [] and sends == []


def test_main_force_flag_redelivers_and_reports(tmp_path: Path, monkeypatch) -> None:
    from datetime import datetime, timedelta

    yesterday = (datetime.now() - timedelta(days=1)).strftime("%y%m%d")
    prev = tmp_path / f"{yesterday}-mode2-daily"
    prev.mkdir()
    (prev / f"{yesterday}-0300_trip_mode2_daily_6site.xlsx").write_bytes(b"y")
    runs, sends = _run_main(tmp_path, monkeypatch, "1")
    assert len(runs) == 1  # trip_deliver.py invoked once (mocked, no real send)
    assert sends == ["msg"]


def test_split_plaintext_keeps_complete_lines() -> None:
    text = "标题\n" + "\n".join(f"• 产品{i}｜HK｜8% → 5%" for i in range(20))
    chunks = MODULE.split_plaintext(text, limit=80)
    assert len(chunks) > 1
    assert all(len(chunk) <= 80 for chunk in chunks)
    assert "\n".join(chunks) == text
