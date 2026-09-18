import io
import json

import openpyxl
import pytest

from daily_mode2_urls import VENDOR_COLS, extract_product_ids
from scripts import daily_mode2_google_target_sync as sync

SITES = ("hk", "tw", "sg")


def build_workbook_bytes(rows, headers=VENDOR_COLS, sheet_name="Sheet1") -> bytes:
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.title = sheet_name
    ws.append(list(headers))
    for row in rows:
        ws.append(list(row))
    buf = io.BytesIO()
    wb.save(buf)
    return buf.getvalue()


def good_bytes():
    return build_workbook_bytes([
        ["10001", "10002", None, "x", "", "10003"],
        [10004, "10001", None, None, None, None],
    ])


def fetch_returning(data):
    calls = []

    def fetch(url, timeout):
        calls.append((url, timeout))
        return data

    fetch.calls = calls
    return fetch


@pytest.fixture
def mirror(tmp_path):
    docs = tmp_path / "docs"
    docs.mkdir()
    old = docs / "日常抓取目标.xlsx"
    old.write_bytes(build_workbook_bytes([["99999", None, None, None, None, None]]))
    return old


def test_valid_sync_atomically_replaces_mirror_and_writes_provenance(mirror):
    fetch = fetch_returning(good_bytes())
    prov = sync.sync_target(mirror, sites=SITES, fetch=fetch)

    assert fetch.calls[0][0] == sync.export_url()
    assert sync.SPREADSHEET_ID in fetch.calls[0][0] and "gid=0" in fetch.calls[0][0]
    # product-ID semantics identical to daily_mode2_urls
    assert extract_product_ids(mirror) == [10001, 10002, 10003, 10004]
    assert prov["product_ids"] == ["10001", "10002", "10003", "10004"]
    assert all(isinstance(p, str) for p in prov["product_ids"])
    assert prov["url_count"] == 4 and prov["product_count"] == 4
    assert prov["site_count"] == 3 and prov["expected_rows"] == 12
    assert prov["spreadsheet_id"] == sync.SPREADSHEET_ID and prov["gid"] == "0"
    assert prov["sha256"] == sync.sha256_of(mirror)
    assert prov["synced_at"]

    sidecar = sync.provenance_path(mirror)
    assert sidecar.parent == mirror.parent
    on_disk = json.loads(sidecar.read_text(encoding="utf-8"))
    assert on_disk["sha256"] == prov["sha256"]
    # no leftover temp files from atomic replacement
    leftovers = [p for p in mirror.parent.iterdir() if p not in (mirror, sidecar)]
    assert leftovers == []


def test_public_google_export_localized_default_sheet_is_accepted(mirror):
    payload = build_workbook_bytes(
        [[20001.0, 20002.0, None, None, None, None]],
        headers=["TSIM", "BC", "TGT", "FR", "JY", "其他"],
        sheet_name="工作表1",
    )
    prov = sync.sync_target(mirror, sites=SITES, fetch=fetch_returning(payload))
    assert extract_product_ids(mirror) == [20001, 20002]
    assert prov["product_ids"] == ["20001", "20002"]


@pytest.mark.parametrize(
    "payload",
    [
        b"",
        b"<!DOCTYPE html><html><body>Sign in</body></html>",
        b"  <html>login</html>",
        b"not a zip at all",
        pytest.param(build_workbook_bytes([["1"]], sheet_name="Other"), id="missing-sheet1"),
        pytest.param(build_workbook_bytes([["1", "2"]], headers=["TSIM 網址", "BC"]), id="missing-headers"),
        pytest.param(build_workbook_bytes([["abc", None, None, None, None, None]]), id="no-valid-ids"),
        pytest.param(build_workbook_bytes([]), id="no-rows"),
    ],
)
def test_invalid_payload_rejected_without_replacing_mirror(mirror, payload):
    before = mirror.read_bytes()
    with pytest.raises(sync.TargetSyncError):
        sync.sync_target(mirror, sites=SITES, fetch=fetch_returning(payload))
    assert mirror.read_bytes() == before
    assert extract_product_ids(mirror) == [99999]
    assert not sync.provenance_path(mirror).exists()
    assert sorted(p.name for p in mirror.parent.iterdir()) == [mirror.name]


@pytest.mark.parametrize(
    "exc",
    [
        sync.TargetSyncError("HTTP 403 from Google Sheets export"),
        ConnectionError("network down"),
        TimeoutError("timed out"),
    ],
)
def test_google_failure_rejects_without_stale_fallback(mirror, exc):
    before = mirror.read_bytes()

    def fetch(url, timeout):
        raise exc

    with pytest.raises(sync.TargetSyncError):
        sync.sync_target(mirror, sites=SITES, fetch=fetch)
    assert mirror.read_bytes() == before
    assert not sync.provenance_path(mirror).exists()


