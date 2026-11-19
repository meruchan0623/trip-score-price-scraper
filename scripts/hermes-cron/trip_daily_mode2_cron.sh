#!/bin/bash
# trip_daily_mode2_cron.sh
# 携程日常 Mode2 多站点商品信息抓取 - 数据采集段 (no_agent)
#
# 每天凌晨 03:00 由 hermes cron 调用，执行：
#   1. 拉取最新 URL 输入文件（调用 daily_mode2_urls.py）
#   2. 将 URL 列表拆分为 5 份
#   3. 启动 5 个并行 Go CLI mode2 进程（每进程独立 Chrome）
#   4. 等待全部完成，校验数据完整性
#   5. 合并 5 个 XLSX 为一个
#   6. 写 .collection_done 标记文件供轮询分析任务检测
#
# 退出码：0=成功；非0=失败（stdout 输出错误原因）

set -euo pipefail

PROJECT_ROOT="${TRIP_PROJECT_ROOT:-$(cd "$(dirname "$0")/../.." && pwd)}"
RUN_DIR_BASE="${TRIP_DATA_ROOT:-$HOME/Documents/trip-sku-price-scrape}"
HERMES_ROOT="${TRIP_HERMES_HOME:-$HOME/.hermes}"
: "${TRIP_GOOGLE_SPREADSHEET_ID:?Set TRIP_GOOGLE_SPREADSHEET_ID}"
: "${TRIP_DELIVERY_TARGET:?Set TRIP_DELIVERY_TARGET}"
PYTHON="${TRIP_PYTHON:-python3}"
VENDOR_STABILIZER="$PROJECT_ROOT/scripts/stabilize_vendor_attribution.py"
BINARY="$PROJECT_ROOT/trip-score-price-scraper"
DAILY_XLSX="$PROJECT_ROOT/docs/日常抓取目标.xlsx"
TARGET_SYNC="$PROJECT_ROOT/scripts/daily_mode2_google_target_sync.py"
# The product CLI must use the explicitly refreshed Trip customer session.
# Never silently fall back to an old worker profile or anonymous state.
SESSION_FILE="${TRIP_CLI_SESSION_FILE:-$RUN_DIR_BASE/trip.com-cli-session.json}"
URL_FILE=""
TARGET_ENV=""
LOCK_DIR="/tmp/trip_mode2_daily.lock"
RUN_DIR_BASE="${TRIP_MODE2_ROOT:-$RUN_DIR_BASE}"
RUN_DATE="${TRIP_MODE2_DATE:-$(date +%y%m%d)}"
case "$RUN_DATE" in (??????) [[ "$RUN_DATE" =~ ^[0-9]{6}$ ]] || { echo "invalid TRIP_MODE2_DATE" >&2; exit 2; } ;; *) echo "invalid TRIP_MODE2_DATE" >&2; exit 2 ;; esac
case "$RUN_DIR_BASE" in /*) ;; *) echo "TRIP_MODE2_ROOT must be absolute" >&2; exit 2 ;; esac
RUN_NAME="${RUN_DATE}-mode2-daily"
RUN_TIMESTAMP="$(date +%y%m%d-%H%M)"
RUN_DIR="$RUN_DIR_BASE/$RUN_NAME"
DONE_MARKER="$RUN_DIR/.collection_done"
YESTERDAY_PROMO="$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_yesterday_promo.py"
FAIL_REASON_FILE="$RUN_DIR/.failure_reason"
NOTIFY_MARKER="$RUN_DIR/.failure_notified"
STATE_WRITER="$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_launcher_state.py"
RESUME_HELPER="$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_resume.py"
LOCK_GUARD="$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_lock_guard.py"
# Failure notices must be short and in Chinese (group convention) and must never
# fire for a successful run. MODE2_NOTIFY_DISABLED=1 suppresses the outbound
# message for tests while still recording the reason.
NOTIFY_TARGET="${MODE2_NOTIFY_TARGET:-$TRIP_DELIVERY_TARGET}"
mkdir -p "$RUN_DIR"

LOCK_OWNED=0
# Include entropy beyond PID/time so an old quarantined directory cannot collide
# with a very fast PID reuse on a later startup.
LOCK_TOKEN="$$-$(date +%s)-$RANDOM"
LOCK_PID="$$"
LOCK_STARTED=""
STAGE="startup"
FAIL_REASON=""
PIDS=()
LAUNCH_READY_FILE="${TRIP_MODE2_LAUNCH_READY_FILE:-}"

write_terminal_state() {
    local status="$1" code="$2" reason="$3"
    "$PYTHON" "$STATE_WRITER" --run-dir "$RUN_DIR" --status "$status" \
        --stage "$STAGE" --exit-code "$code" --reason "$reason" >/dev/null 2>&1 || true
}

release_owned_lock() {
    if [ "$LOCK_OWNED" = "1" ] && [ "$(lock_owner_value token 2>/dev/null || true)" = "$LOCK_TOKEN" ]; then
        rm -f "$LOCK_DIR/owner"
        rmdir "$LOCK_DIR" 2>/dev/null || true
    fi
}

# lstart includes seconds on macOS and Linux.  It is recorded with the PID so a
# later, unrelated process that happens to reuse the PID cannot be mistaken for
# the lock holder.  Failure to inspect a process is deliberately treated as
# "unknown", not stale: availability must never win over a live owner.
lock_process_start() {
    if [ -n "${TRIP_MODE2_LOCK_TEST_STARTED:-}" ]; then
        printf '%s\n' "$TRIP_MODE2_LOCK_TEST_STARTED"
        return 0
    fi
    /bin/ps -p "$1" -o lstart= 2>/dev/null | /usr/bin/sed 's/^[[:space:]]*//'
}

