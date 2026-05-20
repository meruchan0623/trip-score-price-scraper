# Trip.com 评分 + SKU 价格抓取工具

从 Trip.com 商品页面提取评分、评价数、预订数、客户维度评分、商品资讯以及 SKU 组合价格。程序同时提供 GUI 和 CLI。CLI 的设计按 GUI 的功能排布映射，便于从人工操作迁移到 Agent、脚本或定时任务自动化。

## 1. 启动入口

GUI 启动：

```bash
python3 main.py
```

或：

```bash
python3 main.py --gui
```

CLI 启动至少需要一种输入来源：

```bash
python3 main.py --input "https://www.trip.com/..."
```

```bash
python3 main.py --urls-file ./urls.txt
```

```bash
python3 main.py --excel-input ./vendors.xlsx
```

没有输入时返回退出码 `2`。

## 2. 输入区：URL 文本框 / Excel 文件

GUI 输入区包含两种来源：

- 文本框：每行一个 URL。
- Excel 文件：选择厂商链接 Excel，优先于文本框。

CLI 对应参数：

| GUI 功能 | CLI 参数 | 说明 |
| --- | --- | --- |
| 文本框粘贴单条/多条 URL | `--input URL` | 可重复传入多个。 |
| 文本框批量 URL | `--urls-file FILE` | 文本文件，每行一个 URL。 |
| 选择 Excel | `--excel-input FILE` | 第一行是厂商名，第 2 行起是链接。 |
| Excel 并发浏览器数 | `--workers N` | 范围 `1..5`，默认 `min(厂商数, 5)`。 |

普通 URL 文件示例：

```text
https://www.trip.com/...
https://www.trip.com/...
```

厂商 Excel 格式：

```text
| 厂商A                    | 厂商B                    |
| ------------------------ | ------------------------ |
| https://www.trip.com/... | https://www.trip.com/... |
| https://www.trip.com/... |                          |
```

Excel 解析规则：

- 只读取活动工作表。
- 每列第 1 行是厂商名，不作为链接。
- 第 2 行起，单元格文本同时包含 `http` 和 `trip.com` 才作为链接。
- 空单元格跳过。
- 非 Trip 链接跳过并写 warning。

## 3. 抓取选项区

GUI 的“抓取选项”包含三个复选框：

| GUI 选项 | 默认 | CLI 参数 | CLI 行为 |
| --- | --- | --- | --- |
| 仅抓取评分 | 关闭 | `--rating-only` / `--no-rating-only` | 开启或关闭仅评分模式。 |
| 抓取实价（SKU动态价格） | 开启 | `--real-price` / `--no-real-price` | 开启或关闭动态实价抓取。 |
| 抓取商品资讯（设备信息） | 开启 | `--product-info` / `--no-product-info` | 开启或关闭商品资讯抓取。 |

CLI 还提供运行模式，用于打包常用抓取选项：

| 运行模式 | Headless | 仅评分 | 动态实价 | 商品资讯 | 用途 |
| --- | --- | --- | --- | --- | --- |
| 不传 `--run-mode` | 关闭 | 关闭 | 开启 | 开启 | 保持旧默认行为。 |
| `--run-mode mode1` | 开启 | 关闭 | 开启 | 开启 | 无头完整抓取，结果表输出 `实价` 列。 |
| `--run-mode mode2` | 关闭 | 开启 | 关闭 | 开启 | 有头仅评分抓取。 |

优先级：先应用程序默认值，再应用 `--run-mode`，最后应用显式开关。因此显式开关会覆盖运行模式。如果 `mode1` 同时传入 `--no-real-price` 或 `--rating-only`，则不会输出 `实价` 列。

示例：

```bash
python3 main.py --urls-file ./urls.txt --rating-only
```

```bash
python3 main.py --urls-file ./urls.txt --no-real-price --no-product-info
```

```bash
python3 main.py --run-mode mode1 --excel-input ./vendors.xlsx
```

```bash
python3 main.py --run-mode mode2 --no-rating-only --real-price --urls-file ./urls.txt
```

## 4. 浏览器控制区：打开、关闭、并发、Headless

GUI 通过按钮控制浏览器：

- 打开浏览器
- 关闭浏览器
- Excel 模式下指定并发浏览器数

CLI 自动完成浏览器生命周期：

