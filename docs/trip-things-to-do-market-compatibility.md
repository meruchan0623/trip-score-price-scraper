# Trip Things-to-Do 市场兼容性记录

观测日期：2026-08-06（Asia/Shanghai）
基准提交：`a3b40aaed2855aaea35d982ac0549e247213e59a`
商品：`45892282`；输入 URL 固定为 `https://hk.trip.com/things-to-do/detail/45892282/`

## 边界与判定

这是 Go CDP 实现、自动化测试和公开页面探针的证据记录，不是已登录的业务验收。没有执行登录、凭据/Token 读取、原始 Cookie 导出、写入、部署、cron 或远程操作；returned-context gate 仅在页面内读取 `ibusite`、`ibulocale`、`cookiePricesDisplayed` 三个公开市场偏好值并返回规范化后的 site/locale/currency。authenticated business acceptance remains open。`--fresh-profile` 每次使用临时空 profile，不能据此推断登录态业务流程。

市场解析顺序是 `--site` 选择 host 和默认值，`--locale` 覆盖展示语言，`--currency` 覆盖价格币种。最终导航显式设置两项 query；returned-context gate 必须确认最终 host、site ID、locale 和 ISO currency，非默认组合才可信任价格。SA 的默认 locale 是 `ar-SA`，但它不是 60 个 Things-to-Do advertised locale 列表中的独立新增值；它是站点默认值与广告能力清单的边界案例。

广告 locale 是能力 allow-list，并不保证每个 locale/host/product 都有库存。通过上下文门禁但商品不存在时，结果分类为 `unavailable`；这仍是上下文证据，不是可售价格验收。

## 33 个已登记公开 host（观测日 2026-08-06）

| code | site ID | host | 默认 locale | 默认 currency |
|---|---|---|---|---|
| global | EN | www.trip.com | en-XX | USD |
| hk | HK | hk.trip.com | zh-HK | HKD |
| sg | SG | sg.trip.com | en-SG | SGD |
| us | US | us.trip.com | en-US | USD |
| tw | TW | tw.trip.com | zh-TW | TWD |
| jp | JP | jp.trip.com | ja-JP | JPY |
| kr | KR | kr.trip.com | ko-KR | KRW |
| th | TH | th.trip.com | th-TH | THB |
| vn | VN | vn.trip.com | vi-VN | VND |
| id | ID | id.trip.com | id-ID | IDR |
| my | MY | my.trip.com | ms-MY | MYR |
| au | AU | au.trip.com | en-AU | AUD |
| uk | GB | uk.trip.com | en-GB | GBP |
| de | DE | de.trip.com | de-DE | EUR |
| fr | FR | fr.trip.com | fr-FR | EUR |
| es | ES | es.trip.com | es-ES | EUR |
| it | IT | it.trip.com | it-IT | EUR |
| nl | NL | nl.trip.com | nl-NL | EUR |
| pt | PT | pt.trip.com | pt-PT | EUR |
| br | BR | br.trip.com | pt-BR | BRL |
| ca | CA | ca.trip.com | en-CA | CAD |
| ae | AE | ae.trip.com | en-AE | AED |
| sa | SA | sa.trip.com | ar-SA | SAR |
| in | IN | in.trip.com | en-IN | INR |
| ph | PH | ph.trip.com | en-PH | PHP |
| ua | UA | ua.trip.com | uk-UA | UAH |
| dk | DK | dk.trip.com | da-DK | DKK |
| pl | PL | pl.trip.com | pl-PL | PLN |
| fi | FI | fi.trip.com | fi-FI | EUR |
| se | SE | se.trip.com | sv-SE | SEK |
| tr | TR | tr.trip.com | tr-TR | TRY |
| gr | GR | gr.trip.com | el-GR | EUR |
| ru | RU | ru.trip.com | ru-RU | RUB |

## 60 个 advertised Things-to-Do locale

