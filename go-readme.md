# Go CLI 使用手册：trip-score-cdp

`trip-score-cdp` 是本仓库内独立的 Go CLI。它用于以 Go-native 方式运行 Trip.com 商品评分、SKU、外显价格、实价和促销标签抓取；不替换 Python GUI/CLI，也不改变 `python3 main.py ...` 的行为。

当前浏览器链路为：Go 准备 patched chromedriver，直接启动 Chrome subprocess，再由 chromedriver 通过 `debuggerAddress` attach 到该 Chrome，最后由 Rod/CDP 执行页面抓取。WebDriver 只负责 UC attach 和生命周期，DOM、SKU、价格读取仍走 Rod/CDP。

程序运行期间会自动启用系统保活，避免长任务因系统空闲休眠、显示器休眠或自动锁屏相关策略中断：macOS 使用系统 `caffeinate`，Windows 使用 `SetThreadExecutionState`。该行为默认启用，不需要额外参数；任务结束后自动释放。

## 1. 快速启动

以下命令均使用有头 Chrome、JSON lines 输出、命令行 verbose 日志、不写 `run.log` / `--log-file`，并使用默认输出目录 `~/Documents/trip_vendor_results/YYMMDD-HHMM/`。

### 场景 1：导入单厂商或多厂商 Excel，抓实价和商品资讯，写入 mode1

`mode1` 等价于：有头完整抓取、抓 SKU/外显价/实价/促销标签/商品资讯，默认延迟 `normal`。Excel 厂商模式建议直接使用最大 worker 数 `5`，程序会按厂商列并发并保持 worker profile/session 隔离。

```bash
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp

/tmp/trip-score-cdp \
  --run-mode mode1 \
  --excel-input /Users/example/Downloads/scrape.xlsx \
  --workers 5 \
  --json-lines \
  --log-level verbose
```

显式展开写法：

```bash
/tmp/trip-score-cdp \
  --no-headless \
  --no-rating-only \
  --real-price \
  --product-info \
  --delay-profile normal \
  --excel-input /Users/example/Downloads/scrape.xlsx \
  --workers 5 \
  --json-lines \
  --log-level verbose
```

### 场景 2：导入单厂商或多厂商 Excel，只抓评分和商品资讯，写入 mode2

`mode2` 等价于：有头、仅评分/评价/商品资讯、不抓 SKU 和价格、不抓实价，默认延迟 `fast`。

```bash
/tmp/trip-score-cdp \
  --run-mode mode2 \
  --excel-input /Users/example/Downloads/scrape.xlsx \
  --workers 5 \
  --json-lines \
  --log-level verbose
```

显式展开写法：

```bash
/tmp/trip-score-cdp \
  --no-headless \
  --rating-only \
  --no-real-price \
  --product-info \
  --delay-profile fast \
  --excel-input /Users/example/Downloads/scrape.xlsx \
  --workers 5 \
  --json-lines \
  --log-level verbose
```

### 场景 3：导入单条或多条 Trip 产品 URL，延迟 normal

单条 URL 的 mode1 等价命令：

```bash
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --input "https://hk.trip.com/things-to-do/detail/92237157/" \
  --json-lines \
  --log-level verbose
```

单条 URL 的 mode2 等价命令：

```bash
/tmp/trip-score-cdp \
  --run-mode mode2 \
  --delay-profile normal \
  --input "https://hk.trip.com/things-to-do/detail/92237157/" \
  --json-lines \
  --log-level verbose
```

多条 URL 可重复传入 `--input`，或使用 `--urls-file`：

```bash
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --urls-file /Users/example/Downloads/trip_urls.txt \
  --json-lines \
  --log-level verbose
```

普通 URL 输入模式当前使用单浏览器顺序执行；`--workers 5` 只适用于 `--excel-input` 厂商列并发。若需要多 URL 并发，请把 URL 放入 Excel 的多个厂商列后使用场景 1 或场景 2。

