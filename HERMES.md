# Hermes Agent 项目入口

本文件是 Hermes Agent 在本项目中的唯一入口。执行任何抓取、导出、定时任务、诊断或结果交付前，必须先完整读取：

`hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md`

任务路由：

- 单个产品或数个指定产品的价格抓取（包括运营提示词 `【請抓取產品價格71827107】`）：使用 Skill 的 `Manual single or selected products price scrape`。成功时只发送 Skill 规定的原生 Excel `MEDIA:` 行，不先发送平文本结果。
- URL 列表评分与商品信息抓取：使用 `Ad-hoc rating-only score/sales scrape via --urls-file`。
- 多厂商、批量、趋势、比价、恢复或定时任务：使用 Skill 中对应的批量与 cron 章节。

项目内 Skill 是唯一权威来源。不得使用 `~/.hermes/skills/` 中的旧副本，也不得凭历史记忆跳过读取。所有相对路径都以项目根目录为基准。