- 任务开始时自动打开 Chrome。
- 任务结束后自动关闭 Chrome。
- 普通模式默认单浏览器。
- 厂商 Excel 模式按厂商并发，多进程启动多个独立 Chrome，最多 5 个。
- CLI 会在启动 worker 前预热 `undetected_chromedriver`，随后把 chromedriver 复制到每个 worker 目录，避免并发 worker 触碰同一个 driver 文件。
- 每个厂商 worker 都有独立 `_workers/<厂商>/items.json`、`result.json`、`session.json`、`profile/`、`driver/` 和 `stop.request`。
- CLI 会自动优先使用 `certifi` 配置 `SSL_CERT_FILE`，避免 Python 新机证书链缺失导致 driver 下载失败。

CLI 参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--browser chrome` | `chrome` | 当前只支持 Chrome。 |
| `--headless` | 关闭 | 使用 Chrome headless 模式。 |
| `--workers N` | `min(厂商数, 5)` | 厂商 Excel 模式并发浏览器数。 |
| `--run-mode mode1` | 无 | 无头完整抓取。 |
| `--run-mode mode2` | 无 | 有头仅评分抓取。 |
| `--timeout SECONDS` | `300` | 等待浏览器启动完成的最长秒数。 |

示例：

```bash
python3 main.py --excel-input ./vendors.xlsx --workers 3 --headless
```

Headless 会向 Chrome 注入：

```text
--headless=new
--window-size=1920,1080
--disable-gpu
```

如果 Trip 页面在 headless 下出现验证码、空结果或启动失败，去掉 `--headless`，改用可见 Chrome。

Mac 新机首次运行时，如果日志出现 `CERTIFICATE_VERIFY_FAILED`，先执行：

```bash
/Applications/Python\ 3.13/Install\ Certificates.command
```

当前 CLI 也会自动尝试使用 `certifi` 证书路径；如果 driver 已经缓存，后续启动会跳过联网准备，通常明显更快。需要压缩多浏览器启动等待时，可在 driver 缓存成功后使用：

```bash
python3 main.py --excel-input ./vendors.xlsx --workers 5 --startup-stagger 0
```

## 5. 浏览器内存保护

GUI 默认启用浏览器内存保护：

- Chrome RSS 超过 8192MB 时重启。
- 每处理 20 个 URL 自动重启。

CLI 参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--browser-memory-limit-mb N` | `8192` | 管理中的 Chrome 进程 RSS 超过 N MB 后重启。 |
| `--browser-restart-every N` | `20` | 每处理 N 个 URL 后重启 Chrome。 |
| `--disable-browser-memory-guard` | 关闭 | 禁用内存保护和自动重启。 |

示例：

```bash
python3 main.py \
  --urls-file ./urls.txt \
  --browser-memory-limit-mb 4096 \
  --browser-restart-every 10
```

## 6. 开始执行 / 暂停 / 停止

GUI 通过按钮控制：

- 开始执行
- 暂停执行 / 恢复执行
- 停止执行

CLI 对应行为：

| GUI 操作 | CLI 等价 |
| --- | --- |
| 开始执行 | 执行 `python3 main.py ...` |
| 暂停/恢复 | `SIGUSR1`，第一次暂停，下一次恢复 |
| 停止执行 | `Ctrl+C` / `SIGINT` / `SIGTERM` |

信号示例：

```bash
kill -USR1 <pid>
kill -TERM <pid>
```

停止时程序会尽量：

- 写入 worker 自己的 `stop.request` 文件。
- 导出已抓取结果。
- 保存 session。
- 关闭浏览器。

CLI 中断采用两段式退出：

- 第一次 `Ctrl+C` / `SIGINT` / `SIGTERM`：写入每个活跃 worker 的 `stop.request`，等待 worker 自行停止、导出和关闭浏览器。
- 第二次 `Ctrl+C` / `SIGINT` / `SIGTERM`：升级为强制停止，终止剩余 worker 进程树。
- 如果第一次中断后超过 `--stop-grace-seconds`，程序会自动升级为强制终止剩余 worker 进程树。

## 7. 日志区

GUI 底部显示调试与日志信息。

CLI 的输出分为三层：

- stdout：任务开始、进度、结果路径、简短 warning。
- stderr：参数错误、最终失败摘要、厂商失败摘要。
- `--log-file`：可选摘要日志，只记录启动、完成、失败和 warning；不会记录 SKU 点击、价格读取、worker 细节或浏览器噪声。未指定时不写文本日志。

确实需要摘要日志时使用：

```bash
python3 main.py \
  --urls-file ./urls.txt \
  --log-file "$HOME/Documents/trip_scrape.log"
```

日志文件目录不存在时会自动创建。只有显式传入 `--log-file` 时，终端失败摘要才会带上日志路径。