HK 商品页会在浏览器实际导航前自动补齐 `locale=zh-hk&curr=HKD`。日志中 `url` 保留原始输入，`first_trip_navigation_url` 和 `final_url` 记录真实打开的 HK URL；正常情况下不应再出现 `www.trip.com -> sg.trip.com -> hk.trip.com` 的首跳链路。

## 2. 推荐参数组合

| 场景 | 推荐命令核心参数 | 说明 |
| --- | --- | --- |
| Excel 完整抓取 | `--run-mode mode1 --excel-input FILE --workers 5 --json-lines --log-level verbose` | 抓 SKU、外显价、实价、促销标签和商品资讯；mode1 默认有头、`normal` 延迟。 |
| Excel 评分资讯 | `--run-mode mode2 --excel-input FILE --workers 5 --json-lines --log-level verbose` | 只抓评分、评价维度和商品资讯；mode2 默认有头、`fast` 延迟。 |
| URL 完整抓取 | `--run-mode mode1 --input URL --json-lines --log-level verbose` | 单条或多条 URL 顺序抓取，默认输出到当次 run folder。 |
| URL 评分资讯 | `--run-mode mode2 --delay-profile normal --input URL --json-lines --log-level verbose` | URL 场景如需按本节要求使用 normal 延迟，可显式覆盖 mode2 的 fast 默认。 |
| 风控或页面异步不稳 | `--delay-profile slow --workers 1 --json-lines --log-level verbose` | 降低页面打开、滚动、SKU 点击和取价频率。 |
| 机器解析日志 | `--json-lines --no-color --log-level verbose` | 每行一个 JSON event，最后一行为 `run_done`；默认不写日志文件。 |

遇到 Trip 页面弹验证、空结果、SKU 容器不存在但页面肉眼可见 SKU 时，优先降低并发并改用 `--delay-profile safe` 或 `--delay-profile slow` 复测。

不建议把 `fast` 作为大批量 SKU 实价/促销标签校验的默认档位。`fast` 适合 mode2 评分资讯抓取；SKU 实价读取要优先使用 `normal/safe`，风控明显时用 `slow`。

## 3. 延迟档位

当前共有 4 档延迟档位：`fast`、`normal`、`safe`、`slow`。`--delay-profile` 默认是 `normal`。所有延迟单位都是秒，并带随机抖动。

| 档位 | 推荐用途 |
| --- | --- |
| `fast` | 最快档，用于短 smoke test、确认浏览器启动、HK 首跳和基础导出。 |
| `normal` | 默认档，用于小样本日常验证。 |
| `safe` | 稳定档，使用原 `fast` 数值，推荐用于 SKU、实价和促销标签验证。 |
| `slow` | 保守档，使用原 `normal` 数值，用于风控、页面异步慢或结果不稳定时。 |

| 档位 | page | scroll | sku click | sku price | between SKU |
| --- | --- | --- | --- | --- | --- |
| `fast` | `0.4-0.9` | `0.1-0.25` | `0.08-0.2` | `0.15-0.35` | `0.08-0.18` |
| `normal` | `0.8-1.5` | `0.2-0.45` | `0.15-0.35` | `0.25-0.55` | `0.12-0.3` |
| `safe` | `1.5-3` | `0.3-0.7` | `0.25-0.6` | `0.4-0.8` | `0.2-0.5` |
| `slow` | `1.5-3` | `0.6-1.2` | `0.6-1.2` | `0.8-1.6` | `0.4-0.9` |

可用具体参数覆盖某一类延迟：

```bash
/tmp/trip-score-cdp \
  --delay-profile safe \
  --page-delay-min 1.2 \
  --page-delay-max 2 \
  --sku-click-delay-min 0.4 \
  --sku-click-delay-max 0.8 \
  --sku-price-delay-min 0.6 \
  --sku-price-delay-max 1 \
  --input "https://hk.trip.com/things-to-do/detail/92237157/" \
  --no-headless
```