def test_cli_fails_closed_on_network_error(mirror, tmp_path, monkeypatch, capsys):
    def boom(url, timeout):
        raise sync.TargetSyncError("transport error")

    monkeypatch.setattr(sync, "default_fetch", boom)
    urls = tmp_path / "urls.txt"
    env = tmp_path / "env.sh"
    rc = sync.main(["--mirror", str(mirror), "--output-urls", str(urls), "--env-file", str(env)])
    assert rc == 1
    assert "NOT replaced" in capsys.readouterr().err
    assert not urls.exists() and not env.exists()
    assert not sync.provenance_path(mirror).exists()
    assert extract_product_ids(mirror) == [99999]


def test_stage_run_target_copies_and_refuses_completed_run(mirror, tmp_path):
    prov = sync.sync_target(mirror, sites=SITES, fetch=fetch_returning(good_bytes()))
    run_dir = tmp_path / "runs" / "260916-mode2"
    target_dir = sync.stage_run_target(run_dir, mirror, prov)

    staged = target_dir / mirror.name
    assert staged.read_bytes() == mirror.read_bytes()
    staged_prov = json.loads((target_dir / "provenance.json").read_text(encoding="utf-8"))
    assert staged_prov["sha256"] == prov["sha256"]
    assert staged_prov["url_count"] == 4
    assert staged_prov["spreadsheet_id"] == sync.SPREADSHEET_ID

    # re-staging the same target is idempotent
    sync.stage_run_target(run_dir, mirror, prov)

    # a completed run must never be overwritten
    (run_dir / "COMPLETED").write_text("done")
    with pytest.raises(sync.TargetSyncError, match="completed"):
        sync.stage_run_target(run_dir, mirror, prov)
    assert staged.read_bytes() == mirror.read_bytes()


def test_stage_run_target_refuses_different_target_in_existing_run(mirror, tmp_path):
    prov = sync.sync_target(mirror, sites=SITES, fetch=fetch_returning(good_bytes()))
    run_dir = tmp_path / "run"
    sync.stage_run_target(run_dir, mirror, prov)

    other = build_workbook_bytes([["424242", None, None, None, None, None]])
    prov2 = sync.sync_target(mirror, sites=SITES, fetch=fetch_returning(other))
    with pytest.raises(sync.TargetSyncError, match="sha256"):
        sync.stage_run_target(run_dir, mirror, prov2)
    assert json.loads((run_dir / "target" / "provenance.json").read_text())["sha256"] == prov["sha256"]


def test_dynamic_url_and_processed_counts_from_fresh_target(mirror, tmp_path, capsys):
    prov = sync.sync_target(mirror, sites=SITES, fetch=fetch_returning(good_bytes()))
    urls = tmp_path / "urls.txt"
    assert sync.write_url_list(prov, urls) == 4
    lines = urls.read_text(encoding="utf-8").splitlines()
    assert lines == [f"https://hk.trip.com/things-to-do/detail/{p}/" for p in (10001, 10002, 10003, 10004)]

    counts = sync.expected_counts(prov)
    assert counts["MODE2_EXPECTED_URLS"] == 4
    assert counts["MODE2_EXPECTED_SITES"] == 3
    assert counts["MODE2_EXPECTED_ROWS"] == 12

    env = tmp_path / "env.sh"
    sync.write_env_file(prov, env)
    text = env.read_text(encoding="utf-8")
    assert "MODE2_EXPECTED_URLS=4\n" in text
    assert "MODE2_EXPECTED_ROWS=12\n" in text
    assert f"MODE2_TARGET_SHA256={prov['sha256']}\n" in text

    # a bigger fresh target changes the derived counts (no hard-coded 161/966)
    big = build_workbook_bytes([[str(100000 + i), None, None, None, None, None] for i in range(7)])
    prov_big = sync.sync_target(mirror, sites=sync.DEFAULT_SITES, fetch=fetch_returning(big))
    counts_big = sync.expected_counts(prov_big)
    assert counts_big["MODE2_EXPECTED_URLS"] == 7
    assert counts_big["MODE2_EXPECTED_SITES"] == 6
    assert counts_big["MODE2_EXPECTED_ROWS"] == 42


def test_cli_end_to_end_offline(mirror, tmp_path, monkeypatch, capsys):
    monkeypatch.setattr(sync, "default_fetch", fetch_returning(good_bytes()))
    urls = tmp_path / "urls.txt"
    env = tmp_path / "env.sh"
    run_dir = tmp_path / "run"
    rc = sync.main([
        "--mirror", str(mirror), "--sites", "hk,tw,sg,my,en,kr",
        "--output-urls", str(urls), "--env-file", str(env), "--run-dir", str(run_dir),
    ])
    assert rc == 0
    out = json.loads(capsys.readouterr().out.strip())
    assert out["MODE2_EXPECTED_URLS"] == 4
    assert out["MODE2_EXPECTED_ROWS"] == 24
    assert "product_ids" not in out
    assert len(urls.read_text().splitlines()) == 4
    assert (run_dir / "target" / mirror.name).exists()
    assert extract_product_ids(mirror) == [10001, 10002, 10003, 10004]
