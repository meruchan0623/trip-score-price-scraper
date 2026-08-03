# Hermes 手动产品价格抓取稳定性与输出边界 Spec

**状态：提案，未实施**
**日期：2026-08-03**
**范围：** Hermes Agent 的「指定 Trip 产品价格抓取」；不改变每日厂商趋势任务、横向比价任务及其已声明的交付物。

## 1. 背景与证据

2026-08-03 已通过 SSH 对 Hermes 主机进行只读核查。以下是本 Spec 的事实基础；产品 ID、URL 和会话内容均已脱敏。

| 观察 | 证据 | 影响 |
| --- | --- | --- |
| 旧会话采用 `curl` 下载网页、临时 Python + `openpyxl` 重新编表的路径。 | 当前会话请求转储中出现旧的页面缓存、`openpyxl`、`價格清單` 与长 SKU 文本表输出。 | Agent 可绕过 Go CLI 原生导出，生成字段、币种和命名都不受控的二次 Excel。 |
| 当前仓库在 `Harden Hermes manual scrape delivery` 版本，插件符号链接指向项目代码。 | 主机 HEAD 为 `7b0b1ca`；`~/.hermes/plugins/trip-manual-delivery-guard` 指向项目插件，两个插件文件哈希一致。 | 基础治理已部署，但不能证明所有自然语言变体都会经过守卫。 |
| 近 7 天新增的 7 个新式手动目录均只有一个 `trip_scoreandprice_results_*.xlsx`。 | `manual-<id>` / `manual-<ids>` 目录清单。 | 新式原生路径已经能够避免二次工作簿。 |
| 历史手动目录仍混有输入表、重复 Excel、质量重跑文件及大量 Chrome profile 文件。 | `manual-3products` 等旧目录包含输入 XLSX、重复输出、`stdout.log`、`quality_rerun_summary.jsonl` 与浏览器状态文件。 | 运行时文件和可交付文件同目录，造成目录污染、误附件风险和难以审计。 |
| 同一产品在当日出现 `rerun`、`normal`、`slow` 等多个手工目录。 | 2026-08-03 的手动 run 目录命名与时间顺序。 | 失败处理缺少幂等键、单一重试策略和“停止而非继续试错”的边界。 |
| 插件的手动请求正则只匹配「抓取…价格」；历史对话大量使用「抓价格」「抓產品價格」「ID: …」等变体。 | 插件 `_MANUAL_INTENT_RE` 与会话请求转储比对。 | 部分真实请求不会被标记为 manual turn，因而可能不注入约束、也不经过最终交付过滤。 |
| 插件向模型注入 `fast` 延迟提示，项目 Skill 的手动固定契约则为 `normal`。 | `trip-manual-delivery-guard/__init__.py` 与项目 Skill 的手动章节。 | 同一任务存在相互矛盾的运行指令，可能导致不必要的风控、失败与重复执行。 |

## 2. 目标与非目标

### 2.1 目标

1. 任意「指定产品价格」自然语言请求都只能走 Go CLI 原生抓取路径。
2. 对一次成功的手动请求，用户只能收到一个原生 `trip_scoreandprice_results_*.xlsx` 附件；不得收到 Agent 生成的价格清单、清洗表、输入表、重跑表或 Markdown SKU 表。
3. 一个手动运行目录只保留经允许的交付物和最少的 run manifest；浏览器 profile、临时输入、日志、诊断和重跑状态全部写入隔离的运行时目录。
4. 对风险拦截、超时或质量不合格，系统提供稳定、可审计的失败结果；不得自行切换 `fast/normal/slow` 连续试跑。
5. 每次执行均可从 manifest 判断：请求、路由、命令版本、结果、交付和失败原因，而无需解析模型自然语言回复。

### 2.2 非目标

- 不删除历史目录或历史会话记录；清理由运营人员另行确认后执行。
- 不改变每日厂商批量任务的原始表、趋势表、横向比价表及 ZIP 交付契约。
- 不改变 Go CDP 的 SKU 解析规则、价格字段或浏览器反爬策略本身。
- 不要求 Agent 在成功时解释产品、SKU、价格、最低价或运行过程。

## 3. 目标行为与输出契约

### 3.1 手动请求路由

下列请求等价，必须被识别为 manual scrape：

- `请抓取产品价格 <ID>` / `請抓取產品價格 <ID>`
- `抓价格 <ID>` / `抓價格 <ID>` / `抓產品價格 ID:<ID>`
- `请抓这个链接的价格 <Trip detail URL>`
- 多个 ID 或多个 Trip detail URL，保持用户给出的顺序。

