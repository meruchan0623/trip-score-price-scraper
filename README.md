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
- 外显价格、动态实价、SKU 促销标签、商品级总促销标签
- 通过 `--site` 选择 33 个已登记站点（默认 `hk`），并用 `--locale` / `--currency` 覆盖展示语言与价格币种
- 厂商 Excel 批量抓取
- TSIM / 任意基准厂商 vs 竞对厂商横向比价

## URL 拼接约定（重要）

用产品 ID / 目标表拼接抓取 URL 时，**默认只拼 `https://{site}.trip.com/things-to-do/detail/<产品ID>/`，不追加任何查询参数**（如 `?locale=zh-HK&curr=HKD`）。

- 只有当用户**显式传入需要抓取的货币/站点/语言**时，才追加对应参数（例如用户说「抓 HKD」「新加坡站」「MYR」时，才按需补 `?locale=...&curr=...` 或 `--site/--locale/--currency`）。
- 原因：CLI 默认站为 `hk`，并由 returned-context gate 校验最终 host/site/locale/currency；URL 上多余的默认参数可能与实际请求不一致，也不必要。让 CLI 自行按默认站与校验规则规范化真实导航。
- 从「日常抓取目标.xlsx」这类 ID 目标表构造清单时，同样遵循：只拼 `/detail/<ID>/`，除非用户另行指定货币/站点。


## 项目结构

```text
cmd/trip-score-cdp/main.go        # 唯一可执行入口
internal/tripcdp/                 # Go CDP 抓取、浏览器控制、导出、比价逻辑
HERMES.md                         # Hermes Agent 项目入口
hermes-agent/skills/trip-sku-price-scraper-operations/
                                  # 项目内唯一 Hermes 操作 skill
hermes-agent/plugins/trip-manual-delivery-guard/
                                  # 指定产品抓取发送前静默交付插件
tests/test_trip_manual_delivery_guard.py
                                  # Hermes 回复过滤回归测试
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
旧版 Python 抓取器对应的测试
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
  --delay-profile fast \
  --timeout 1800 \
  --input "https://www.trip.com/things-to-do/detail/..." \
  --output ./trip_product.xlsx
```

### 分站切换

`--site` 先选择目标 host 及其默认 locale/currency；随后 `--locale` 和 `--currency` 分别覆盖展示语言和价格币种。每次导航都会显式带上 `locale`、`curr`，并由 returned-context gate 校验最终 host、站点、locale、currency；非默认组合只有在该校验通过后才会产生可信价格行。未知站点、未广告 locale 或不支持的 ISO 币种会在参数解析阶段失败，不会静默回退到香港站。完整登记和观测证据见 [Trip Things-to-Do 市场兼容性记录](docs/trip-things-to-do-market-compatibility.md)。

例如抓取同一商品的新加坡站：

```bash
/tmp/trip-score-cdp \
  --site sg \
  --fresh-profile \
  --no-headless \
  --delay-profile fast \
  --input "https://hk.trip.com/things-to-do/detail/45892282/" \
  --output ./trip_product_sg.xlsx
```

更多市场/覆盖组合示例：

```bash
/tmp/trip-score-cdp-all-sites --site jp --fresh-profile --no-headless \
  --delay-profile fast \
  --input "https://hk.trip.com/things-to-do/detail/45892282/" \
  --output /tmp/trip-jp-45892282.xlsx

/tmp/trip-score-cdp-all-sites --site sg --locale zh-SG --currency MYR \
  --fresh-profile --no-headless --delay-profile normal \
  --input "https://hk.trip.com/things-to-do/detail/45892282/" \
  --output /tmp/trip-sg-zh-myr-45892282.xlsx
```

输入 URL 的原始分站仅用于识别商品路径；最终导航和输出 URL 由 `--site` 规范化。Hermes 指定产品手工交付流程仍固定使用默认香港站，不传 `--site sg`。

公共页面本地调试时可显式传 `--fresh-profile`，避免复制历史浏览器资料；该参数默认关闭，且不会复用已保存的登录态。

### URL 文件抓取

`urls.txt` 每行一个 Trip.com 商品 URL：

```bash
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --delay-profile fast \
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
  --delay-profile fast \
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
--delay-profile fast
--timeout 1800
--workers 3
--compare-vendor "TSIM 網址"
```

说明：

- `fast` 已由用户更新为默认；`normal`/`safe`/`slow` 用于特殊情况。
- `--timeout 1800` 用于降低复杂多 SKU 商品在 worker/session 超时下半截漏抓的风险。
- **有头浏览器（visible Chrome）现在是默认模式**：mode1 和 mode2 都默认启动可见 Chrome 窗口。如需关闭，传入 `--headless`。低质量 SKU 补救重跑默认仍为有头 `safe`。
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