lock_owner_value() {
    /usr/bin/awk -F= -v key="$1" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$LOCK_DIR/owner"
}

acquire_lock() {
    LOCK_STARTED="$(lock_process_start "$LOCK_PID")"
    [ -n "$LOCK_STARTED" ] || return 1
    "$PYTHON" "$LOCK_GUARD" --lock-dir "$LOCK_DIR" --token "$LOCK_TOKEN" \
        --pid "$LOCK_PID" --started "$LOCK_STARTED" || return $?
    LOCK_OWNED=1
}

stage_failure_reason() {
    case "$1" in
        target_sync) printf '%s' '目标表同步失败，请查看采集日志' ;;
        build) printf '%s' '采集程序构建失败，请查看采集日志' ;;
        workers) printf '%s' '采集任务失败，请查看采集日志' ;;
        merge) printf '%s' '采集结果合并失败，请查看采集日志' ;;
        enrichment) printf '%s' '采集结果整理失败，请查看采集日志' ;;
        delivery) printf '%s' '报告投递失败，请查看采集日志' ;;
        *) printf '%s' '采集任务启动失败，请查看采集日志' ;;
    esac
}

on_signal() {
    local signal_name="$1" code="$2" pid
    trap - TERM INT
    FAIL_REASON="采集任务收到${signal_name}信号，已停止"
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    for pid in "${PIDS[@]}"; do
        wait "$pid" 2>/dev/null || true
    done
    exit "$code"
}

