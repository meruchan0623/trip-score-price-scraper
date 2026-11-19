from __future__ import annotations

import hashlib
import importlib.util
import json
from pathlib import Path

import openpyxl
import pytest

ROOT = Path(__file__).parents[1]
SPEC = importlib.util.spec_from_file_location("mode2_resume", ROOT / "scripts/hermes-cron/trip_mode2_resume.py")
assert SPEC and SPEC.loader
MOD = importlib.util.module_from_spec(SPEC); SPEC.loader.exec_module(MOD)


def frozen(tmp_path: Path) -> tuple[Path, Path, Path]:
    run = tmp_path / "261021-mode2-daily"; target = run / "target"; target.mkdir(parents=True)
    book = target / "日常抓取目标.xlsx"; wb = openpyxl.Workbook(); ws = wb.active
    ws.append(["TSIM 網址", "BC", "TGT", "FR", "JY", "其他"]); ws.append(["100", None, None, None, None, None]); ws.append(["200", None, None, None, None, None]); wb.save(book); wb.close()
    digest = hashlib.sha256(book.read_bytes()).hexdigest()
    (target / "provenance.json").write_text(json.dumps({"sha256": digest, "product_ids": ["100", "200"], "sites": ["hk", "ru"], "url_count": 2, "site_count": 2, "expected_rows": 4, "staged_workbook": str(book)}))
    urls, env = run / "mode2_target_urls.txt", run / "mode2_target.env"
    assert MOD.main(["freeze", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--workers", "1", "--start-stamp", "261021-0101"]) == 0
    return run, urls, env


def worker(base: Path, ids=("100", "200"), *, success=True) -> None:
    path = base / "w1"; path.mkdir(parents=True); wb = openpyxl.Workbook(); wb.remove(wb.active)
    for site in ("HK", "RU"):
        ws = wb.create_sheet(site); ws.append(["商品ID"])
        for value in ids: ws.append([value])
    wb.save(path / "trip_mode2_w1.xlsx"); wb.close()
    (path / "terminal_output.txt").write_text(json.dumps({"event": "run_done", "success": success, "processed_count": len(ids) * 2}) + "\n")


def test_complete_workers_resume_without_target_download_or_worker_rewrite(tmp_path: Path) -> None:
    run, urls, env = frozen(tmp_path); worker(run)
    before = (run / "w1/trip_mode2_w1.xlsx").read_bytes()
    assert MOD.main(["validate-workers", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--worker-base", str(run), "--workers", "1"]) == 0
    assert json.loads((run / MOD.STATE).read_text())["collection_start_stamp"] == "261021-0101"
    assert (run / "w1/trip_mode2_w1.xlsx").read_bytes() == before


@pytest.mark.parametrize("ids", [("100",), ("100", "100"), ("100", "999")])
def test_worker_coverage_missing_duplicate_or_extra_is_rejected(tmp_path: Path, ids) -> None:
    run, urls, env = frozen(tmp_path); worker(run, ids)
    with pytest.raises(SystemExit): MOD.main(["validate-workers", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--worker-base", str(run), "--workers", "1"])


def test_tampered_frozen_snapshot_and_url_drift_are_rejected(tmp_path: Path) -> None:
    run, urls, env = frozen(tmp_path)
    urls.write_text("https://hk.trip.com/things-to-do/detail/999/\n")
    with pytest.raises(SystemExit): MOD.main(["check-frozen", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--workers", "1"])


def test_inspect_workers_keeps_valid_chunks_and_selects_only_bad_ones(tmp_path: Path) -> None:
    run, urls, env = frozen(tmp_path)
    # Two chunks make it possible to prove this is not an all-worker retry.
    (run / MOD.STATE).unlink()
    MOD.main(["freeze", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--workers", "2"])
    MOD.main(["make-chunks", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--worker-base", str(run), "--workers", "2"])
    worker(run, ("100",))
    bad = run / "w2"; bad.mkdir()
    before = (run / "w1/trip_mode2_w1.xlsx").read_bytes()
    # stdout is intentionally machine-readable for the cron wrapper.
    import contextlib, io
    output = io.StringIO()
    with contextlib.redirect_stdout(output):
        assert MOD.main(["inspect-workers", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--worker-base", str(run), "--workers", "2"]) == 0
    assert json.loads(output.getvalue()) == {"valid": [1], "retry": [2]}
    assert (run / "w1/trip_mode2_w1.xlsx").read_bytes() == before
    # Restore URL then prove the stored byte hash itself is authoritative.
    MOD.write_url_list(json.loads((run / "target/provenance.json").read_text()), urls)
    (run / "target/日常抓取目标.xlsx").write_bytes(b"tampered")
    with pytest.raises(SystemExit): MOD.main(["check-frozen", "--run-dir", str(run), "--url-file", str(urls), "--env-file", str(env), "--workers", "1"])
