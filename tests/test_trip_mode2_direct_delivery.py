from __future__ import annotations

import importlib.util
import hashlib
import json
import os
from datetime import datetime
from pathlib import Path

import openpyxl
import pytest

SCRIPT = Path(__file__).parents[1] / "scripts/hermes-cron/trip_mode2_direct_delivery.py"
spec = importlib.util.spec_from_file_location("delivery", SCRIPT)
MODULE = importlib.util.module_from_spec(spec)
spec.loader.exec_module(MODULE)


class Today:
    @classmethod
    def now(cls):
        return datetime(2026, 1, 2)


class Tomorrow:
    @classmethod
    def now(cls):
        return datetime(2026, 1, 3)


HEADERS = ["厂商", "商品ID", "URL", "商品名称", "商品资讯", "抓取备注", "总促销标签", "评分"]


def target_book(path):
    workbook = openpyxl.Workbook()
    sheet = workbook.active
    sheet.append(["目的地", "TSIM网址", "BC", "TGT", "FR", "JY", "其他"])
    sheet.append(["Paris", "100", "200", "", "", "", ""])
    workbook.save(path)
    return path


def source_book(path, bc_promo="20%", ids=("100", "200")):
    workbook = openpyxl.Workbook()
    workbook.remove(workbook.active)
    for site in ("HK", "RU"):
        sheet = workbook.create_sheet(site)
        sheet.append(HEADERS)
        sheet.append(["TSIM", ids[0], "https://tsim/100", "TSIM Paris", "ok", "", "10%", "4.8"])
        sheet.append(["BC", ids[1], "https://bc/200", "BC Paris", "ok", "", bc_promo, "4.7"])
    workbook.save(path)
    return path


def source_book_without_notes(path, bc_promo="20%", ids=("100", "200")):
    book = source_book(path, bc_promo, ids)
    wb = openpyxl.load_workbook(book)
    for sheet in wb.worksheets:
        sheet.delete_cols(6)
    wb.save(book)
    return book


def setup_run(tmp_path, current_promo="20%", previous=True, current_name="260102-0300_trip_mode2_daily_2site.xlsx"):
    run = tmp_path / "260102-mode2-daily"
    run.mkdir()
    current = source_book(run / current_name, current_promo)
    target = target_book(tmp_path / "target.xlsx")
    marker = {"sites": "hk,ru", "scope": "hk_ru_2site", "output_path": str(current), "url_count": 2, "target_snapshot": str(target), "cron_tick": "2026-01-02T04:00:00"}
    (run / ".collection_done").write_text(json.dumps(marker))
    if previous:
        old = tmp_path / "260101-mode2-daily"
        old.mkdir()
        source_book(old / "old_trip_mode2_daily_6site.xlsx", "10%")
    return run, current, target


def transport(monkeypatch, deliveries, messages):
    monkeypatch.setattr(MODULE, "deliver", lambda path, subject: deliveries.append(path) or {"receipt_path": "test-attachment", "receipt": {"status": "delivered"}})
    monkeypatch.setattr(MODULE, "send", lambda text: messages.append(text) or f"test-message-{len(messages)}")


def configure(monkeypatch, tmp_path):
    monkeypatch.setattr(MODULE, "ROOT", tmp_path)
    monkeypatch.setattr(MODULE, "datetime", Today)


def test_real_main_builds_report_once_and_repeat_sends_nothing(tmp_path, monkeypatch):
    run, _, _ = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main(["--date", "260102", "--root", str(tmp_path)]) == 0
    assert len(deliveries) == 1 and len(messages) == 1


def test_historical_retry_after_midnight_reuses_run_day_report_and_sends_nothing(tmp_path, monkeypatch):
    run, _, _ = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0
    report = next(run.glob("260102-*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_20260102-0300.xlsx"))
    monkeypatch.setattr(MODULE, "datetime", Tomorrow)
    assert MODULE.main(["--date", "260102", "--root", str(tmp_path)]) == 0
    assert deliveries == [report] and len(messages) == 1
    assert not list(run.glob("260103-*-promo-replay"))
    report = next(run.glob("*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_20260102-0300.xlsx"))
    assert report.is_file()
    assert deliveries == [report]
    assert (run / ".promo_change_reported").is_file()
    receipt = json.loads((run / ".delivery_done").read_text())
    assert receipt["messages"] == {"0": "test-message-1"}
    assert MODULE.main(["--date", "260102", "--root", str(tmp_path)]) == 0
    assert len(deliveries) == 1 and len(messages) == 1


def test_no_baseline_builds_real_pending_report_not_additions(tmp_path, monkeypatch):
    run, _, _ = setup_run(tmp_path, previous=False)
    configure(monkeypatch, tmp_path)
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0
    report = next(run.glob("*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_20260102-0300.xlsx"))
    audit = json.loads((report.parent / "audit.json").read_text())
    assert audit["baseline_date"] == "missing"
    assert audit["pending_reasons"]["missing_baseline_not_addition"] == 2
    assert audit["event_count"] == 0
    assert report.is_file() and deliveries == [report]