on_exit() {
    local code=$?
    trap - EXIT
    release_owned_lock
    if [ "$code" -eq 0 ]; then
        write_terminal_state completed 0 ""
        exit 0
    fi
    local status="collection_failed"
    [ "$STAGE" = "delivery" ] && status="delivery_failed"
    local reason="$FAIL_REASON"
    [ -n "$reason" ] || reason="$(stage_failure_reason "$STAGE")"
    write_terminal_state "$status" "$code" "$reason"
    if [ -f "$NOTIFY_MARKER" ]; then
        exit "$code"
    fi
    if [ "${MODE2_NOTIFY_DISABLED:-0}" = "1" ]; then
        echo "[cron] failure notice suppressed: $reason" >&2
        exit "$code"
    fi
    # Only a confirmed send consumes the notification retry.  A transient
    # Hermes failure leaves no marker, so the next safe invocation can retry.
    # stdout alone is ambiguous. Consume a retry only for a JSON-confirmed
    # target receipt with a non-empty message_id; never forward stderr.
    local notice
    if notice=$(/usr/bin/env hermes send --json --to "$NOTIFY_TARGET" "❌ 携程HK/RU两站${STAGE}失败：${reason}" 2>/dev/null) \
       && "$PYTHON" -c 'import json,sys; d=json.load(sys.stdin); raise SystemExit(0 if d.get("success") is True and str(d.get("message_id", "")) else 1)' <<<"$notice"; then
        touch "$NOTIFY_MARKER" 2>/dev/null || true
    fi
    exit "$code"
}
trap on_exit EXIT
trap 'on_signal TERM 143' TERM
trap 'on_signal INT 130' INT

# A detached launcher publishes the running PID before allowing this collector
# to run.  Direct cron invocations do not set this and continue immediately.
if [ -n "$LAUNCH_READY_FILE" ]; then
    launch_ready=0
    for _ in $(seq 1 300); do
        if [ -f "$LAUNCH_READY_FILE" ]; then
            rm -f "$LAUNCH_READY_FILE"
            launch_ready=1
            break
        fi
        sleep 0.1
    done
    if [ "$launch_ready" != "1" ]; then
        # A launcher that disappeared never released the collector.
        FAIL_REASON="启动器未完成状态握手"
        exit 1
    fi
fi

# Record a short failure reason for the notice, then stop.
fatal() {
    FAIL_REASON="$1"
    printf 'FATAL: %s\n' "$FAIL_REASON" >&2
    printf '%s\n' "$FAIL_REASON" > "$FAIL_REASON_FILE" 2>/dev/null || true
    exit "${2:-1}"
}

# Default production concurrency remains five workers. A verified early-risk
# abort may lower it for one manual recovery without changing the next cron.
NUM_WORKERS="${MODE2_WORKERS:-5}"
case "$NUM_WORKERS" in
    1|2|3|4|5) ;;
    *) fatal "MODE2_WORKERS must be an integer from 1 to 5" ;;
esac
# Scheduled production remains normal. Manual recovery can explicitly select
# the CLI's bounded safe profile after a verified risk-heavy normal attempt.
MODE2_DELAY_PROFILE="${MODE2_DELAY_PROFILE:-normal}"
case "$MODE2_DELAY_PROFILE" in
    normal|safe) ;;
    *) fatal "MODE2_DELAY_PROFILE must be normal or safe" ;;
esac
SITES="hk,ru"
# Manual-only: MODE2_FORCE_RERUN=1 bypasses the .collection_done skip and
# produces a fresh timestamped XLSX (existing XLSX files are never deleted).
# Never set by the cron schedule itself.
MODE2_FORCE_RERUN="${MODE2_FORCE_RERUN:-0}"
MODE2_FINALIZE_ONLY="${MODE2_FINALIZE_ONLY:-0}"

# Refuse to begin a customer-data collection without the explicitly refreshed
# session. This avoids silently resurrecting stale session.json files from a
# prior run's worker profile.
if [ ! -s "$SESSION_FILE" ]; then
    fatal "refreshed Trip CLI session is missing or empty: $SESSION_FILE" 1
fi

# =========================== 0. 防止重复运行 ===========================
# A mkdir lock protects both the shared target mirror and the five Chrome workers.
if ! acquire_lock; then
    fatal "已有采集任务运行中" 75
fi
# This process now owns the lock.  A new attempt must not inherit an old
# failure notice/reason, but a non-owner path above never touches either.
rm -f "$FAIL_REASON_FILE" "$NOTIFY_MARKER"

# Test harness only: exercise the lock protocol without touching target sync,
# browser processes, or collection artifacts.  Production cron never sets it.
if [ -n "${TRIP_MODE2_LOCK_TEST_HOLD_SECONDS:-}" ]; then
    sleep "$TRIP_MODE2_LOCK_TEST_HOLD_SECONDS"
    exit 0