`mode2` 是固定兼容模式，对齐旧 GUI 中只勾选「仅抓取评分」+「抓取商品资讯」的行为；即使误传 `--real-price` 或 `--no-rating-only`，也不会进入 SKU/价格抓取路径。两个模式默认均为有头浏览器（visible Chrome）；如需 headless，传入 `--headless`。

### Mode2 厂商表 × 多站点（多 worker，市场串行）

当同时传入 `--excel-input <厂商表.xlsx>` 与 `--sites <站点列表>` 时，CLI 进入**多厂商多站点 mode2** 路径：

- **市场（站点）严格串行**：先跑完一个站点所有厂商的产品，再进入下一个站点。
- **同一站点内多厂商并行**：用 `--workers N`（最多 5）并发多个厂商 worker，同站不同厂商连续跑。
- **唯一输出**：不论 `--workers` 数量，协调器只在所有 worker、所有站点完成后写入**一个**合并 workbook；**每个站点一个 sheet**（大写 site code，如 `HK`/`TW`），每行带 **`厂商` 列**。
- worker 不写厂商分片 XLSX；只回传行数据给协调器。运行目录仅保留最终 workbook（浏览器临时 profile 在结束时清理）。

```bash
/tmp/trip-score-cdp \
  -run-mode mode2 \
  --excel-input /path/to/日常抓取目标.xlsx \
  --sites hk,tw,jp \
  --delay-profile fast \
  --workers 3 \
  --output-dir /path/to/run_dir \
  --no-headless
```

`--excel-input` 的单元格须为 Trip 完整 URL（含 `http` + `trip.com`），列头即厂商标识（如 `TSIM 網址`/`BC`/`TGT`/`FR`/`JY`/`其他`）。若用户提供的是产品 ID，Hermes 应先按「URL 拼接约定」把 ID 转成 URL 再写入厂商表。

### SKU 接口批量价格

`mode1` 会优先复用真实商品页加载期间捕获的 `getProductInfo` 与 `getProductPriceCalendar` 响应：前者提供合法 SKU 叶子、`packageId`、`resourceId` 与销售属性；前者不可用时，从价格日历的 `packageInfos` 建立 SKU 边界，包括尚未报价的资源。价格日历提供原价、折后价、促销和库存。程序按 `packageId + resourceId` 合并，不会用所有属性值的笛卡尔积虚构 SKU。

当当前价格覆盖率达到 100% 时，程序直接使用批量接口价格，并在 JSON 事件中输出 `sku_api_batch_used`、树节点数、已报价数和缺价数；未报价的树节点只计入边界诊断，不伪造价格行。接口缺失、失败、资源 ID 不一致或覆盖率不足 100% 时，输出 `sku_api_batch_fallback` 并继续使用原有 CDP 逐 SKU 交互和价格质量门禁。

商品详情若在加载期间跳转到同站 `/things-to-do/experiences/` 列表页，程序仅从实际列表页正常进入原商品一次，再校验商品 ID；不会将列表页价格作为商品报价。2026-09-18 实测，直接导航可能返回无初始商品数据的 `/xticket` 客户端渲染页，商品信息请求被拦截后会触发香港列表页默认跳转；从真实列表页进入则可取得 `/xttd` 服务端渲染的商品数据。该恢复不修改请求响应或伪造 Referer。

接口字段映射：`priceInfo.originalPrice` → 外显价格，`priceInfo.price` → 实价，`preferentialPriceDetails` → SKU 促销标签。这里的实价是当前公开可预订折后价；结账价仍可能受日期、数量、附加产品或用户权益影响。产品级「总促销标签」与 SKU 级「促销标签」继续分开保存。

## 延迟档位

`--delay-profile` 支持：

```text
fast
normal
safe
slow
```

日常主抓取默认推荐 `fast`，并配合 `--timeout 1800`、`--workers 3`。如果仍出现风控、页面加载慢、SKU 点击失败，低质量补救重跑会自动使用有头 `safe`；不要默认使用 `headless`。

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

## Hermes 项目集成

操作说明已同步到项目：

```text
HERMES.md
hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md
hermes-agent/plugins/trip-manual-delivery-guard/plugin.yaml
hermes-agent/plugins/trip-manual-delivery-guard/__init__.py
```

`HERMES.md` 是项目规范入口，但不是 Hermes 会按文件名自动加载的运行时规则。Hermes 主机必须在 `~/.hermes/config.yaml` 中把项目 Skill 目录加入外部 Skill 搜索路径，同时不得保留同名的 `~/.hermes/skills/` 旧副本，否则本地副本会遮蔽项目版本：

