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

RUN_NAME="$(date +%y%m%d)-mode2-daily"
RUN_TIMESTAMP="$(date +%y%m%d-%H%M)"
RUN_DIR="$RUN_DIR_BASE/$RUN_NAME"
DONE_MARKER="$RUN_DIR/.collection_done"
YESTERDAY_PROMO="$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_yesterday_promo.py"
FAIL_REASON_FILE="$RUN_DIR/.failure_reason"
NOTIFY_MARKER="$RUN_DIR/.failure_notified"
# Failure notices must be short and in Chinese (group convention) and must never
# fire for a successful run. MODE2_NOTIFY_DISABLED=1 suppresses the outbound
# message for tests while still recording the reason.
NOTIFY_TARGET="${MODE2_NOTIFY_TARGET:-$TRIP_DELIVERY_TARGET}"
mkdir -p "$RUN_DIR"

notify_failure() {
    local code=$?
    rmdir "$LOCK_DIR" 2>/dev/null || true
    if [ "$code" -eq 0 ]; then
        return 0
    fi
    if [ -f "$NOTIFY_MARKER" ]; then
        return 0
    fi
    local reason="exit $code"
    if [ -s "$FAIL_REASON_FILE" ]; then
        reason="$(/usr/bin/head -c 300 "$FAIL_REASON_FILE")"
    fi
    touch "$NOTIFY_MARKER" 2>/dev/null || true
    if [ "${MODE2_NOTIFY_DISABLED:-0}" = "1" ]; then
        echo "[cron] failure notice suppressed: $reason" >&2
        return 0
    fi
    /usr/bin/env hermes send --to "$NOTIFY_TARGET" "❌ 携程Mode2六站采集失败：${reason}" >/dev/null 2>&1 || true
}
trap notify_failure EXIT