fi

if [ -f "$DONE_MARKER" ]; then
    DONE_SCOPE=$("$PYTHON" -c "import json; p=json.load(open('$DONE_MARKER')); print(p.get('sites',''))" 2>/dev/null || true)
    DONE_KIND=$("$PYTHON" -c "import json; p=json.load(open('$DONE_MARKER')); print(p.get('scope',''))" 2>/dev/null || true)
    DONE_OUTPUT=$("$PYTHON" -c "import json; p=json.load(open('$DONE_MARKER')); print(p.get('output_path',''))" 2>/dev/null || true)
    if [ "$DONE_SCOPE" != "$SITES" ] || [ "$DONE_KIND" != "hk_ru_2site" ] || [[ "$DONE_OUTPUT" != *_trip_mode2_daily_2site.xlsx ]]; then
        echo "[cron] legacy six-site marker is not HK/RU completion; forcing isolated migration rerun" >&2
        MODE2_FORCE_RERUN=1
    fi
    if [ "$MODE2_FORCE_RERUN" = "1" ]; then
        echo "[cron] MODE2_FORCE_RERUN=1: ignoring existing $DONE_MARKER, rerunning collection" >&2
    else
        # Collection is immutable once marked done.  Delivery is a separate,
        # receipt-bound idempotent step: retry it here rather than scraping a
        # second time when a completed run lost its transport window.
        echo "[cron] collection already completed; resuming receipt-bound delivery" >&2
        STAGE="delivery"
        "$PYTHON" "$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_direct_delivery.py" --run-dir "$RUN_DIR" --root "$RUN_DIR_BASE" >&2
        exit 0
    fi
fi

# =========================== 1. 同步并冻结当天 Google 目标 ===========================
# The Google Sheet is the only authority. The sync helper validates a fresh XLSX,
# atomically replaces the mirror, and writes a run-local input snapshot/provenance.
# Any download/schema failure exits non-zero; stale mirrors are never used silently.
mkdir -p "$RUN_DIR"
# A forced recovery must preserve the original day's immutable target snapshot.
# Stage its fresh snapshot under the timestamped rerun subtree instead of
# overwriting the root-level evidence from a prior attempt.
TARGET_RUN_DIR="$RUN_DIR"
if [ "$MODE2_FORCE_RERUN" = "1" ]; then
    TARGET_RUN_DIR="$RUN_DIR/rerun-$RUN_TIMESTAMP"
    mkdir -p "$TARGET_RUN_DIR"
fi
URL_FILE="$TARGET_RUN_DIR/mode2_target_urls.txt"
TARGET_ENV="$TARGET_RUN_DIR/mode2_target.env"
if [ "$MODE2_FORCE_RERUN" != "1" ] && [ -f "$TARGET_RUN_DIR/target/provenance.json" ]; then
    # Same-day recovery is bound to the original bytes, schema, IDs, scope and
    # counts.  It must never touch Google or the mutable mirror.
    echo "[cron] validating immutable frozen target for same-day recovery" >&2
    STAGE="target_sync"
    "$PYTHON" "$RESUME_HELPER" check-frozen --run-dir "$TARGET_RUN_DIR" --url-file "$URL_FILE" --env-file "$TARGET_ENV" --workers "$NUM_WORKERS" || fatal "冻结目标校验失败，拒绝重抓或覆盖证据" 1
else
    echo "[cron] syncing latest Google target and freezing this run input" >&2
    STAGE="target_sync"
    "$PYTHON" "$TARGET_SYNC" --mirror "$DAILY_XLSX" --run-dir "$TARGET_RUN_DIR" --sites "$SITES" --output-urls "$URL_FILE" --env-file "$TARGET_ENV" --timeout 60
    "$PYTHON" "$RESUME_HELPER" freeze --run-dir "$TARGET_RUN_DIR" --url-file "$URL_FILE" --env-file "$TARGET_ENV" --workers "$NUM_WORKERS" --start-stamp "$RUN_TIMESTAMP" || fatal "冻结目标校验失败" 1
