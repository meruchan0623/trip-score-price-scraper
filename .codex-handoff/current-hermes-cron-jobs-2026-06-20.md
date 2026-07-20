# 当前 Hermes Cron 定时任务描述文件（Codex Handoff）

> 生成时间：2026-06-20  
> 来源：`cronjob(action='list')` 当前 Hermes profile: `default`  
> 用途：给 Codex 与程序代码、Skill 一起阅读，理解当前定时任务触发、投递和与 Trip SKU 程序的关系。

---

## 1. 与本程序直接相关的 Trip SKU Cron

### file: `cron/trip-sku-daily-launcher.yaml`

```yaml
job_id: d3a8d581e5c9
name: Trip SKU厂商每日价格趋势抓取-启动器
program_relation: direct
schedule: "0 8 * * *"
repeat: "26/3650"
deliver: origin
enabled: true
state: scheduled
script: trip_sku_daily_launcher.py
no_agent: true
next_run_at: "2026-06-21T08:00:00+08:00"
last_run_at: "2026-06-20T08:00:20.535505+08:00"
last_status: ok
last_delivery_error: null
workflow:
  - Hermes cron 调用 ~/.hermes/scripts/trip_sku_daily_launcher.py
  - wrapper exec 项目脚本 scripts/hermes-cron/trip_sku_daily_launcher.py
  - 项目脚本创建 run_dir、复制输入表、go build、生成 detached runner
  - runner 执行 /tmp/trip-score-cdp 抓取、趋势 finalizer、final delivery
current_default_cli:
  run_mode: mode1
  headless: true
  delay_profile: normal
  timeout: 1800
  workers: 3
  compare_vendor: "TSIM 網址"
  output_root: /Users/tsimclaw/Documents/trip-sku-price-scrape
codex_notes:
  - 这是 SKU 动态抓取主入口；不要把 watcher 当作主抓取入口。
  - launcher 必须 quick fire-and-exit，不要在 cron 进程内长轮询。
  - 修改默认参数时必须同步 input_summary/current_state config、任务说明.md、实际 shell CLI 三处。
```

### file: `cron/trip-sku-daily-watcher-delivery.yaml`

```yaml
job_id: eedb567db83c
name: Trip SKU厂商每日价格趋势抓取-完成投递
program_relation: direct
schedule: "every 60m"
repeat: "619/36500"
deliver: "origin,telegram:运营自动化数据组"
enabled: true
state: scheduled
script: trip_sku_non_tsim_daily_watcher.py
no_agent: true
next_run_at: "2026-06-20T17:32:43.340485+08:00"
last_run_at: "2026-06-20T16:32:43.340485+08:00"
last_status: ok
last_delivery_error: null
workflow:
  - 读取 /Users/tsimclaw/Documents/trip-sku-price-scrape/current_state.json
  - 若 status 为 running/launched，则解析 terminal_output.txt 输出简短进度
  - 若 status 为 finalized 且未见 .delivered_daily_trend，则调用 final_delivery 补偿投递
  - 正常最终投递优先由 detached runner 内部的 final_delivery 完成，watcher 是兜底
codex_notes:
  - 文件名仍有 non_tsim legacy 字样，但任务已经包含 TSIM 与竞对，不要按非 TSIM 理解。
  - watcher 的 format_final 当前负责平文本 Top5 报告结构，是本次报告改造重点。
  - 当前 sales_abs 聚合存在按 SKU 行重复累加风险；运营版应按商品级字段只计一次。
```

### file: `cron/trip-sku-one-shot-normal-interception-test.yaml`

```yaml
job_id: b0a59be10c5e
name: 一次性SKU Normal模式拦截测试
program_relation: direct_test
schedule: "once at 2026-06-20 17:00"
repeat: once
deliver: origin
enabled: true
state: scheduled
script: trip_sku_daily_launcher.py
no_agent: true
next_run_at: "2026-06-20T17:00:00+08:00"
last_run_at: null
last_status: null
last_delivery_error: null
prompt_preview: "一次性在17:00启动 Trip SKU Normal 默认参数全量抓取，用于观察拦截/质量失败数量。脚本会启动 detached runner 并退出；后续由现有 SKU watcher/final..."
codex_notes:
  - 与主 launcher 使用同一脚本；属于一次性诊断任务，不应固化为长期业务输出入口。
  - 如果 Codex 调整输出方案，需注意一次性任务也会走同一 finalizer/final_delivery 链路。
```