def test_native_workbook_without_optional_notes_delivers(tmp_path, monkeypatch):
    run, current, _ = setup_run(tmp_path)
    source_book_without_notes(current)
    configure(monkeypatch, tmp_path)
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0
    assert len(deliveries) == 1 and len(messages) == 1


def test_nonempty_optional_notes_remain_quarantined(tmp_path, monkeypatch):
    run, current, _ = setup_run(tmp_path)
    wb = openpyxl.load_workbook(current)
    for sheet in wb.worksheets:
        sheet.cell(2, HEADERS.index("抓取备注") + 1).value = "timeout"
    wb.save(current)
    configure(monkeypatch, tmp_path)
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0
    report = next(run.glob("*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_*.xlsx"))
    audit = json.loads((report.parent / "audit.json").read_text())
    assert audit["pending_reasons"]["current_nonempty_failure_note"] == 2


def test_attachment_failure_pending_then_retry_reuses_real_report(tmp_path, monkeypatch):
    run, _, _ = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    monkeypatch.setattr(MODULE, "deliver", lambda *_: (_ for _ in ()).throw(RuntimeError("test attachment failure")))
    with pytest.raises(SystemExit):
        MODULE.main()
    report = next(run.glob("*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_20260102-0300.xlsx"))
    assert (run / ".pending_delivery.json").is_file() and not (run / ".promo_change_reported").exists()
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0
    assert deliveries == [report] and len(messages) == 1
    assert not (run / ".pending_delivery.json").exists()


def test_second_plaintext_retry_does_not_resend_first_or_attachment(tmp_path, monkeypatch):
    run, _, _ = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    real_module = MODULE.report_module()

    class RealBuilder:
        load_targets = staticmethod(real_module.load_targets)

        def main(self, arguments):
            real_module.main(arguments)
            output = Path(arguments[arguments.index("--output-dir") + 1])
            (output / "messages.txt").write_text("one\n\n--- Telegram message ---\n\ntwo\n")

    monkeypatch.setattr(MODULE, "report_module", lambda: RealBuilder())
    deliveries, sent = [], []
    monkeypatch.setattr(MODULE, "deliver", lambda path, subject: deliveries.append(path) or {"receipt_path": "test", "receipt": {}})
    def fail_second(text):
        sent.append(text)
        if text == "two":
            raise RuntimeError("test message two failure")
        return "test-message-1"
    monkeypatch.setattr(MODULE, "send", fail_second)
    with pytest.raises(SystemExit):
        MODULE.main()
    assert deliveries and sent == ["one", "two"]
    monkeypatch.setattr(MODULE, "send", lambda text: sent.append(text) or "test-message-2")
    assert MODULE.main() == 0
    assert len(deliveries) == 1 and sent == ["one", "two", "two"]


def test_marker_selection_beats_newer_unrelated_workbook(tmp_path):
    run = tmp_path / "run"
    run.mkdir()
    exact = source_book(run / "exact_trip_mode2_daily_2site.xlsx")
    source_book(run / "zzz_trip_mode2_daily_2site.xlsx")
    assert MODULE.marker_output(run, {"sites": "hk,ru", "scope": MODULE.SCOPE, "output_path": str(exact)}) == exact


def test_target_mismatch_rejects_before_any_transport(tmp_path, monkeypatch):
    _, current, _ = setup_run(tmp_path)
    source_book(current, ids=("100", "999"))
    configure(monkeypatch, tmp_path)
    monkeypatch.setattr(MODULE, "deliver", lambda *_: pytest.fail("attachment transport called"))
    monkeypatch.setattr(MODULE, "send", lambda *_: pytest.fail("message transport called"))
    with pytest.raises(SystemExit, match="mismatches frozen target"):
        MODULE.main()


def test_old_six_site_receipt_cannot_suppress_new_source_and_changed_source_resends(tmp_path, monkeypatch):
    run, current, _ = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    (run / ".delivery_done").write_text(json.dumps({"scope": MODULE.SCOPE, "source_path": "old6", "source_sha256": "old", "report_path": "old", "target": MODULE.TARGET, "artifacts": {"report_xlsx": "old"}, "messages": {"0": "old"}}))
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0 and len(deliveries) == 1
    source_book(current, "30%")
    assert MODULE.main() == 0
    assert len(deliveries) == 2 and len(messages) == 2
    assert len(list(run.glob("*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_20260102-0300.xlsx"))) == 2