fi
if [ ! -s "$URL_FILE" ] || [ ! -s "$TARGET_ENV" ]; then
    fatal "Google target sync did not produce URL/env input" 1
fi
# shellcheck disable=SC1090
. "$TARGET_ENV"
: "${MODE2_EXPECTED_URLS:?FATAL: target sync omitted URL count}"
: "${MODE2_EXPECTED_ROWS:?FATAL: target sync omitted row count}"
: "${MODE2_TARGET_SHA256:?FATAL: target sync omitted target digest}"
URL_COUNT=$(wc -l < "$URL_FILE" | tr -d ' ')
if [ "$URL_COUNT" -ne "$MODE2_EXPECTED_URLS" ] || [ "$URL_COUNT" -eq 0 ]; then
    fatal "URL list/count mismatch: actual=$URL_COUNT expected=$MODE2_EXPECTED_URLS" 1
fi

# =========================== 2. 重新编译并验证 Go CLI ===========================
# 不复用可能与当前源码/参数表漂移的旧二进制，避免历史 --kill-orphans 参数问题。
# =========================== 3. 拆分 URL 列表为 N 份 ===========================
mkdir -p "$RUN_DIR"
# Forced reruns keep worker outputs/logs apart from the original run so the
# run_done counting below never reads a stale terminal_output.txt.
WORKER_BASE="$RUN_DIR"
if [ "$MODE2_FORCE_RERUN" = "1" ]; then
    WORKER_BASE="$RUN_DIR/rerun-$RUN_TIMESTAMP"
    mkdir -p "$WORKER_BASE"
fi
RESUME_COMPLETE=0
VALID_WORKERS=()
WORKERS_TO_RUN=()
# Same-day partial recovery is deliberately not a forced rerun.  Keep every
# independently validated original chunk immutable; retry only missing/failed
# chunks below a fresh run-local directory and join them by read-only symlink.
if [ "$MODE2_FORCE_RERUN" != "1" ] && [ -d "$RUN_DIR/w1" ]; then
    RECOVERY_PLAN=$("$PYTHON" "$RESUME_HELPER" inspect-workers --run-dir "$TARGET_RUN_DIR" --url-file "$URL_FILE" --env-file "$TARGET_ENV" --worker-base "$RUN_DIR" --workers "$NUM_WORKERS") || fatal "无法检查已有 worker 证据" 1
    while IFS= read -r i; do [ -n "$i" ] && VALID_WORKERS+=("$i"); done < <("$PYTHON" -c 'import json,sys; print(*json.load(sys.stdin)["valid"], sep="\n")' <<<"$RECOVERY_PLAN")
    while IFS= read -r i; do [ -n "$i" ] && WORKERS_TO_RUN+=("$i"); done < <("$PYTHON" -c 'import json,sys; print(*json.load(sys.stdin)["retry"], sep="\n")' <<<"$RECOVERY_PLAN")
    if [ "${#WORKERS_TO_RUN[@]}" -eq 0 ]; then
        WORKER_BASE="$RUN_DIR"
        RESUME_COMPLETE=1
        echo "[cron] all workers have strict successful evidence; finalizing without browser/Go" >&2
    else
        WORKER_BASE="$RUN_DIR/retry-$RUN_TIMESTAMP"
        mkdir -p "$WORKER_BASE"
        "$PYTHON" "$RESUME_HELPER" make-chunks --run-dir "$TARGET_RUN_DIR" --url-file "$URL_FILE" --env-file "$TARGET_ENV" --worker-base "$WORKER_BASE" --workers "$NUM_WORKERS" || fatal "retry chunk generation failed" 1
        for i in "${VALID_WORKERS[@]-}"; do
            [ -n "$i" ] || continue
            ln -s "$RUN_DIR/w$i" "$WORKER_BASE/w$i" || fatal "无法保留已验证 worker $i 输出" 1
        done
        # Bash 3.2 expands an assigned-but-empty array as unset under nounset.
        # All-retry recovery is valid, so use the nounset-safe default here.
        echo "[cron] preserving validated workers (${VALID_WORKERS[*]-}); retrying only (${WORKERS_TO_RUN[*]-})" >&2
    fi