### 7.1 终端输出模式

CLI 默认使用 `normal` 输出，适合人工看进度，也适合 Agent 抓取摘要。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--log-level quiet` | 否 | 只输出开始和最终结果，适合 cron/nohup。 |
| `--log-level normal` | 是 | 输出任务总览、厂商进度、warning 和结果路径。 |
| `--log-level verbose` | 否 | 额外输出 URL/浏览器阶段日志；SKU 逐项调试日志默认不输出。 |
| `--json-lines` | 关闭 | 每行输出一个 JSON event，适合 Agent/脚本解析。 |
| `--no-color` | 关闭 | 禁用 ANSI 颜色；当前默认也不主动输出颜色。 |

普通人类可读输出：

```bash
python3 main.py \
  --excel-input ./vendors.xlsx \
  --workers 3 \
  --log-file "$HOME/Documents/trip_scrape.log"
```

安静模式：

```bash
python3 main.py \
  --excel-input ./vendors.xlsx \
  --log-level quiet \
  --log-file "$HOME/Documents/trip_scrape.log"
```

Agent 推荐模式：

```bash
python3 main.py \
  --run-mode mode1 \
  --excel-input ./vendors.xlsx \
  --workers 3 \
  --json-lines \
  --no-color \
  --log-file "$HOME/Documents/trip_scrape.log"
```

`--json-lines` 示例：

```json
{"event":"run_started","vendor_count":5,"url_count":327,"workers":5,"headless":false}
{"event":"driver_prepare_started","stage":"driver_prepare","message":"preparing chromedriver"}
{"event":"driver_prepare_done","stage":"driver_prepare","driver_reused":true,"elapsed_ms":28}
{"event":"worker_spawn_started","vendor":"BC","worker_id":1,"message":"spawning worker"}
{"event":"browser_start_started","vendor":"BC","worker_id":1,"message":"starting Chrome"}
{"event":"browser_start_done","vendor":"BC","worker_id":1,"elapsed_ms":7321}
{"event":"vendor_started","vendor":"BC","url_count":57}
{"event":"url_started","vendor":"BC","worker_id":1,"url_index":12,"url_total":57,"url":"https://www.trip.com/..."}
{"event":"url_done","vendor":"BC","worker_id":1,"url_index":12,"url_total":57,"rows":1,"elapsed_ms":18300,"url":"https://www.trip.com/..."}
{"event":"vendor_done","vendor":"BC","success":true,"rows":214,"output_path":"/Users/example/BC_260519-1538.xlsx"}
{"event":"run_done","success":true,"exit_code":0,"processed_count":830,"output_paths":["/Users/example/BC_260519-1538.xlsx"]}
```

约定：

- 最后一行 JSON event 固定是 `run_done`。
- `run_done.exit_code` 与进程退出码一致。
- 输出路径使用绝对路径。
- 多进程 worker 不直接向终端输出；主进程统一渲染。
- 终端不直接打印 traceback；worker 失败摘要会写入轻量 `result.json`，显式传入 `--log-file` 时也会写入摘要日志。
- CLI worker 不初始化 Tk，因此 macOS 程序坞不会因为 worker 出现 Python 小火箭图标。

## 8. 结果预览与导出

GUI 中间区域实时预览结果，任务结束自动导出，也可手动点击“导出到Excel”。

CLI 无界面预览，任务结束自动导出。

普通模式默认输出：

```text
~/Documents/trip_scoreandprice_results_YYYYMMDD_HHMMSS.xlsx
~/Documents/trip_score_results_YYYYMMDD_HHMMSS.xlsx
```

普通模式指定输出：

```bash
python3 main.py \
  --urls-file ./urls.txt \
  --output "$HOME/Documents/trip_result.xlsx"
```

厂商 Excel 模式默认输出目录：

```text
~/Documents/trip_vendor_results/YYMMDD-HHMM/
```

厂商 Excel 模式指定输出目录：

```bash
python3 main.py \
  --excel-input ./vendors.xlsx \
  --output-dir "$HOME/Documents/trip_vendor_outputs"