覆盖参数默认值是 `-1`，表示使用档位默认值。传入负数、小于 `-1` 的值、或 `min > max` 会返回参数错误。

## 4. 输入、输出和字段

输入来源：

| 参数 | 说明 |
| --- | --- |
| `--input URL` | 产品 URL，可重复传入。 |
| `--urls-file FILE` | 文本文件，每行一个产品 URL。 |
| `--excel-input FILE` | 厂商 Excel，第一行是厂商名，第 2 行起是链接。 |

Excel 读取规则：

- 只读取活动工作表。
- 每列第 1 行是厂商名。
- 第 2 行起，单元格文本同时包含 `http` 和 `trip.com` 才作为链接。
- 非 Trip 链接跳过并写 warning。

输出规则：

| 参数 | 说明 |
| --- | --- |
| `--output FILE` | 普通模式输出文件；单厂商 Excel 模式也可使用。 |
| `--output-dir DIR` | 厂商 Excel 多文件输出目录。 |
| 未指定输出 | 单 URL、`--urls-file`、Excel 厂商模式都会写到 `~/Documents/trip_vendor_results/YYMMDD-HHMM/`；普通模式文件名是 `trip_scoreandprice_results_YYMMDD-HHMM.xlsx`，仅评分模式是 `trip_score_results_YYMMDD-HHMM.xlsx`。 |

默认完整字段：

```text
商品ID, URL, 商品名称, SKU层级1..N, 货币, 外显价格, 实价, 促销标签,
预定数, 评价数, 评分, 客户服务, 方便程度, 讯号状态, 中差评价数, 商品资讯
```

字段变化：

- 厂商 Excel 模式在第一列增加 `厂商`。
- `--rating-only` 不输出 SKU、货币、外显价格、实价、促销标签。
- `--no-real-price` 不输出 `实价`。
- `--no-product-info` 不输出 `商品资讯`。
- SKU 层级列按本次抓取到的最大 SKU 层级动态生成。

## 5. SKU、价格、实价和促销标签语义

- SKU 遍历使用动态递归模型：进入每一层时重新读取当前 DOM 可用选项，点击、递归、取价，再回溯取消选择。
- 外显价格来自当前组合的 `display_price` bundle；为空时才调用外显价 fallback。
- 实价只在 `--real-price` 开启且输出含 `实价` 列时读取；SKU 路径中不会用外显价回填实价。
- 促销标签过滤价格文本，只保留含 `減`、`%`、`折` 的可见文本。
- 每个 SKU 组合取价前会先应用 `sku price` 延迟，再等待 footer 价格区稳定，降低实价和促销标签落到下一个 SKU 的风险。
- 没有 SKU 容器时，输出 `SKU层级1=无SKU选项`，外显价走默认价格读取；开启实价时无 SKU fallback 的 `实价` 与外显价一致。
- 如果当前页面结构、登录态或风控导致 Trip 不返回 SKU 容器，日志会出现 `SKU container not found; product may have no SKU options`。这不一定是程序错误，需要结合可见浏览器页面判断。

## 6. 浏览器、UC、profile 和 session

启动模型：

1. 检测本机 Google Chrome 和主版本。
2. 在 undetected-chromedriver cache 目录准备 patched chromedriver。
3. 使用 Go CLI 自己维护的 managed profile seed，并复制出 worker 专用 profile，避免直接占用默认 Chrome profile 的 remote debugging 限制。
4. 直接启动 Chrome subprocess，附带 `--remote-debugging-host=127.0.0.1`、`--remote-debugging-port`、`--disable-blink-features=AutomationControlled`、`--start-maximized` 等参数。
5. 启动 chromedriver service，并通过 `goog:chromeOptions.debuggerAddress` attach 到已有 Chrome。
6. Rod 连接同一个 CDP endpoint，执行页面抓取。

cache 和兼容：