fi
if [ "$MODE2_FINALIZE_ONLY" = "1" ] && [ "$RESUME_COMPLETE" != "1" ]; then
    fatal "MODE2_FINALIZE_ONLY requires complete validated worker evidence" 1
fi
if [ "$RESUME_COMPLETE" != "1" ]; then
    "$PYTHON" "$RESUME_HELPER" make-chunks --run-dir "$TARGET_RUN_DIR" --url-file "$URL_FILE" --env-file "$TARGET_ENV" --worker-base "$WORKER_BASE" --workers "$NUM_WORKERS" || fatal "run-local chunk generation failed" 1
fi

# =========================== 4. 启动 N 个并行 worker ===========================
if [ "$RESUME_COMPLETE" != "1" ]; then
pushd "$PROJECT_ROOT" > /dev/null
echo "[cron] building current Go binary" >&2
STAGE="build"
/opt/homebrew/bin/go build -buildvcs=false -o "$BINARY" ./cmd/trip-score-cdp
if "$BINARY" --help 2>&1 | grep -q -- "--kill-orphans"; then fatal "current CLI unexpectedly exposes deprecated --kill-orphans" 1; fi
popd > /dev/null
STAGE="workers"
if [ "${#WORKERS_TO_RUN[@]}" -eq 0 ]; then
    WORKERS_TO_RUN=($(seq 1 "$NUM_WORKERS"))
fi
for i in "${WORKERS_TO_RUN[@]}"; do
    WORKER_DIR="$WORKER_BASE/w$i"
    mkdir -p "$WORKER_DIR"
    CHUNK_FILE="$WORKER_BASE/chunks/w${i}.txt"
    "$BINARY" \
        -run-mode mode2 \
        --delay-profile "$MODE2_DELAY_PROFILE" \
        --urls-file "$CHUNK_FILE" \
        --sites "$SITES" \
        --session-file "$SESSION_FILE" \
        --fresh-profile \
        --output "$WORKER_DIR/trip_mode2_w${i}.xlsx" \
        --output-dir "$WORKER_DIR" \
        --log-file "$WORKER_DIR/terminal_output.txt" \
        --log-level normal \
        --browser-restart-every 25 \
        --browser-memory-limit-mb 4096 \
        > "$WORKER_DIR/console.log" 2>&1 &
    PIDS+=($!)
    echo "[cron] worker $i started (PID: $!, URLs: $(wc -l < "$CHUNK_FILE" | tr -d ' '))" >&2
    # Stagger startup to avoid chromedriver race
    sleep 2
done

# =========================== 5. 等待全部 worker 完成 ===========================
FAILED=0
for i in "${!PIDS[@]}"; do
    PID=${PIDS[$i]}
    WORKER_NUM=${WORKERS_TO_RUN[$i]}
    if ! wait "$PID"; then
        echo "FATAL: worker $WORKER_NUM (PID $PID) exited with error" >&2
        FAILED=1
    fi
done

if [ "$FAILED" -ne 0 ]; then
    fatal "一个或多个 worker 抓取失败，未生成合并结果"
fi

fi

# =========================== 6. 校验各 worker 输出 ===========================
TOTAL_PROCESSED=$("$PYTHON" "$RESUME_HELPER" validate-workers --run-dir "$TARGET_RUN_DIR" --url-file "$URL_FILE" --env-file "$TARGET_ENV" --worker-base "$WORKER_BASE" --workers "$NUM_WORKERS") || fatal "worker outputs/run_done evidence failed strict validation" 1