```

厂商模式输出规则：

- 每个厂商生成一个 Excel 文件。
- 文件名格式：`厂商名_YYMMDD-HHMM.xlsx`。
- 文件名非法字符会替换为 `_`。
- 重复厂商名会追加列号或序号。
- 结果第一列增加 `厂商`。
- 同一产品产生多行 SKU 时，`厂商` 只在该产品系列第一行写入。
- 中断时已完成 URL 的结果会尽量写入同一目录下的厂商文件。

## 9. 输出字段

默认模式工作表名：

```text
Trip商品数据
```

默认字段：

```text
商品ID, URL, 商品名称, SKU层级1..N, 货币, 外显价格, 实价, 促销标签,
预定数, 评价数, 评分, 客户服务, 方便程度, 讯号状态, 中差评价数, 商品资讯
```

厂商 Excel 模式会在第一列增加：

```text
厂商
```

仅评分模式工作表名：

```text
Trip评分数据
```

仅评分字段：

```text
商品ID, URL, 商品名称,
预定数, 评价数, 评分, 客户服务, 方便程度, 讯号状态, 中差评价数, 商品资讯
```

字段随参数变化：

- `--rating-only` 不输出 SKU、货币、外显价格、实价、促销标签。
- `--no-real-price` 不输出 `实价`。
- `--no-product-info` 不输出 `商品资讯`。
- SKU 层级列按实际最大 SKU 层数动态生成。

## 10. 清空数据 / 重跑

GUI 有“清空数据”按钮。

CLI 没有内存中的界面状态，不需要清空。重跑同一命令即可。

注意：

- 普通模式默认文件名带时间戳，不会覆盖旧文件。
- 厂商模式默认输出目录带时间戳，不会覆盖旧目录。
- 如果显式传入 `--output` 或 `--output-dir`，需要自行确认是否允许覆盖或混入旧结果。

## 11. Session 登录态

GUI 和 CLI 都复用旧版 `SessionManager`。

行为：

- 浏览器启动后自动尝试加载 session。
- 任务结束后自动保存 session。
- `--session-file FILE` 可指定 session 文件。
- 不指定时使用旧版默认路径。

示例：

```bash
python3 main.py \
  --urls-file ./urls.txt \
  --session-file "$HOME/Documents/trip_session.json"