```yaml
skills:
  external_dirs:
    - /Users/example/Develop/trip-score-price-scraper/hermes-agent/skills
```

指定产品抓取还必须启用发送前过滤插件。插件代码只保存在项目内；用户插件目录只放符号链接：

```bash
mkdir -p /Users/example/.hermes/plugins
ln -s \
  /Users/example/Develop/trip-score-price-scraper/hermes-agent/plugins/trip-manual-delivery-guard \
  /Users/example/.hermes/plugins/trip-manual-delivery-guard
hermes plugins enable trip-manual-delivery-guard
hermes plugins list
readlink /Users/example/.hermes/plugins/trip-manual-delivery-guard
```

如果链接已存在，先用 `readlink` 确认它指向上述项目目录，不要复制或覆盖成第二份代码。配置完成后通过 Hermes Dashboard 或主机当前的服务管理方式重启 Gateway，并从 Telegram 新建会话验收。

Hermes Agent 发现 Skill 后，应完整读取其中对应章节。项目不再维护用户目录下的 Skill 或插件代码副本。当修改抓取、比价、cron 或报告逻辑时，应同步更新该 Skill。

### 交付目的地规则（完成时将结果文件发送到指令来源群组）

**铁律：任务完成时，将结果文件（原生 XLSX / ZIP 包等 MEDIA 附件）发送到「发出本次抓取指令的那个会话/群组」，或其显式指定的目标。**

- 若指令来自某群组 A → 把结果文件发送回群组 A（指令来源会话的 origin）。
- 若指令显式指定了其他投递目标 → 投递到该指定目标。
- 不要把结果只保存到本地、投递到无关群组，或忽略交付。
- 该规则覆盖所有抓取完成交付：Mode2 成功/失败定型文、厂商 Excel 模式、cron 结果包、手动单产品抓取。

Hermes 手动抓取单个或数个指定产品时，固定使用项目根目录构建的 `./trip-score-price-scraper`，以重复 `-input` 传入产品，并使用 `-delay-profile fast -product-info -no-quality-rerun -output-dir <manual-dir>`。运营提示词如 `【請抓取產品價格71827107】` 会按产品 ID 构造 URL（遵循上面的「URL 拼接约定」：默认只拼 `/detail/<ID>/`，不追加 `?locale=...&curr=...`，除非用户显式指定货币/站点）。该流程默认有头单 worker 顺序抓取；Cloudflare/风险拦截、未知价格状态或缺少产品名、SKU、货币、价格任一必填数据时，即使已保留原生 Excel 也返回非零退出码。有效成功时只交付原生 XLSX 附件，不发送平文本价格、产品/SKU 摘要或二次整理表。只有失败、部分 SKU 点击失败、反爬/API 拦截、超时/中断、部分产品失败或原生 Excel 校验异常可以显示单行提示。批量和 cron 任务继续使用各自章节的参数。

**手动抓取执行铁律：** Hermes 必须**直接用命令行传参运行** `./trip-score-price-scraper`，**绝不编写/运行任何 shell 包装脚本或临时调用脚本来执行抓取**；一次传参命令若被网关守卫误报（`ValueError: embedded null byte`），用后台方式重发同一条原样命令行即可，不得改写为脚本绕行。抓取成功（exit 0）后**禁止用 openpyxl 等二次读表、也不得 print 出 SKU/价格/评分/行数等明细到群聊**；成功只回原生工作簿路径，CLI 的 `run_done`/exit code 即成功判定。

**结果一定不漏发（强制投递 + 成功回执）：** 抓取成功后的投递必须用项目脚本 `trip_deliver.py` 走 `hermes send` 完成，脚本校验文件存在且非空、调用 `hermes send --to telegram:<chat_id> "MEDIA:<path>"`，**仅当 hermes send 返回 exit 0 才算送达**（写 `.delivered.json` 并 exit 0）；投递失败则写 `.pending_delivery.json` 并返回非零、任务标记为「失败待补发」，绝不静默当作成功。目标 `chat_id` = 发起该次请求的会话/群组。

## 验证

```bash
go test ./...
python3 -m unittest -v tests/test_trip_manual_delivery_guard.py
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
/tmp/trip-score-cdp --help
```

## 维护约定

- 不再恢复 Python CLI / GUI / PyInstaller spec 作为入口。
- 不再新增 Python 抓取主流程。
- Go CDP 程序是唯一动作组件和唯一入口。
- README 是项目唯一主使用文档。