---

## 2. 当前 Hermes Cron 全量清单（含非本程序任务）

### file: `cron/trip-outerlist-daily-full-builtin-compare-xlsx.yaml`

```yaml
job_id: 3476b2c48c71
name: trip-outerlist-daily-full-builtin-compare-xlsx
program_relation: unrelated_trip_outerlist
schedule: "30 9 * * *"
repeat: forever
deliver: "origin,telegram:-5212298591"
enabled: true
state: scheduled
script: trip_outerlist_daily_full.py
no_agent: true
next_run_at: "2026-06-21T09:30:00+08:00"
last_run_at: "2026-06-20T09:31:26.868028+08:00"
last_status: ok
last_delivery_error: null
codex_notes:
  - Trip Outerlist 任务，不属于 SKU 价格抓取程序；不要混入本次 SKU 输出方案。
```

### file: `cron/ctrip-comment-daily-mid-bad-export.yaml`

```yaml
job_id: 8c58be5afe1c
name: 携程评价日报（中评+差评）
program_relation: unrelated_comment_export
schedule: "0 5 * * 0,2-6"
repeat: forever
deliver: "telegram:TSim 运营自动化配置组,telegram:Meru and TSim Hermes Bot - 客服数据组,telegram:TSim Hermes客服数据组"
enabled: true
state: scheduled
script: daily_mid_bad_export.py
no_agent: false
prompt_preview: "原样输出上面的脚本报告内容。"
next_run_at: "2026-06-21T05:00:00+08:00"
last_run_at: "2026-06-20T05:01:07.321725+08:00"
last_status: ok
last_delivery_error: "delivery error: Telegram send failed: Chat not found"
codex_notes:
  - 携程评论导出任务，不属于 SKU 价格抓取程序。
```

### file: `cron/ctrip-comment-weekly-mid-bad-export.yaml`

```yaml
job_id: b82469836319
name: 携程评价周报（中评+差评）
program_relation: unrelated_comment_export
schedule: "0 5 * * 1"
repeat: forever
deliver: "telegram:TSim 运营自动化配置组,telegram:Meru and TSim Hermes Bot - 客服数据组,telegram:TSim Hermes客服数据组"
enabled: true
state: scheduled
script: weekly_mid_bad_export.py
no_agent: false
prompt_preview: "原样输出上面的脚本报告内容。"
next_run_at: "2026-06-22T05:00:00+08:00"
last_run_at: "2026-06-15T05:03:21.565189+08:00"
last_status: ok
last_delivery_error: "delivery error: Telegram send failed: Chat not found; delivery error: Telegram send failed: Chat not found"
codex_notes:
  - 携程评论导出任务，不属于 SKU 价格抓取程序。
```

### file: `cron/kkday-comment-daily-export.yaml`

```yaml
job_id: 50e3a7e31dc4
name: KKday评价日报
program_relation: unrelated_comment_export
schedule: "30 5 * * *"
repeat: forever
deliver: origin
enabled: true
state: scheduled
script: kkday_daily_comment_export.py
no_agent: true
next_run_at: "2026-06-21T05:30:00+08:00"
last_run_at: null
last_status: null
last_delivery_error: null
codex_notes:
  - KKday 评论导出任务，不属于 SKU 价格抓取程序。
```

---

## 3. Codex 应重点阅读的关联文件

```text
/Users/tsimclaw/Develop/trip-score-price-scraper/
├── scripts/hermes-cron/trip_sku_daily_launcher.py
├── scripts/hermes-cron/trip_sku_trend_finalize.py
├── internal/tripcdp/vendor_compare.go
├── internal/tripcdp/runner.go
├── internal/tripcdp/cli.go
├── internal/tripcdp/export.go
├── hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md
├── docs/sku-dynamic-report-operations-optimization-2026-06-20.md
└── .codex-handoff/current-hermes-cron-jobs-2026-06-20.md
```

---

## 4. 协调输出方案改造边界

- 不建议重写 Go 抓取主链路；优先新增运营版报告生成层。
- 保留原始完整诊断输出，新增外放运营版 XLSX / Markdown / Telegram 平文本。
- 任何定时任务默认参数改动都要同时更新：Hermes cron 描述、launcher state metadata、任务说明.md、实际 CLI 命令、Skill。
- Telegram 外放报告应由固定脚本生成，不交给 LLM 临场组织。