# Record a short failure reason for the notice, then stop.
fatal() {
    printf 'FATAL: %s\n' "$1" >&2
    printf '%s\n' "$1" > "$FAIL_REASON_FILE" 2>/dev/null || true
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
SITES="tw,hk,ru,jp,vn,th"
# Manual-only: MODE2_FORCE_RERUN=1 bypasses the .collection_done skip and
# produces a fresh timestamped XLSX (existing XLSX files are never deleted).
# Never set by the cron schedule itself.
MODE2_FORCE_RERUN="${MODE2_FORCE_RERUN:-0}"

# Refuse to begin a customer-data collection without the explicitly refreshed
# session. This avoids silently resurrecting stale session.json files from a
# prior run's worker profile.
if [ ! -s "$SESSION_FILE" ]; then
    fatal "refreshed Trip CLI session is missing or empty: $SESSION_FILE" 1
fi

# =========================== 0. 防止重复运行 ===========================
# A mkdir lock protects both the shared target mirror and the five Chrome workers.
if ! mkdir "$LOCK_DIR" 2>/dev/null; then
    fatal "another Mode2 collection is already running (lock: $LOCK_DIR)" 75
fi

if [ -f "$DONE_MARKER" ]; then
    if [ "$MODE2_FORCE_RERUN" = "1" ]; then
        echo "[cron] MODE2_FORCE_RERUN=1: ignoring existing $DONE_MARKER, rerunning collection" >&2
    else
        echo "[cron] collection already completed today, skipping" >&2
        cat "$DONE_MARKER"
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
echo "[cron] syncing latest Google target and freezing this run input" >&2
"$PYTHON" "$TARGET_SYNC"     --mirror "$DAILY_XLSX"     --run-dir "$TARGET_RUN_DIR"     --sites "$SITES"     --output-urls "$URL_FILE"     --env-file "$TARGET_ENV"     --timeout 60
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
pushd "$PROJECT_ROOT" > /dev/null
echo "[cron] building current Go binary" >&2
/opt/homebrew/bin/go build -buildvcs=false -o "$BINARY" ./cmd/trip-score-cdp
if "$BINARY" --help 2>&1 | grep -q -- "--kill-orphans"; then
    fatal "current CLI unexpectedly exposes deprecated --kill-orphans" 1
fi
popd > /dev/null

# =========================== 3. 拆分 URL 列表为 N 份 ===========================
mkdir -p "$RUN_DIR"
# Forced reruns keep worker outputs/logs apart from the original run so the
# run_done counting below never reads a stale terminal_output.txt.
WORKER_BASE="$RUN_DIR"
if [ "$MODE2_FORCE_RERUN" = "1" ]; then
    WORKER_BASE="$RUN_DIR/rerun-$RUN_TIMESTAMP"
    mkdir -p "$WORKER_BASE"
fi
CHUNK_SIZE=$(( (URL_COUNT + NUM_WORKERS - 1) / NUM_WORKERS ))
for i in $(seq 1 $NUM_WORKERS); do
    CHUNK_FILE="/tmp/trip_mode2_chunk_${i}.txt"
    START=$(( (i - 1) * CHUNK_SIZE + 1 ))
    END=$(( i * CHUNK_SIZE ))
    sed -n "${START},${END}p" "$URL_FILE" > "$CHUNK_FILE"
    if [ ! -s "$CHUNK_FILE" ]; then
        fatal "chunk $i is empty" 1
    fi
    CHUNK_COUNT=$(wc -l < "$CHUNK_FILE" | tr -d ' ')
    echo "[cron] chunk $i: $CHUNK_COUNT URLs" >&2
done

# =========================== 4. 启动 N 个并行 worker ===========================
PIDS=()
for i in $(seq 1 $NUM_WORKERS); do
    WORKER_DIR="$WORKER_BASE/w$i"
    mkdir -p "$WORKER_DIR"
    CHUNK_FILE="/tmp/trip_mode2_chunk_${i}.txt"
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
for i in $(seq 0 $((NUM_WORKERS - 1))); do
    PID=${PIDS[$i]}
    WORKER_NUM=$((i + 1))
    if ! wait "$PID"; then
        echo "FATAL: worker $WORKER_NUM (PID $PID) exited with error" >&2
        FAILED=1
    fi
done

if [ "$FAILED" -ne 0 ]; then
    fatal "一个或多个 worker 抓取失败，未生成合并结果"
fi

# =========================== 6. 校验各 worker 输出 ===========================
TOTAL_PROCESSED=0
for i in $(seq 1 $NUM_WORKERS); do
    WORKER_DIR="$WORKER_BASE/w$i"
    OUTPUT_XLSX="$WORKER_DIR/trip_mode2_w${i}.xlsx"
    if [ ! -f "$OUTPUT_XLSX" ] || [ ! -s "$OUTPUT_XLSX" ]; then
        fatal "worker $i output xlsx missing or empty: $OUTPUT_XLSX" 1
    fi
    # Count processed rows from terminal output
    RUN_DONE_LINE=$(grep -m1 '"event":"run_done"' "$WORKER_DIR/terminal_output.txt" 2>/dev/null || true)
    PROCESSED=$(echo "$RUN_DONE_LINE" | "$PYTHON" -c "import sys,json; d=json.load(sys.stdin); print(d.get('processed_count',0))" 2>/dev/null || echo 0)
    TOTAL_PROCESSED=$((TOTAL_PROCESSED + PROCESSED))
    echo "[cron] worker $i: $PROCESSED rows" >&2
done

if [ "$TOTAL_PROCESSED" -ne "$MODE2_EXPECTED_ROWS" ]; then
    fatal "expected $MODE2_EXPECTED_ROWS total processed rows, got $TOTAL_PROCESSED" 1
fi

# =========================== 7. 合并 XLSX ===========================
MERGED_XLSX="$RUN_DIR/${RUN_TIMESTAMP}_trip_mode2_daily_6site.xlsx"
"$PYTHON" -c "
import openpyxl, sys, os

sites = ['TW', 'HK', 'RU', 'JP', 'VN', 'TH']
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
if ! "$PYTHON" "$VENDOR_STABILIZER" "$MERGED_XLSX" --require-attribution >&2; then
    fatal "厂商归属整理失败，厂商列仍为「手动输入」，已阻止投递"
fi

# =========================== 7b. 插入昨日总促销标签基线列 ===========================
# Copies the previous calendar day's exact 总促销标签 text per (site, 商品ID)
# into a `昨日总促销标签` column right before today's 总促销标签. Missing
# baseline -> empty cells; idempotent on rerun; never touches prior workbooks.
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
    'workers': $NUM_WORKERS,
    'target_sha256': '$MODE2_TARGET_SHA256',
    'target_snapshot': '$TARGET_RUN_DIR/target/日常抓取目标.xlsx',
    'target_provenance': '$TARGET_RUN_DIR/target/provenance.json',
    'cron_tick': datetime.datetime.now().isoformat()
}
with open('$DONE_MARKER', 'w') as f:
    json.dump(data, f)
"

# The collector owns both deliveries. This avoids model-backed polling jobs
# and sends only after all workers/XLSX validation completed successfully.
# On a forced rerun the fresh XLSX must be delivered (and the promo-change
# report regenerated) despite existing .delivery_done/.promo_change_reported.
if [ "$MODE2_FORCE_RERUN" = "1" ]; then
    MODE2_FORCE_DELIVERY=1 "$PYTHON" "$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_direct_delivery.py" >&2
else
    "$PYTHON" "$PROJECT_ROOT/scripts/hermes-cron/trip_mode2_direct_delivery.py" >&2
fi
printf '{"run_dir":"%s","output_path":"%s","processed_count":%s,"delivery":"direct"}\n' \
  "$RUN_DIR" "$MERGED_XLSX" "$TOTAL_PROCESSED"