```

厂商并发模式中，每个 worker 使用自己目录下的 `session.json` 和 Chrome `profile/`，避免多个进程同时写同一个 session 或 profile。任务结束后，会把第一个成功 worker 的 session 复制回指定或默认 session 文件。

CLI 不提供“打开浏览器后等待人工登录”的流程。需要登录态时，先通过 GUI 或已有 session 文件准备登录状态。

## 12. 完整参数表

| 参数 | 类型/取值 | 默认值 | GUI 对应功能 | 说明 |
| --- | --- | --- | --- | --- |
| `-h`, `--help` | flag | 关闭 | 无 | 显示帮助并退出。 |
| `--input INPUT` | string，可重复 | `[]` | URL 文本框 | 单个产品链接。 |
| `--urls-file URLS_FILE` | path | `None` | URL 文本框批量输入 | 每行一个链接。 |
| `--excel-input EXCEL_INPUT` | path | `None` | 选择Excel | 厂商链接 Excel。 |
| `--output OUTPUT` | path | 自动生成 | 导出到Excel | 普通模式输出文件。 |
| `--output-dir OUTPUT_DIR` | path | 自动生成 | 厂商模式导出目录 | 厂商多文件输出目录。 |
| `--workers WORKERS` | int, `1..5` | `min(厂商数, 5)` | 并发浏览器数 | 厂商模式并发数量。 |
| `--startup-stagger STARTUP_STAGGER` | float | `0` | 并发浏览器数 | 多 worker 启动错峰秒数，默认不启用错峰。 |
| `--stop-grace-seconds STOP_GRACE_SECONDS` | int | `30` | 停止执行 | 收到停止信号后等待 worker 导出部分结果的秒数。 |
| `--run-mode {mode1,mode2}` | enum | `None` | 抓取选项预设 | 打包常用抓取参数。 |
| `--browser {chrome}` | enum | `chrome` | 打开浏览器 | 当前只支持 Chrome。 |
| `--headless` / `--no-headless` | flag | 关闭 | 无 | 开启或关闭无头模式。 |
| `--session-file SESSION_FILE` | path | 旧版默认路径 | 自动加载/保存登录状态 | 指定 session 文件。 |
| `--log-file LOG_FILE` | path | `None` | 日志区 | 可选摘要日志；不写 SKU 逐项日志或 worker 噪声。 |
| `--log-level {quiet,normal,verbose}` | enum | `normal` | 日志区 | 控制终端输出详细程度。 |
| `--json-lines` | flag | 关闭 | 日志区 | 输出机器可解析 JSON event。 |
| `--no-color` | flag | 关闭 | 日志区 | 关闭颜色输出，便于日志系统消费。 |
| `--timeout TIMEOUT` | int | `300` | 打开浏览器等待 | 浏览器启动超时秒数。 |
| `--browser-memory-limit-mb N` | int | `8192` | 浏览器内存保护 | RSS 超阈值重启。 |
| `--browser-restart-every N` | int | `20` | 浏览器内存保护 | 每处理 N 个 URL 重启。 |
| `--disable-browser-memory-guard` | flag | 关闭 | 浏览器内存保护 | 禁用自动重启。 |
| `--rating-only` / `--no-rating-only` | flag | 关闭 | 仅抓取评分 | 开启或关闭仅评分模式。 |
| `--real-price` / `--no-real-price` | flag | 开启 | 抓取实价 | 开启或关闭实价抓取。 |
| `--product-info` / `--no-product-info` | flag | 开启 | 抓取商品资讯 | 开启或关闭商品资讯抓取。 |

## 13. 最小命令示例

普通单链接：

```bash
python3 main.py --input "https://www.trip.com/..."
```

普通批量：

```bash
python3 main.py --urls-file ./urls.txt
```

厂商 Excel：

```bash
python3 main.py --excel-input ./vendors.xlsx
```

厂商 Excel + 5 浏览器 headless：

```bash
python3 main.py --excel-input ./vendors.xlsx --workers 5 --headless
```

运行模式一，无头完整抓取：

```bash
python3 main.py --run-mode mode1 --excel-input ./vendors.xlsx
```

运行模式二，有头仅评分：

```bash
python3 main.py --run-mode mode2 --urls-file ./urls.txt
```

运行模式二，但显式改回完整 SKU/实价抓取：

```bash
python3 main.py --run-mode mode2 --no-rating-only --real-price --urls-file ./urls.txt
```

## 14. 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功抓取并导出。 |
| `2` | 参数或输入错误。 |
| `4` | 浏览器启动、抓取、导出等运行失败。 |
| `130` | 收到停止信号，任务中断。 |

成功输出示例：

```text
run started vendors=1 urls=12 workers=1 headless=False mode=default output=/Users/example/trip_scoreandprice_results_20260519_120000.xlsx
captured <rows> rows -> <output.xlsx>
```

厂商多文件成功输出示例：

```text
run started vendors=5 urls=327 workers=5 headless=False mode=default output=/Users/example/Documents/trip_vendor_results/260519-1538
[BC] spawning worker
[BC] starting Chrome
[BC] Chrome started
[BC] url started 12/57 url=https://www.trip.com/...
[BC] url done 12/57 rows=1 elapsed=18.3s url=https://www.trip.com/...
progress urls=128/327 vendors=3/5 active=BC:12/57:done,TGT:8/63:open failed=0
[BC] done urls=57/57 rows=214 output=/Users/example/Documents/trip_vendor_results/260519-1538/BC_260519-1538.xlsx
captured <rows> rows -> <n> files
  /path/vendor_a.xlsx
  /path/vendor_b.xlsx
```

失败输出示例：

```text
[FR] chrome detect failed stage=chrome_detect error="Google Chrome not found"
[FR] worker failed stage=chrome_detect exitcode=4 error="Google Chrome not found"
[FR] failed urls=0/95 rows=0 output=/Users/example/Documents/trip_vendor_results/260519-1538/FR_260519-1538.xlsx reason="Google Chrome not found"
failed: FR: Google Chrome not found
```

如果 worker 进程退出但没有发出 `vendor_done`，主进程会按失败处理，不会再输出 `captured 0 rows -> ...` 的假成功。

中断保存示例：

```text
stop requested; waiting for partial exports grace=30s
[BC] partial export rows=18 output=/Users/example/Documents/trip_vendor_results/260519-1538/BC_260519-1538.xlsx
captured 18 rows before failure -> 1 files
failed: stopped by signal
```

第二次中断或宽限期超时会出现：

```text
force stop requested; terminating remaining browser work
```

## 15. 项目结构和实现对应

```text
trip-score-price-scraper/
├── main.py                    # 程序入口
├── gui.py                     # GUI 包装入口
├── cli.py                     # CLI 参数解析
├── service.py                 # CLI/厂商并发批处理编排（不依赖Tk/GUI）
├── models.py                  # 请求、结果、ScrapeItem 数据结构
├── trip_cli_scraper.py        # CLI-only Selenium 抓取运行器
├── session_store.py           # CLI session 加载/保存
├── xlsx_export.py             # CLI Excel 导出
├── trip_get_scoreandprice.pyw # 旧 GUI 入口（CLI 不依赖）
├── requirements.txt           # Python 依赖
└── trip_get_scoreandprice.spec # PyInstaller 构建配置
```

核心调用链：

```text
main.py
  └── cli.run_cli()
        ├── 解析 CLI 参数
        ├── 构造 TaskRequest
        └── service.scrape_xxx()
              ├── 普通模式：单 TripCliScraper 批量抓取
              └── Excel模式：scrape_vendor_batches()
                    ├── load_excel_items()
                    ├── 按厂商分组
                    ├── subprocess 启动独立 worker
                    ├── 每个 worker 使用独立 session/profile/driver/stop 文件
                    ├── 每个 worker 创建独立 TripCliScraper 和 Chrome
                    └── 每厂商独立导出 Excel