if [ "$TOTAL_PROCESSED" -ne "$MODE2_EXPECTED_ROWS" ]; then
    fatal "expected $MODE2_EXPECTED_ROWS total processed rows, got $TOTAL_PROCESSED" 1
fi

# =========================== 7. 合并 XLSX ===========================
STAGE="merge"
COLLECTION_STAMP=$("$PYTHON" -c "import json; print(json.load(open('$TARGET_RUN_DIR/mode2_resume.json')).get('collection_start_stamp') or '$RUN_TIMESTAMP')")
MERGED_XLSX="$RUN_DIR/${COLLECTION_STAMP}_trip_mode2_daily_2site.xlsx"
"$PYTHON" -c "
import openpyxl
from pathlib import Path
sites=['HK','RU']
def ids_from_urls(path):
    values=[]
    for raw in Path(path).read_text().splitlines():
        value=raw.strip().rstrip('/').split('/')[-1]
        if not value or not value.isdigit(): raise SystemExit('malformed target URL product ID: '+raw)
        values.append(value)
    if len(values)!=len(set(values)): raise SystemExit('duplicate frozen target IDs')
    return set(values)
all_ids=[x.strip().rstrip('/').split('/')[-1] for x in Path('$URL_FILE').read_text().splitlines() if x.strip()]
chunk_size=(len(all_ids)+$NUM_WORKERS-1)//$NUM_WORKERS
for worker in range(1,$NUM_WORKERS+1):
    # Legacy runs without a local manifest are derived solely from the frozen
    # URL order and recorded worker count, never from a shared /tmp file.
    expected=set(all_ids[(worker-1)*chunk_size:worker*chunk_size])
    if not expected: raise SystemExit(f'worker {worker} derived chunk is empty')
    wb=openpyxl.load_workbook(f'$WORKER_BASE/w{worker}/trip_mode2_w{worker}.xlsx',read_only=True,data_only=True)
    try:
      if wb.sheetnames != sites: raise SystemExit(f'worker {worker} sheets must be exactly HK/RU: {wb.sheetnames!r}')
      for site in sites:
        rows=wb[site].iter_rows(values_only=True); head=next(rows,())
        if '商品ID' not in head: raise SystemExit(f'worker {worker} {site} missing 商品ID')
        ix=list(head).index('商品ID'); values=[]
        for row in rows:
          value=row[ix] if ix<len(row) else None
          if not isinstance(value,str) or not value.strip(): raise SystemExit(f'worker {worker} {site} malformed 商品ID')
          values.append(value.strip())
        if len(values)!=len(expected) or len(values)!=len(set(values)) or set(values)!=expected: raise SystemExit(f'worker {worker} {site} IDs do not exactly match its chunk')
    finally: wb.close()
" || fatal "worker workbook must contain exactly HK/RU and complete chunk IDs" 1
"$PYTHON" -c "
import openpyxl, sys, os

sites = ['HK', 'RU']
merged = openpyxl.Workbook()
merged.remove(merged.active)

for site in sites:
    ws = merged.create_sheet(site)
    header_written = False
    for i in range(1, $NUM_WORKERS + 1):
        path = f'$WORKER_BASE/w{i}/trip_mode2_w{i}.xlsx'
        if not os.path.exists(path):
            continue
        wb = openpyxl.load_workbook(path, read_only=True, data_only=True)
        if site not in wb.sheetnames:
            wb.close()
            continue
        src = wb[site]
        rows = src.iter_rows(values_only=True)
        if not header_written:
            header = next(rows, None)
            if header:
                ws.append(list(header))
                header_written = True
            else:
                wb.close()
                continue
        else:
            next(rows, None)  # skip header
        for row in rows:
            if row:
                ws.append(list(row))
        wb.close()

merged.save('$MERGED_XLSX')
print('merged ok')
" 2>&1

if [ ! -f "$MERGED_XLSX" ] || [ ! -s "$MERGED_XLSX" ]; then
    fatal "merged xlsx missing or empty: $MERGED_XLSX" 1