触发后，路由器应从 URL 或 ID 生成唯一的有序产品列表，并构造 deterministic run key：

```text
manual:<locale>:<currency>:<ordered-product-ids>:<request-hash>
```

默认 locale/currency 为 `zh-HK/HKD`。同一 session 内、同一 run key 尚未结束时，第二次请求只能返回该 run 的状态；不得启动并发重复浏览器。

所有 `trip.com` 及其子域 URL（包括 `www`、`sg`、`us`、`hk` 及含 `curr=CNY` 的链接）都必须先重写为 `https://hk.trip.com`，并覆盖查询参数为 `locale=zh-HK`、`curr=HKD`；保留与价格无关的查询参数。最终原生工作簿的 `URL` 列必须写入该规范后的香港站 URL。任何 CNY/RMB 页面、价格、币种或二次换算均不得进入手动交付工作簿。

### 3.2 运行命令

手动模式的唯一执行器是项目根目录构建的 Go CLI。默认参数固定为：

```text
-no-headless
-delay-profile normal
-product-info
-no-quality-rerun
```

只有用户明确要求重试时才能创建新 attempt；重试仍使用 `normal`。风险拦截后的处理是结束本次 attempt 并报告失败，不允许 Agent 自主依次尝试 `fast`、`slow` 或改用 `curl` / `openpyxl`。

### 3.3 文件系统边界

```text
trip-sku-price-scrape/
  manual-<ordered-ids>/
    trip_scoreandprice_results_<timestamp>.xlsx  # 唯一用户可交付工作簿
    run-manifest.json                            # 可审计元数据，不作为聊天附件
  _runtime/manual/<run-id>/
    profile/
    session.json
    terminal_output.txt
    run.jsonl
    diagnostics/
```

要求：

- `manual-<ordered-ids>/` 不得包含输入 XLSX、`_quality_rerun/`、Chrome profile、`stdout.log`、截图、缓存或多个原生结果文件。
- CLI 写入先进入 `_runtime/manual/<run-id>/staging/`；只有所有结构校验通过后，才原子移动唯一原生 XLSX 到 final 目录。
- 质量诊断保留 JSON/JSONL，不生成诊断 XLSX。
- `run-manifest.json` 至少包含 `run_id`、产品 ID 列表、开始/结束时间、CLI Git SHA、参数、退出码、原生工作簿 SHA-256、质量状态、风险状态和 delivery status。它不得包含 Cookie、token、完整用户消息或浏览器 profile 内容。

### 3.4 对话与交付边界

成功时，网关只发送原生 XLSX 附件；用户可见文本为空。

失败或影响完整性时，只允许以下固定文本之一：

```text
抓取失败：未生成原生Excel（<run-dir>）
抓取结果异常：原生Excel校验未通过。
抓取重要警告：<预定义且不含价格的原因>。
```

以下均被禁止：产品名、SKU 数、最低价、价格表、币种比较、运行命令、临时路径、Agent 自建 Excel、`MEDIA:` 指向非原生文件。

最终交付检查必须独立于 LLM 输出执行，检查附件真实路径是否同时满足：

1. 位于 final manual 目录；
2. 文件名严格匹配 `trip_scoreandprice_results_YYMMDD-HHMM.xlsx`；
3. 与当前 run manifest 的 `workbook_sha256` 一致；
4. 工作簿仅有 `Trip商品数据` sheet，且满足原生字段校验；
5. 当前 run 状态为 `success` 或允许交付的 `warning`。

任一项不满足时，网关拒绝附件，不以模型文本作为替代。

## 4. 实施要求

### P0：确定性拦截与输出白名单

1. 扩展 manual request matcher，覆盖「抓」「抓取」、繁简体、`产品/產品`、`价格/價格`、`ID:` 与 Trip detail URL；不因品牌/目的地附注漏匹配。
2. 将产品 ID 解析、运行目录、固定 CLI 参数和附件候选计算提取到无 LLM 依赖的路由组件。
3. 让发送前插件只使用路由组件产生的 run manifest；不要从模型回复中猜测结果或附件。
4. 修正插件上下文中的 `fast` 为 `normal`，并消除与 Skill 的重复、冲突参数说明。
5. 在 Agent 工具政策和项目 Skill 中明确禁止手动模式使用 `curl` 下载产品页、`openpyxl` 重建价格表、`--output` 自定义工作簿名、`--urls-file` 和 `--excel-input`。

### P0：输出与运行时隔离