- macOS cache 目录默认是 `~/Library/Application Support/undetected_chromedriver/`。
- patched driver 文件名按 Chrome major 区分，例如 `undetected_chromedriver_148`。
- Chrome 115+ 使用 Chrome for Testing JSON 解析下载地址。
- Chrome 114 及以下使用 legacy chromedriver storage 的 `LATEST_RELEASE_<major>` 路径。
- 复用 driver 前会检查 patched marker、`chromedriver --version` 和 major 是否匹配。

profile 和 session：

- 长期状态统一放在 `~/Documents/trip_vendor_results/_state/`：

```text
~/Documents/trip_vendor_results/_state/
├── session.json
└── profile/
```

- 第二次运行会自动复用 `_state/session.json` 与 `_state/profile/`。
- 如果 `_state/` 不存在，Go CLI 会扫描最近的 `~/Documents/trip_vendor_results/YYMMDD-HHMM/_workers/*/`，把历史 worker 的 `session.json` 和 `profile/` 迁移为 `_state/`。
- 每次运行期间仍使用当前 run folder 下的 `_workers/<id_vendor>/session.json` 和 `_workers/<id_vendor>/profile/` 做隔离；任务结束后统合回 `_state/`，成功后清理 `_workers/`。
- 默认以 `_state/profile/` 作为 profile seed；首次运行没有 `_state/profile/` 时创建一个干净的 Go CLI 专用 profile，让 Chrome/CDP 自行积累登录态。
- 如果检测到旧版从本机 Chrome 复制来的大型 `_state/profile/`，首次运行会把它重命名为 `profile_legacy_*` 备份，然后创建新的 managed profile，后续运行直接复用轻量状态。
- profile copy 会跳过 `Singleton*`、Crashpad、BrowserMetrics、journal、wal、shm、socket、symlink、Cache、Code Cache、扩展目录和 Chrome 组件缓存等易损或膨胀文件。
- session JSON 是 Python 兼容的补充层，结构为：

```json
{
  "cookies": [],
  "local_storage": {},
  "session_storage": {}
}
```

- 默认 session 路径：`~/Documents/trip_vendor_results/_state/session.json`。
- 旧 Python/GUI session 路径仍可作为第一次状态初始化的兼容来源；初始化后 Go CLI 使用 `_state/session.json`。
- 可用 `--session-file FILE` 指定 session JSON。
- 显式 `--session-file FILE` 时只读写用户指定的 session 文件，不覆盖 `_state/session.json`；profile 仍使用 `_state/profile/`。
- session 加载失败是 warning，不会触发浏览器 fallback，也不会阻止抓取。

## 7. 可用参数总表

以下表格按当前 `go run ./cmd/trip-score-cdp --help` 对齐。

### 输入与输出

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--input URL` | 空 | 产品 URL，可重复传入。 |
| `--urls-file FILE` | 空 | 每行一个产品 URL 的文本文件。 |
| `--excel-input FILE` | 空 | 厂商 Excel，第一行厂商名，第 2 行起是链接。 |
| `--output FILE` | 自动生成 | Excel 输出文件。 |
| `--output-dir DIR` | 自动生成 | 厂商 Excel 多文件输出目录。 |

### 抓取选项

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--rating-only` / `--no-rating-only` | 关闭 | 开启或关闭仅评分模式。 |
| `--real-price` / `--no-real-price` | 开启 | 开启或关闭动态实价抓取。 |
| `--product-info` / `--no-product-info` | 开启 | 开启或关闭商品资讯抓取。 |
| `--run-mode mode1` | 空 | 有头完整抓取：非仅评分、抓实价、抓商品资讯；默认 `normal` 延迟。 |
| `--run-mode mode2` | 空 | 有头评分资讯抓取：仅评分、不抓实价、抓商品资讯；未显式传 `--delay-profile` 时默认 `fast` 延迟。 |

显式开关优先级最高：先应用默认值，再应用 `--run-mode`，最后应用 `--headless`、`--rating-only`、`--real-price`、`--product-info` 等显式开关。