fi
# Fail closed: require every frozen target once in each requested market.
"$PYTHON" -c "
import openpyxl
from pathlib import Path
urls=[x.strip().rstrip('/').split('/')[-1] for x in Path('$URL_FILE').read_text().splitlines() if x.strip()]
if any(not x.isdigit() for x in urls): raise SystemExit('malformed frozen target product ID')
expected=set(urls); wb=openpyxl.load_workbook('$MERGED_XLSX', read_only=True, data_only=True)
if wb.sheetnames != ['HK','RU']: raise SystemExit('merged sheets must be exactly HK,RU: '+repr(wb.sheetnames))
for site in ('HK','RU'):
 ws=wb[site]; rows=ws.iter_rows(values_only=True); head=next(rows, None) or ()
 if '商品ID' not in head: raise SystemExit(site+' missing 商品ID')
 ix=list(head).index('商品ID'); ids=[]
 for r in rows:
  value=r[ix] if len(r)>ix else None
  if not isinstance(value,str) or not value.strip(): raise SystemExit(site+' malformed 商品ID')
  ids.append(value.strip())
 if len(ids)!=len(urls) or len(set(ids))!=len(ids) or set(ids)!=expected: raise SystemExit(f'{site} target IDs incomplete/duplicate')
wb.close()
" || fatal "merged HK/RU workbook failed strict sheet/row/target-ID validation" 1
STAGE="enrichment"
if ! "$PYTHON" "$VENDOR_STABILIZER" "$MERGED_XLSX" --target-table "$TARGET_RUN_DIR/target/日常抓取目标.xlsx" --require-attribution >&2; then
    fatal "厂商归属整理失败，厂商列仍为「手动输入」，已阻止投递"
fi

# =========================== 7b. 插入昨日总促销标签基线列 ===========================
# Copies the previous calendar day's exact 总促销标签 text per (site, 商品ID)
# into a `昨日总促销标签` column right before today's 总促销标签. Missing
# baseline -> empty cells; idempotent on rerun; never touches prior workbooks.
STAGE="enrichment"
"$PYTHON" "$YESTERDAY_PROMO" "$MERGED_XLSX" --root "$RUN_DIR_BASE" >&2

# =========================== 8. 写完成标记 + 直接投递 ===========================
"$PYTHON" -c "
import json, datetime
data = {
    'run_dir': '$RUN_DIR',
    'output_path': '$MERGED_XLSX',
    'processed_count': $TOTAL_PROCESSED,
    'url_count': $URL_COUNT,
    'sites': '$SITES',
    'scope': 'hk_ru_2site',
    'workers': $NUM_WORKERS,
    'target_sha256': '$MODE2_TARGET_SHA256',
    'target_snapshot': '$TARGET_RUN_DIR/target/日常抓取目标.xlsx',
    'target_provenance': '$TARGET_RUN_DIR/target/provenance.json',
    'collection_start_stamp': '$COLLECTION_STAMP',
    'cron_tick': datetime.datetime.now().isoformat()
}
with open('$DONE_MARKER', 'w') as f:
    json.dump(data, f)
"

# The collector owns both deliveries. This avoids model-backed polling jobs
# and sends only after all workers/XLSX validation completed successfully.
# On a forced rerun the fresh XLSX must be delivered (and the promo-change
# report regenerated) despite existing .delivery_done/.promo_change_reported.
STAGE="delivery"
if [ "$MODE2_FORCE_RERUN" = "1" ]; then
    MODE2_FORCE_DELIVERY=1 "$PYTHON" "$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_direct_delivery.py" --run-dir "$RUN_DIR" --root "$RUN_DIR_BASE" >&2
else
    "$PYTHON" "$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_direct_delivery.py" --run-dir "$RUN_DIR" --root "$RUN_DIR_BASE" >&2
fi
printf '{"run_dir":"%s","output_path":"%s","processed_count":%s,"delivery":"direct"}\n' \
  "$RUN_DIR" "$MERGED_XLSX" "$TOTAL_PROCESSED"