```

## 16. 安装、构建和验证

安装依赖：

```bash
python3 -m pip install -r requirements.txt
```

构建：

```bash
pyinstaller trip_get_scoreandprice.spec
```

静态检查：

```bash
python3 -m py_compile main.py cli.py service.py models.py trip_cli_scraper.py session_store.py xlsx_export.py
```

参数检查：

```bash
python3 main.py --help
python3 main.py --excel-input ./vendors.xlsx --workers 0
python3 main.py --excel-input ./vendors.xlsx --workers 6
```

`--workers 0` 和 `--workers 6` 应返回退出码 `2`。

## 17. 故障排查

没有输入：

```text
failed: At least one URL is required via --input, --urls-file, or --excel-input.
```

处理：传入 `--input`、`--urls-file` 或 `--excel-input`。

浏览器启动失败：

- 确认已安装 Google Chrome。
- 新 Mac 或官方 Python 运行时若出现 `CERTIFICATE_VERIFY_FAILED`，执行 `/Applications/Python\ 3.13/Install\ Certificates.command` 后重试。
- 如果安装了 `certifi`，CLI 会自动设置 `SSL_CERT_FILE` 并在 `driver_prepare_done` 中记录证书来源。
- 首次运行会下载/patch chromedriver，之后复用 `~/Library/Application Support/undetected_chromedriver/` 下的缓存。
- 如果使用 `--headless`，先去掉该参数验证。
- 使用 `--json-lines` 或显式 `--log-file` 查看 ChromeDriver / undetected-chromedriver 相关失败摘要。

抓取结果为空：

- 确认 URL 是 Trip.com 产品页。
- 确认 session 登录态有效。
- 使用可见 Chrome 观察是否出现验证码、登录弹窗或区域弹窗。

厂商 Excel 没有结果：

- 确认厂商名在第 1 行。
- 确认链接从第 2 行开始。
- 确认链接文本包含 `http` 和 `trip.com`。

导出失败：

- 确认 `--output` 或 `--output-dir` 有写入权限。
- 确认目标 Excel 文件没有被 Excel/WPS 打开占用。

## 18. Go + CDP 独立 CLI：trip-score-cdp

仓库额外提供一个独立 Go 命令 `trip-score-cdp`。它不替换 `python3 main.py`，也不影响 GUI；Python CLI/GUI 仍按本文前面章节使用。

Go CLI 当前用于验证和运行 Go-native 抓取链路：patched chromedriver + Chrome subprocess + WebDriver attach 负责浏览器启动与生命周期，Rod/CDP 继续负责 Trip 页面、SKU 遍历、价格、实价和促销标签读取。它也提供 `--json-lines`、Excel 多厂商 worker、可取消中断、延迟档位等面向批量运行的能力。

Go CLI 默认结果统一写入 `~/Documents/trip_vendor_results/YYMMDD-HHMM/`，默认不写 `run.log`；长期 session 与 managed profile 统一维护在 `~/Documents/trip_vendor_results/_state/`，第二次运行会自动复用。Go CLI 不再从本机日常 Chrome profile 复制完整 User Data，Windows 上会避免把数 GB 的缓存、扩展和组件文件复制到每个 worker。

快速运行：

```bash
go run ./cmd/trip-score-cdp \
  --real-price \
  --product-info \
  --input "https://hk.trip.com/things-to-do/detail/92237157/" \
  --delay-profile fast \
  --no-headless \
  --json-lines
```

构建成独立二进制：

```bash
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
/tmp/trip-score-cdp --help
```

完整 Go CLI 操作文档、推荐参数、延迟档位、UC/profile/session 说明和故障排查见 [go-readme.md](go-readme.md)。