### 浏览器、并发和中断

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--browser chrome` | `chrome` | 当前只支持 Chrome。 |
| `--headless` / `--no-headless` | 有头 | 开启或关闭 Chrome headless。 |
| `--timeout SECONDS` | `300` | 浏览器启动超时秒数。 |
| `--workers N` | `min(厂商数, 5)` | Excel 厂商模式并发浏览器数，范围 `1..5`。 |
| `--startup-stagger SECONDS` | `0.5` | 多 worker 启动错峰秒数，`0` 表示关闭。 |
| `--stop-grace-seconds N` | `30` | 收到停止信号后等待部分结果导出的秒数。 |
| `--session-file FILE` | `~/Documents/trip_vendor_results/_state/session.json` | 指定 session JSON 文件；显式指定时不覆盖 `_state/session.json`。 |

### 内存保护

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--browser-memory-limit-mb N` | `8192` | 管理中的 Chrome RSS 超过 N MB 后重启。 |
| `--browser-restart-every N` | `20` | 每处理 N 个 URL 后重启 Chrome。 |
| `--disable-browser-memory-guard` | 关闭 | 禁用内存保护和自动重启。 |

### 延迟与风控

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--delay-profile fast|normal|safe|slow` | `normal` | 延迟预设档位。 |
| `--page-delay-min/max` | `-1` | 页面加载完成后、滚动前等待秒数；`-1` 使用档位默认。 |
| `--scroll-delay-min/max` | `-1` | 每次滚动之间等待秒数；`-1` 使用档位默认。 |
| `--sku-click-delay-min/max` | `-1` | SKU 点击成功后等待秒数；`-1` 使用档位默认。 |
| `--sku-price-delay-min/max` | `-1` | SKU 价格 bundle 读取前等待秒数；`-1` 使用档位默认。 |
| `--sku-between-delay-min/max` | `-1` | SKU 组合之间等待秒数；`-1` 使用档位默认。 |

### 日志

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--log-file FILE` | 空 | JSON event 日志文件；默认不写 `run.log` 或 `trip_cli_*.log`，只有显式传入时才写文件。 |
| `--log-level quiet|normal|verbose` | `normal` | 控制终端人类可读输出。 |
| `--json-lines` | 关闭 | 每行输出一个 JSON event，适合 Agent/脚本解析。 |
| `--no-color` | 关闭 | 禁用 ANSI 颜色。 |

## 8. 日志和退出码

`--json-lines` 输出每行一个事件。最后一行固定是 `run_done`，其中 `exit_code` 与进程退出码一致。

常见事件：

```json
{"event":"run_started","mode":"single","url_count":1,"workers":1,"delay_profile":"safe"}
{"event":"browser_prepare","stage":"driver_prepare","driver_reused":true,"driver_version":"148.0.7778.178"}
{"event":"browser_prepare","stage":"browser_start","profile_mode":"configured_profile","debugger_address":"127.0.0.1:53587"}
{"event":"url_started","url":"https://hk.trip.com/things-to-do/detail/92237157/","first_trip_navigation_url":"https://hk.trip.com/things-to-do/detail/92237157/?curr=HKD&locale=zh-hk","session_nav_skipped":true}
{"event":"log","stage":"risk","message":"risk scripts detected","risk_scripts_detected":[".../risk/ubtrms/...",".../jigsaw-captcha/..."]}
{"event":"delay_applied","stage":"page_open","label":"page","delay_ms":1112}
{"event":"url_done","rows":84,"final_url":"https://hk.trip.com/things-to-do/detail/92237157/?curr=HKD&locale=zh-hk","elapsed_ms":188517}
{"event":"run_done","success":true,"exit_code":0,"processed_count":84}
```

`risk_scripts_detected` 只表示页面加载了 Trip 风控相关脚本，不等于验证码。只有出现 `risk_challenge_detected` 时，Go CLI 才会停止当前 URL 的 SKU 点击并导出错误行。

