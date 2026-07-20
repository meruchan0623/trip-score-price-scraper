# Hermes Agent 项目入口

本文件是本项目 Hermes 运行规范的唯一项目入口。Hermes 不会仅因文件名为 `HERMES.md` 就自动加载它；主机必须按 README 将项目 Skill 配置到 `skills.external_dirs`。执行任何抓取、导出、定时任务、诊断或结果交付前，必须先完整读取：

`hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md`

任务路由：

- 单个产品或数个指定产品的价格抓取（包括运营提示词 `【請抓取產品價格71827107】`）：使用 Skill 的 `Manual single or selected products price scrape`。成功时只交付程序原生 Excel 附件，不发送平文本结果。
- URL 列表评分与商品信息抓取：使用 `Ad-hoc rating-only score/sales scrape via --urls-file`。
- 多厂商、批量、趋势、比价、恢复或定时任务：使用 Skill 中对应的批量与 cron 章节。

指定产品抓取的用户可见回复由项目插件 `hermes-agent/plugins/trip-manual-delivery-guard/` 在 Hermes 发送前执行硬过滤。插件只允许原生附件，以及失败或影响完整性的单行警告；Agent 不得读取工作簿后生成价格、SKU、评分或产品摘要。

项目内 Skill 和插件是唯一权威来源。不得使用 `~/.hermes/skills/` 中的旧 Skill 副本，也不得在 `~/.hermes/plugins/` 维护插件代码副本；主机只建立指向项目插件目录的符号链接。所有相对路径都以项目根目录为基准。
