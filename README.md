# Trip.com SKU / 价格抓取工具（Go CDP-only）

本项目现在只保留 **Go + Chrome DevTools Protocol (CDP)** 抓取程序。旧版 Python CLI、Python GUI、Selenium/undetected-chromedriver 流程以及 PyInstaller 打包 spec 已删除并停止跟踪；用于自动化机器执行的支持脚本仍保留在仓库中。

唯一入口：

```bash
go run ./cmd/trip-score-cdp --help
```

推荐构建成本地二进制：

```bash
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
/tmp/trip-score-cdp --help
```

## 功能范围

`trip-score-cdp` 从 Trip.com 商品页面抓取：

- 商品 ID / URL / 商品名称
- 评分、评价数、预订数
- 客户服务、方便程度、讯号状态、中差评价数
- 商品资讯
- SKU 层级组合
- 外显价格、动态实价、促销标签
- 厂商 Excel 批量抓取
- TSIM / 任意基准厂商 vs 竞对厂商横向比价

## 项目结构

```text
cmd/trip-score-cdp/main.go        # 唯一可执行入口
internal/tripcdp/                 # Go CDP 抓取、浏览器控制、导出、比价逻辑
HERMES.md                         # Hermes Agent 项目入口
hermes-agent/skills/trip-sku-price-scraper-operations/
                                  # 项目内唯一 Hermes 操作 skill
```

已移除的旧组件：

```text
main.py
cli.py
gui.py
trip_get_scoreandprice.pyw
trip_get_scoreandprice.spec
service.py / models.py / session_store.py / browser_startup.py / trip_cli_scraper.py / xlsx_export.py
requirements.txt
tests/test_*.py
```

## 快速开始

### 构建

```bash
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
```

### 单 URL 抓取

```bash
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --headless \
  --delay-profile normal \
  --timeout 1800 \
  --input "https://www.trip.com/things-to-do/detail/..." \
  --output ./trip_product.xlsx
```

### URL 文件抓取

`urls.txt` 每行一个 Trip.com 商品 URL：

```bash
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --headless \
  --delay-profile normal \
  --timeout 1800 \
  --urls-file ./urls.txt \
  --output ./trip_products.xlsx
```

### 厂商 Excel 批量抓取

输入 Excel 第一行为厂商名，第 2 行起为 Trip.com 商品 URL：

```text
| TSIM 網址                  | BC                         | TGT                        |
|----------------------------|----------------------------|----------------------------|
| https://hk.trip.com/...    | https://hk.trip.com/...    | https://hk.trip.com/...    |
```

运行：

```bash
RUN_DIR="/Users/example/Documents/trip-sku-price-scrape/$(date +%y%m%d-%H%M)"
mkdir -p "$RUN_DIR"

/tmp/trip-score-cdp \
  --run-mode mode1 \
  --headless \
  --delay-profile normal \
  --timeout 1800 \
  --excel-input ./vendors.xlsx \
  --workers 3 \
  --log-level verbose \
  --compare-vendor "TSIM 網址" \
  --output-dir "$RUN_DIR"
```

## 推荐运行参数

日常 / cron 默认：

```text
--run-mode mode1
--headless
--delay-profile normal
--timeout 1800
--workers 3
--compare-vendor "TSIM 網址"
```

说明：

- `normal` 已由用户更新为默认；`safe` 用于风控场景。
- `--timeout 1800` 用于降低复杂多 SKU 商品在 worker/session 超时下半截漏抓的风险。
- `--headless` 是当前日常默认主抓取模式；低质量结果会自动切换为有头 `safe` 单工重跑补救。
- `--workers 3` 是当前每日厂商 Excel 模式的默认上限。
- `--compare-vendor` 可指定任意输入表头作为基准厂商；当前日常任务使用 `TSIM 網址`。

## 输出文件

单 URL / URL 文件模式通常输出一个 XLSX。

厂商 Excel 模式会在 `--output-dir` 下输出：

```text
<vendor>_<run>.xlsx                 # 每厂商原始抓取结果
vendor_compare_<run>.xlsx           # 横向比价工作簿
vendor_compare_<run>.json           # 比价摘要 JSON
_workers/                           # 运行中 worker 状态，成功结束后通常会清理
```

## vendor_compare 工作簿

`--compare-vendor <基准厂商>` 会生成：

```text
vendor_compare_<run>.xlsx
```

工作表：

- `汇总`
  - 各竞对厂商的匹配地区数、匹配 SKU 数、均价差、基准更优占比。
- `总表_横向比价`
  - SKU 粒度横向矩阵。
  - 每行 = 同一输入行 / 目的地 + 同一结构化 SKU 规格。
  - 各厂商放在连续列块中。
