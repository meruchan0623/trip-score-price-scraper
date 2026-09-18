# 指定 Trip 产品抓价：当前注意事项

**适用范围：** 用户要求抓一个或数个明确指定的 Trip.com Things-to-Do 产品价格时。本文件补充 `SKILL.md` 的手动抓取章节；根目录 `README.md` 是项目主使用文档。

## 固定执行契约

- 将任何输入的 Trip.com 链接仅作为产品 ID 来源；实际导航固定为 `https://hk.trip.com/things-to-do/detail/<id>/`，并固定 `locale=zh-HK`、`curr=HKD`。
- 必须使用项目根目录构建的 `./trip-score-price-scraper`，直接以重复 `-input` 传参运行，禁止用临时 shell/Python 包装脚本。
- 固定使用 `-run-mode mode1 -no-headless -site hk -locale zh-HK -currency HKD -delay-profile normal -product-info -no-quality-rerun -output-dir <manual-run-dir>`。
- 单一产品或少量指定产品顺序运行；输出目录为 `/Users/example/Documents/trip-sku-price-scrape/manual-<ids>`。异常重跑保留旧目录，创建时间戳后缀的 `manual-<ids>-rerun-YYMMDD-HHMM`。

## 页面与接口：不得把壳页当结果

直接打开 `/things-to-do/detail/<id>/` 可能先进入 `/xticket` 客户端渲染壳，再跳到同站 `/things-to-do/experiences/...` 列表页。列表页的“低至”价格绝不是目标商品 SKU 价格。

CLI 仅可在已经观察到该同站列表页时，从真实列表页正常回到原商品详情一次，并在提取前重新确认最终详情 URL 和产品 ID。不得伪造 Referer、重放/构造被风控接口请求，或把列表页/通用壳页导出为单 SKU 成功结果。

## SKU API 可信性门禁

- `21052/getProductInfo` 可能返回 HTTP 430 或 `whaleguard block`；这不自动表示价格日历无效。
- `14580/getProductPriceCalendar.data.packageInfos` 可在商品资讯接口缺失时作为 SKU 边界：`packageId`、`resourceId`、销售属性、原价、折后价和促销信息均以实际捕获响应为准。
- 批量 API 结果只有在 `tree_count == priced_count`、`missing_price_count == 0` 且出现 `sku_api_batch_used` 时才可直接信任。
- 覆盖率不足、资源 ID 不一致或接口不完整时，必须进入浏览器逐 SKU 回退与价格质量门禁；不得交付部分 API 行、页面级最低价或对属性做笛卡尔积生成的 SKU。
- 字段映射：`priceInfo.originalPrice` → 外显价格；`priceInfo.price` → 实价；`preferentialPriceDetails` → SKU 促销标签。没有促销时，外显价=实价、促销标签为空、`trusted_base` 是正常可信结果。

## 完成与交付门禁

每个请求产品都必须同时满足：

1. `url_done`、`page_valid=true`、`trusted_rows > 0`，且没有价格质量失败；批量 API 路径另须满足完整覆盖门禁。
2. 原生 XLSX 只有 `Trip商品数据` 工作表；每个数据行都保留目标商品 ID、SKU 层级、HKD、外显价格、实价和 `trusted_promo` 或 `trusted_base`。
3. 任何 `risk_blocked`、Cloudflare/Whaleguard、目标 ID 缺失、通用页标题、无 SKU、非 HKD、未知/不可信价格状态或缺少必填字段都使该次运行失败，不能交付。
4. 通过 `trip_deliver.py` 投递未二次加工的 Go CLI 原生 XLSX；`.delivered.json` 是送达证据。

成功时只投递原生 Excel 附件，不发送确认、进度、产品信息、价格、SKU 数量、对比、总结、路径或二次整理表。只有失败或影响完整性的异常，才按 `SKILL.md` 的单行前缀报告。

## Session 说明

`session cookies unavailable` 不是登录恢复成功的证据。公开商品可作为匿名可用性探测继续运行；任何确实依赖登录态的路径都必须单独刷新并验证显式 session，且不得输出 Cookie、令牌或其他认证值。