def test_retry_keeps_source_start_filename_and_raw_workbook_unchanged(tmp_path, monkeypatch):
    run, current, _ = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    raw_hash = MODULE.digest(current)
    monkeypatch.setattr(MODULE, "deliver", lambda *_: (_ for _ in ()).throw(RuntimeError("attachment failure")))
    with pytest.raises(SystemExit):
        MODULE.main()
    report = next(run.glob("*-promo-replay/Trip_Mode2_HK-RU_竞品促销报告_20260102-0300.xlsx"))
    assert MODULE.digest(current) == raw_hash
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main() == 0
    assert deliveries == [report]
    assert MODULE.main() == 0
    assert deliveries == [report] and MODULE.digest(current) == raw_hash


def test_invalid_source_name_uses_valid_cron_tick_then_stable_file_timestamp(tmp_path, monkeypatch):
    run, current, _ = setup_run(tmp_path, current_name="legacy_trip_mode2_daily_2site.xlsx")
    marker = json.loads((run / ".collection_done").read_text())
    assert MODULE.report_stamp(current, marker) == "20260102-0400"
    marker["cron_tick"] = "not-a-time"
    source_mtime = datetime(2026, 1, 2, 5, 6, tzinfo=MODULE.SHANGHAI).timestamp()
    os.utime(current, (source_mtime, source_mtime))
    assert MODULE.report_stamp(current, marker) == "20260102-0506"


def test_legacy_generic_report_and_success_receipt_are_not_renamed_or_resent(tmp_path, monkeypatch):
    run, current, target = setup_run(tmp_path)
    configure(monkeypatch, tmp_path)
    source_hash = hashlib.sha256("|".join(MODULE.digest(path) for path in (current, target, tmp_path / "260101-mode2-daily" / "old_trip_mode2_daily_6site.xlsx")).encode()).hexdigest()
    report_dir = run / f"260102-{source_hash[:16]}-promo-replay"
    report_dir.mkdir()
    legacy = report_dir / "report.xlsx"
    legacy.write_bytes(b"legacy report")
    (report_dir / "messages.txt").write_text("")
    (run / ".delivery_done").write_text(json.dumps({"scope": MODULE.SCOPE, "source_path": str(current), "source_sha256": source_hash, "report_path": str(legacy), "target": MODULE.TARGET, "artifacts": {"report_xlsx": "old"}, "messages": {}}))
    monkeypatch.setattr(MODULE, "deliver", lambda *_: pytest.fail("legacy receipt must not resend"))
    monkeypatch.setattr(MODULE, "send", lambda *_: pytest.fail("legacy receipt must not resend"))
    assert MODULE.main() == 0
    assert legacy.is_file() and not list(report_dir.glob("Trip_Mode2_HK-RU_竞品促销报告_*.xlsx"))


@pytest.mark.parametrize("bad", [1, "", " 100", "100 ", "10A"])
def test_strict_rejects_numeric_blank_whitespace_and_nondigit_ids(tmp_path, bad):
    path = source_book(tmp_path / "bad.xlsx")
    workbook = openpyxl.load_workbook(path)
    for site in ("HK", "RU"):
        workbook[site].cell(2, 2).value = bad
    workbook.save(path)
    with pytest.raises(ValueError, match="malformed"):
        MODULE.strict_hk_ru(path, target_ids={"100", "200"})


def test_send_uses_json_success_and_message_id(monkeypatch):
    class Result:
        returncode = 0
        stderr = ""
        stdout = '{"success": true, "message_id": "receipt-7"}'
    seen = {}
    monkeypatch.setattr(MODULE.subprocess, "run", lambda command, **_: seen.setdefault("command", command) and Result())
    assert MODULE.send("hello") == "receipt-7"
    assert seen["command"][:3] == ["hermes", "send", "--json"]

def test_explicit_historical_date_selects_run_without_clock_override(tmp_path, monkeypatch):
    run, _, _ = setup_run(tmp_path)
    historical = tmp_path / "261019-mode2-daily"; run.rename(historical)
    marker=json.loads((historical / ".collection_done").read_text()); marker["output_path"]=str(historical / Path(marker["output_path"]).name); (historical / ".collection_done").write_text(json.dumps(marker))
    configure(monkeypatch, tmp_path)
    deliveries, messages = [], []
    transport(monkeypatch, deliveries, messages)
    assert MODULE.main(["--date", "261019", "--root", str(tmp_path)]) == 0
    assert deliveries and messages

def test_rejects_run_dir_outside_root(tmp_path):
    with pytest.raises(SystemExit):
        MODULE.selected_run(["--run-dir", str(tmp_path / "escape"), "--root", str(tmp_path)])


def test_deployed_delivery_script_matches_project_copy():
    deployed = Path.home() / ".hermes/scripts/trip_mode2_direct_delivery.py"
    if deployed.is_file():
        assert deployed.read_bytes() == SCRIPT.read_bytes()
