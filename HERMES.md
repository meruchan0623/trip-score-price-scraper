# Hermes Agent 项目入口

本文件是本项目 Hermes 运行规范的唯一项目入口。Hermes 不会仅因文件名为 `HERMES.md` 就自动加载它；主机必须按 README 将项目 Skill 配置到 `skills.external_dirs`。执行任何抓取、导出、定时任务、诊断或结果交付前，必须先完整读取：

`hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md`

任务路由：

- 单个产品或数个指定产品的价格抓取（包括运营提示词 `【請抓取產品價格71827107】`）：使用 Skill 的 `Manual single or selected products price scrape`。成功时只交付程序原生 Excel 附件，不发送平文本结果。
- URL 列表评分与商品信息抓取：使用 `Ad-hoc rating-only score/sales scrape via --urls-file`。
- 多厂商、批量、趋势、比价、恢复或定时任务：使用 Skill 中对应的批量与 cron 章节。

凡任务由对话窗口发起，任务完成后必须将本次生成的 Excel 文件作为附件发送到发起该任务的同一个对话窗口；不得只回复文件路径，也不得发送到其他对话、群组或频道。

指定产品抓取的用户可见回复由项目插件 `hermes-agent/plugins/trip-manual-delivery-guard/` 在 Hermes 发送前执行硬过滤。插件只允许原生附件，以及失败或影响完整性的单行警告；Agent 不得读取工作簿后生成价格、SKU、评分或产品摘要。

项目内 Skill 和插件是唯一权威来源。不得使用 `~/.hermes/skills/` 中的旧 Skill 副本，也不得在 `~/.hermes/plugins/` 维护插件代码副本；主机只建立指向项目插件目录的符号链接。所有相对路径都以项目根目录为基准。

## 市场与交付契约（与当前 guard/CLI 一致）

- 没有明确市场意图时，手工产品任务默认 `hk / zh-HK / HKD`。URL 中的 host 只提供商品路径，绝不据此推断市场；只有用户在 URL 外明确说出站点、地区或币种时才解析覆盖。币种到站点的 25 条映射是推荐值，不是硬绑定；显式 `site:locale:currency` 始终优先。
- 单市场继续使用 `--site`、`--locale`、`--currency`（或 HK 默认不传）；多市场使用按用户出现顺序重复的完整 `--market site:locale:currency`，市场 worker 默认 2、允许 1–3，并生成一个 XLSX，按市场顺序建立工作表。所有市场冲突、无效组合或不明确的“几个市场/美元区”等说法都必须 exit 2 或先请求澄清，不得猜测。
- 多市场结果必须覆盖全部请求市场；任一市场失败时保留原生工作簿并标记 partial（CLI exit 4），全部失败不写工作簿且仍 exit 4，不得把部分结果说成成功。价格质量字段按当前提交 exporter 合约输出。
- Hermes 交付前 guard 会以 ZIP/XML 原生结构校验工作簿、市场工作表顺序、host/locale/currency 和可信状态；手工成功只能发送原生 XLSX 附件（同一发起对话窗口），不发送路径、价格/SKU 摘要或重建表。ZIP/XML 校验失败只能返回允许的单行异常/警告及原生附件。
- 实现、自动化测试、公开页面 probe、部署/重启、登录后的业务验收是不同状态；本项目文档中的代码/测试/probe 证据不等同于部署或 authenticated business acceptance。