- `单链接均价对比`
  - 目的地 / 输入行粒度。
  - 每行 = 输入 Excel 的一行 / 一个同地区产品组。
  - 不逐 SKU 展开；用重合 SKU 的均价评估厂商整体价格水平。
- `<vendor>_vs_<base>`
  - 单竞对 vs 基准的 SKU 粒度横向明细。

### SKU 匹配规则

比价使用结构化 SKU 规格解析：

```text
有效期天数 + 流量类型（总量/每日/无限） + 流量MB
```

这些会识别为同规格：

```text
總量-10GB
總量 10GB
总量10GB
5G-總量-10GB
5G網絡-總量-10GB
```

原则：

- `总量` 与 `每日` 不默认合并。
- `500MB` 与 `0.5GB` 可按单位换算归一。
- 竞对有但基准没有的 SKU 不丢弃；在 SKU 明细表中保留，基准列块留空。
- 所有均价 / 最低价 / 价差统计只基于匹配 SKU 交集。
- `相对基准最低价差` 只填在该产品对里竞对最低价匹配 SKU 所在行。

## 运行模式

```text
mode1 = 有头完整抓取：评分 + 商品资讯 + SKU层级 + 外显价 + 实价 + 促销标签
mode2 = 有头仅评分/商品资讯抓取：只输出 商品ID、URL、商品名称、商品资讯、预定数、评价数、评分、客户服务、方便程度、讯号状态、中差评价数；不抓 SKU、不写货币/外显价/实价/促销标签
```

`mode2` 是固定兼容模式，对齐旧 GUI 中只勾选「仅抓取评分」+「抓取商品资讯」的行为；即使误传 `--real-price` 或 `--no-rating-only`，也不会进入 SKU/价格抓取路径。

## 延迟档位

`--delay-profile` 支持：

```text
fast
normal
safe
slow
```

日常主抓取默认推荐 `normal`，并配合 `--headless`、`--timeout 1800`、`--workers 3`。如果仍出现风控、页面加载慢、SKU 点击失败，低质量补救重跑会自动使用有头 `safe`；不要默认使用 `fast`。

## 浏览器与状态

- 仅支持 Chrome。
- 程序自己管理 Chrome/CDP worker profile，不依赖旧 Python session。
- 厂商 Excel 模式按厂商 worker 隔离浏览器状态。
- SIGINT/SIGTERM 会尽量导出已完成结果后退出。

## Hermes cron 集成

当前日常任务不使用 macOS LaunchAgent；使用 Hermes cron no-agent。cron wrapper 位于 `~/.hermes/scripts/`，实际逻辑放在项目内并由 git 追踪：

```text
~/.hermes/scripts/trip_sku_daily_launcher.py                 # thin wrapper
scripts/hermes-cron/trip_sku_daily_launcher.py               # project-tracked launcher
scripts/hermes-cron/trip_sku_trend_finalize.py               # project-tracked trend finalizer
```

该定时任务是“SKU 厂商每日价格趋势抓取”，不是“非 TSIM 厂商抓取”。配置表中可以包含 TSIM 与其他厂商；当前日常横向比价的默认基准厂商是 `TSIM 網址`，通过 launcher 中的 `DEFAULT_COMPARE_VENDOR` 设置，后续可替换为其他输入表头。

该脚本会：

1. 构建 Go 二进制 `/tmp/trip-score-cdp`
2. 复制当日输入 Excel 到运行目录
3. 启动 Go CDP 抓取
4. 生成 `run_daily_sku_vendor_trend.sh` 作为本次 run 的 detached runner
5. 完成后执行趋势分析和打包
6. 投递 ZIP

## 项目内 skill

操作说明已同步到项目：

```text
HERMES.md
hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md
```

Hermes Agent 应先读取 `HERMES.md`，再完整读取其中指定的 Skill。项目不再维护用户目录下的 Skill 副本。当修改抓取、比价、cron 或报告逻辑时，应同步更新该 Skill。

Hermes 手动抓取单个或数个指定产品时，固定使用项目根目录构建的 `./trip-score-price-scraper`，以重复 `-input` 传入产品，并使用 `-no-headless -delay-profile fast -product-info -no-quality-rerun -output-dir <manual-dir>`。运营提示词如 `【請抓取產品價格71827107】` 会按产品 ID 构造香港站 HKD URL。有效成功时用户可见响应只能是原生 XLSX 的单行 `MEDIA:` 附件，不先发送平文本价格、摘要或二次整理表。批量和 cron 任务继续使用各自章节的参数。

## 验证

```bash
go test ./...
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
/tmp/trip-score-cdp --help
```

## 维护约定

- 不再恢复 Python CLI / GUI / PyInstaller spec 作为入口。
- 不再新增 Python 抓取主流程。
- Go CDP 程序是唯一动作组件和唯一入口。
- README 是项目唯一主使用文档。