```text
da-DK de-AT de-CH de-DE el-GR
en-AE en-AU en-BE en-BH en-CA en-CH en-GB en-HK en-ID
en-IE en-IL en-IN en-KH en-KW en-MY en-NZ en-OM en-PH
en-PK en-QA en-SA en-SG en-TH en-US en-XX
es-ES es-MX es-US fi-FI
fr-BE fr-CH fr-FR id-ID it-IT ja-JP ko-KR ms-MY
nl-BE nl-NL pl-PL pt-BR pt-PT
ru-AZ ru-BY ru-KZ ru-RU sv-SE
th-TH tr-TR uk-UA vi-VN zh-HK zh-MY zh-SG zh-TW
```

## 代表性公开探针

九次运行均为 visible Chrome（`--no-headless`）、`--fresh-profile`、`--workers 1`、`--delay-profile normal`、`--json-lines`，串行执行。每行的 `final_url`、`url_done` 和 `run_done` 均从对应 `/tmp/*.log` 读取；price statuses 为 `trusted_base`/`trusted_promo`（197 行全部 trusted，未见 untrusted），没有 `risk_blocked`、context mismatch、currency mismatch 或质量失败。

| site | requested locale / currency | final host | observed locale / currency | product / rows | price statuses | run_done.success | evidence |
|---|---|---|---|---:|---|---|---|
| hk | zh-HK / HKD | hk.trip.com | zh-HK / HKD | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-hk-45892282.log` |
| sg | en-SG / SGD | sg.trip.com | en-SG / SGD | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-sg-45892282.log` |
| tw | zh-TW / TWD | tw.trip.com | zh-TW / TWD | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-tw-45892282.log` |
| jp | ja-JP / JPY | jp.trip.com | ja-JP / JPY | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-jp-45892282.log` |
| kr | ko-KR / KRW | kr.trip.com | ko-KR / KRW | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-kr-45892282.log` |
| th | th-TH / THB | th.trip.com | th-TH / THB | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-th-45892282.log` |
| us | en-US / USD | us.trip.com | en-US / USD | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-us-45892282.log` |
| de | de-DE / EUR | de.trip.com | de-DE / EUR | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-de-45892282.log` |
| sa | ar-SA / SAR | sa.trip.com | ar-SA / SAR | available / 197 | trusted (197) | true | `/tmp/trip-market-acceptance-sa-45892282.log` |

Workbook outputs are under `/tmp/trip-market-acceptance-*-45892282.xlsx`; an XLSX alone is not acceptance evidence. The remaining 24 registry entries have registry/unit-test coverage only, not a public probe in this record.

## Failure classes and safe boundaries

- `market_context_mismatch`: returned host/site/locale/currency disagrees with the request; no trusted price rows.
- `market_context_unverified`: one or more returned context fields is missing; no trusted price rows.
- `currency_mismatch`: API/DOM currency conflicts with expected ISO currency; price is rejected.
- `risk_blocked`: bot/Cloudflare/Whaleguard or bounded risk retry stopped extraction; never compatibility acceptance.
- `unavailable`: context is verified but the product is absent/unavailable on that market; acceptable context evidence only.
- `trusted_base` / `trusted_promo`: accepted price statuses after ISO/currency quality checks.
- captcha, MFA, user takeover, or task-space ownership loss: stop immediately and hand back to the user; no bypass or unbounded retry.

Only cookie names are documented for operator troubleshooting: `ibusite`, `ibugroup`, `ibulanguage`, `ibulocale`, `cookiePricesDisplayed`, `ibu_country`. Values, headers, storage, session files, credentials, tokens, and user identifiers are intentionally excluded.

## Verification performed

At the prerequisite SHA: Hermes guard `python3 -m unittest tests/test_trip_manual_delivery_guard.py` passed (15 tests); protected-file `git diff --exit-code` passed; `go build -o /tmp/trip-score-cdp-all-sites ./cmd/trip-score-cdp` passed. The nine probes above each exited 0 with `run_done.success=true`. Before the documentation commit, the primary agent independently reran `go test ./... -count=1`, `go vet ./...`, the final build, the Hermes guard, protected-file diff, stale-assumption scans, and workbook/log inspection; all completed successfully.
