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
| `--run-mode mode1` | 开启 | 关闭 | 开启 | 开启 | 无头完整抓取。 |
| `--run-mode mode2` | 关闭 | 开启 | 关闭 | 开启 | 有头仅评分抓取。 |

优先级：先应用程序默认值，再应用 `--run-mode`，最后应用显式开关。因此显式开关会覆盖运行模式。

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

- 设置停止标记。
- 对浏览器执行 `window.stop()`。
- 导出已抓取结果。
- 保存 session。
- 关闭浏览器。

## 7. 日志区

GUI 底部显示调试与日志信息。

CLI 默认把日志输出到终端。需要文件日志时使用：

```bash
python3 main.py \
  --urls-file ./urls.txt \
  --log-file "$HOME/Documents/trip_scrape.log"
```

日志文件目录不存在时会自动创建。

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
~/Documents/trip_vendor_results_YYYYMMDD_HHMMSS/
```

厂商 Excel 模式指定输出目录：

```bash
python3 main.py \
  --excel-input ./vendors.xlsx \
  --output-dir "$HOME/Documents/trip_vendor_outputs"
```

厂商模式输出规则：

- 每个厂商生成一个 Excel 文件。
- 文件名格式：`厂商名_YYYYMMDD_HHMMSS.xlsx`。
- 文件名非法字符会替换为 `_`。
- 重复厂商名会追加列号或序号。
- 结果第一列增加 `厂商`。
- 同一产品产生多行 SKU 时，`厂商` 只在该产品系列第一行写入。

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

厂商并发模式中，每个 worker 使用临时 session 文件，避免多个进程同时写同一个 session。任务结束后，会把第一个成功 worker 的 session 复制回指定或默认 session 文件。

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
| `--run-mode {mode1,mode2}` | enum | `None` | 抓取选项预设 | 打包常用抓取参数。 |
| `--browser {chrome}` | enum | `chrome` | 打开浏览器 | 当前只支持 Chrome。 |
| `--headless` / `--no-headless` | flag | 关闭 | 无 | 开启或关闭无头模式。 |
| `--session-file SESSION_FILE` | path | 旧版默认路径 | 自动加载/保存登录状态 | 指定 session 文件。 |
| `--log-file LOG_FILE` | path | `None` | 日志区 | 同步写入日志文件。 |
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
captured <rows> rows -> <output.xlsx>
```

厂商多文件成功输出示例：

```text
captured <rows> rows -> <n> files
  /path/vendor_a.xlsx
  /path/vendor_b.xlsx
```

## 15. 项目结构和实现对应

```text
trip-score-price-scraper/
├── main.py                    # 程序入口
├── gui.py                     # GUI 包装入口
├── cli.py                     # CLI 参数解析
├── service.py                 # CLI/厂商并发批处理编排
├── models.py                  # 请求、结果、ScrapeItem 数据结构
├── trip_get_scoreandprice.pyw # GUI 与 Selenium 抓取核心
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
              ├── 普通模式：单 WebScraperApp 批量抓取
              └── Excel模式：scrape_vendor_batches()
                    ├── load_excel_items()
                    ├── 按厂商分组
                    ├── multiprocessing 启动 worker
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
python3 -m py_compile main.py cli.py service.py models.py trip_get_scoreandprice.pyw
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
- 如果使用 `--headless`，先去掉该参数验证。
- 查看 `--log-file` 中 ChromeDriver / undetected-chromedriver 相关错误。

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