退出码：

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功抓取并导出。 |
| `2` | 参数或输入错误。 |
| `4` | 浏览器启动、抓取或导出失败。 |
| `130` | 收到停止信号，任务中断。 |

中断行为：

- 第一次 `Ctrl+C` / `SIGINT` / `SIGTERM`：优雅停止，尽量导出已抓取结果。
- 第二次 `Ctrl+C` / `SIGINT` / `SIGTERM`：强制关闭剩余浏览器和 chromedriver。
- 超过 `--stop-grace-seconds` 后会自动升级为强制停止。

## 9. 故障排查

### SKU 容器不存在

日志：

```text
SKU container not found; product may have no SKU options
```

处理：

- 确认 URL 是 Trip.com 商品详情页。
- 用 `--no-headless` 观察页面是否真的有 SKU、是否出现登录弹窗或风控验证。
- 如果页面加载慢，使用 `--delay-profile safe` 或 `--delay-profile slow`。
- 如果 Python CLI 同 URL 也无 SKU，则通常是页面当前结构、库存或登录态问题，不一定是 Go 遍历逻辑问题。

### 实价或促销标签为空

处理：

- 先确认命令没有传 `--no-real-price`。
- 使用 `--delay-profile safe` 或增加 `--sku-price-delay-min/max`，给 footer 区异步刷新更多时间。
- 如果实价偶尔错位或掉到下一 SKU，优先使用 `safe/slow`，不要直接扩大选择器范围。

### 触发风控或结果明显偏少

处理：

- 优先使用可见浏览器：`--no-headless`。
- 批量 Excel 降低并发：先用 `--workers 1` 或 `--workers 2`，稳定后再尝试 `--workers 3`。
- 使用 `--delay-profile safe`；仍不稳定时改 `--delay-profile slow`。
- 保留默认 `--startup-stagger 0.5`，避免多个 worker 同时启动。
- 检查 JSON 日志里的 `first_trip_navigation_url`。HK URL 应该直接是 `hk.trip.com` 且带 `locale=zh-hk&curr=HKD`，不应先进入 `www.trip.com` 或 `sg.trip.com`。

### 浏览器启动失败

处理：

- 确认已安装 Google Chrome。
- 查看 JSON 日志中的 `chrome_detect_*`、`driver_prepare`、`browser_start_failed`。
- 首次运行需要下载 chromedriver；网络不可用时会失败。
- cache 目录不可写时，清理或修复 `~/Library/Application Support/undetected_chromedriver/` 权限。

### 残留进程

正常退出会关闭本次 Go 启动的 Chrome、chromedriver 和临时 profile。若外部中断或系统杀进程导致残留，可检查：

```bash
pgrep -fl 'trip-score-cdp|undetected_chromedriver|trip_cdp_chrome_profile'
```

只清理确认属于本次运行的进程，避免误杀日常 Chrome。

## 10. 开发验证

```bash
go test ./...
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
/tmp/trip-score-cdp --help
python3 -m py_compile main.py cli.py service.py models.py trip_cli_scraper.py session_store.py xlsx_export.py browser_startup.py
```

短单 URL 验证：

```bash
/tmp/trip-score-cdp \
  --run-mode mode2 \
  --delay-profile normal \
  --input "https://hk.trip.com/things-to-do/detail/92237157/" \
  --json-lines \
  --log-level verbose \
  --output /tmp/trip-score-cdp-smoke.xlsx
```

SKU/价格验证：

```bash
/tmp/trip-score-cdp \
  --run-mode mode1 \
  --input "https://hk.trip.com/things-to-do/detail/92237157/" \
  --json-lines \
  --log-level verbose \
  --output /tmp/trip-score-cdp-92237157.xlsx
```

短 smoke test 只证明基础启动、HK 首跳、session 和导出链路正常。SKU/价格验证应查看日志中的 `found N SKU layers`、`combination price`、`url_done.rows`，并与 Python CLI 同 URL 对比 SKU 组合数量、外显价、实价和促销标签。
