import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace


SCRIPT = Path(__file__).parents[1] / "trip_deliver.py"
spec = importlib.util.spec_from_file_location("trip_deliver", SCRIPT)
MODULE = importlib.util.module_from_spec(spec)
assert spec.loader
spec.loader.exec_module(MODULE)


def _response(**payload):
    return SimpleNamespace(returncode=0, stdout=json.dumps(payload), stderr="")


def test_telegram_delivery_requires_verified_json_receipt_and_writes_it_atomically(tmp_path, monkeypatch):
    workbook = tmp_path / "report.xlsx"
    workbook.write_bytes(b"xlsx")
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return _response(
            success=True,
            result={"platform": "telegram", "chat_id": "-1001", "thread_id": "77", "message_id": "456"},
        )

    monkeypatch.setattr(MODULE.subprocess, "run", fake_run)

    assert MODULE._deliver("telegram:-1001:77", workbook, "Daily") == 0
    assert calls == [["hermes", "send", "--json", "--to", "telegram:-1001:77", "--subject", "Daily", f"MEDIA:{workbook}"]]
    assert json.loads((tmp_path / "delivery_transport_receipt.json").read_text()) == {
        "success": True,
        "result": {"platform": "telegram", "chat_id": "-1001", "thread_id": "77", "message_id": "456"},
    }


def test_telegram_delivery_fails_closed_for_unverified_receipts(tmp_path, monkeypatch):
    workbook = tmp_path / "report.xlsx"
    workbook.write_bytes(b"xlsx")
    bad_responses = [
        "not json",
        json.dumps({"success": True, "skipped": True, "result": {"platform": "telegram", "chat_id": "1", "message_id": "2"}}),
        json.dumps({"success": True, "result": {"platform": "telegram", "chat_id": "other", "message_id": "2"}}),
        json.dumps({"success": True, "result": {"platform": "telegram", "chat_id": "1", "message_id": ""}}),
        json.dumps({"success": True, "warnings": ["media upload deferred"], "result": {"platform": "telegram", "chat_id": "1", "message_id": "2"}}),
        json.dumps({"success": True, "error": "send failed", "result": {"platform": "telegram", "chat_id": "1", "message_id": "2"}}),
    ]

    for stdout in bad_responses:
        monkeypatch.setattr(MODULE.subprocess, "run", lambda *args, stdout=stdout, **kwargs: SimpleNamespace(returncode=0, stdout=stdout, stderr=""))
        assert MODULE._deliver("telegram:1", workbook, None) == 1
        assert not (tmp_path / "delivery_transport_receipt.json").exists()


def test_telegram_nonzero_exit_and_thread_mismatch_fail_without_receipt(tmp_path, monkeypatch):
    workbook = tmp_path / "report.xlsx"
    workbook.write_bytes(b"xlsx")
    monkeypatch.setattr(MODULE.subprocess, "run", lambda *args, **kwargs: SimpleNamespace(returncode=2, stdout="", stderr="boom"))
    assert MODULE._deliver("telegram:1", workbook, None) == 1

    monkeypatch.setattr(MODULE.subprocess, "run", lambda *args, **kwargs: _response(success=True, result={"platform": "telegram", "chat_id": "1", "thread_id": "8", "message_id": "2"}))
    assert MODULE._deliver("telegram:1:7", workbook, None) == 1
    assert not (tmp_path / "delivery_transport_receipt.json").exists()


def test_nontelegram_keeps_legacy_exit_code_behavior_without_receipt(tmp_path, monkeypatch):
    workbook = tmp_path / "report.xlsx"
    workbook.write_bytes(b"xlsx")
    monkeypatch.setattr(MODULE.subprocess, "run", lambda *args, **kwargs: SimpleNamespace(returncode=0, stdout="not-json", stderr=""))

    assert MODULE._deliver("slack:channel", workbook, None) == 0
    assert not (tmp_path / "delivery_transport_receipt.json").exists()


def test_main_never_marks_unverified_telegram_send_as_delivered(tmp_path, monkeypatch):
    workbook = tmp_path / "report.xlsx"
    workbook.write_bytes(b"xlsx")
    monkeypatch.setattr(MODULE, "_stabilize_vendor_attribution", lambda path: None)
    monkeypatch.setattr(
        MODULE.subprocess,
        "run",
        lambda *args, **kwargs: _response(success=True, skipped=True, result={"platform": "telegram", "chat_id": "1", "message_id": "2"}),
    )

    assert MODULE.main([str(workbook), "--to", "telegram:1"]) == 1
    assert not (tmp_path / ".delivered.json").exists()
    assert json.loads((tmp_path / ".pending_delivery.json").read_text())["status"] == "pending"