1. 在手动工作流显式传入隔离的 `--session-file` / managed profile 路径，禁止浏览器状态落在 final output 目录。
2. 引入 staging → validate → promote 流程，清晰区分可交付与运行时文件。
3. 对现有 final manual 目录执行输出 allowlist 校验；发现额外文件时将 run 标记为 `artifact_boundary_violation`，不可自动交付。
4. 为每次 run 写 JSON manifest；异常时也必须落盘，避免只能通过聊天记录还原行为。

### P1：幂等执行与失败策略

1. 以 run key 维护 `queued/running/success/warning/failed` 状态，运行中重复请求不新建浏览器和目录。
2. 默认最大 attempt 数为 1；只有用户明确的“重试”才创建第 2 次 attempt，并将前一次原因写入 manifest。
3. 遇到 Cloudflare/Whaleguard、超时、缺字段或风险价格状态时，停止并以固定警告/失败交付；不自动替换 delay profile 或改走备用抓取器。
4. 手动任务使用独立短会话或在任务完成后清除该 turn 的任务上下文，避免历史 `curl/openpyxl` 指令在后续同类请求中继续影响工具选择。

### P1：可观测性与保留策略

1. `run.jsonl` 使用结构化事件：`manual_route_detected`、`run_started`、`validation_failed`、`artifact_promoted`、`delivery_allowed`、`delivery_blocked`、`run_finished`。
2. 每个事件包含 `run_id`、attempt、session hash、产品 ID 列表 hash、错误分类和时间，不保存对话全文或机密会话数据。
3. 增加每日只读审计：列出前 24 小时 manual 目录中的非 allowlist 文件、重复 run key、失败原因分布和未交付成功 run。
4. 审计结果写 JSON，运营摘要仅报告计数与 run ID，不附带原始对话或价格数据。

## 5. 测试与验收标准

### 单元测试

- 所有历史请求写法都识别为 manual scrape，包括 `抓价格 ID:<ID>`、`抓產品價格 <ID>`、带品牌文字的 ID、多产品和 URL。
- 非价格场景中出现数字或 Trip URL 不触发 manual scrape。
- `normal` 是路由、插件注入和实际 CLI 命令的唯一默认 delay profile。
- 模型输出无论含有 Markdown 表、`MEDIA:`、旧价格清单路径或多个 XLSX，交付层都只允许 manifest 绑定的原生工作簿。
- 质量失败不会产生 XLSX 诊断文件。
- 运行中相同 run key 的并发请求只得到同一个 run id，不产生第二个 Chrome 进程或 final 目录。

### 集成测试

1. 以临时 output root 执行单产品与多产品模拟 run。
2. 检查 final 目录只有原生工作簿和 manifest，运行时目录包含日志/profile，二者无交叉。
3. 注入 Cloudflare、超时、缺少货币、无 SKU、错误 sheet、多个候选 XLSX 等情形；验证不得交付不合格文件。
4. 重放包含旧 `curl/openpyxl` 历史的会话上下文；验证新的请求仍走确定性 Go CLI 路由。
5. 在 Hermes Gateway 新会话中完成一次真实 smoke run，确认用户界面只看到一个原生附件。

### 上线验收

连续 7 天满足：

- 手动成功 run 的 extra-artifact 数量为 0；
- 手动任务二次 Agent 生成 XLSX 数量为 0；
- 无用户明确重试时，同一 run key 的 attempt 数量为 1；
- `delivery_blocked` 均有对应 manifest 和原因；
- 运营可从审计 JSON 找到每个手动附件的来源 run，而无需读取聊天记录。

## 6. 迁移与风险控制

1. 首先以 feature flag 在一个测试 Telegram 会话启用确定性手动路由，不修改每日 cron。
2. 对现有历史 `manual-*` 目录只生成审计报告，不移动、不删除、不重命名。
3. 与当前生产插件并行验证附件白名单；确认后再关闭旧的“由 LLM 选择附件”分支。
4. 每一步保留回滚开关：关闭新路由可回到既有 Go CLI Skill，但不得恢复 `curl/openpyxl` 旧路径。

## 7. 实施顺序

1. P0 matcher、参数冲突修正与纯路由测试。
2. P0 runtime/final 目录隔离、manifest 与附件白名单。
3. P1 幂等状态机、显式重试与结构化事件。
4. P1 审计任务与 7 天上线观测。

在 P0 完成前，不应继续向旧会话追加“抓价格”类任务；应新建会话，以免历史 `curl/openpyxl` 工具链继续参与决策。
