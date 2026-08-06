# Trip.com All Current Sites Compatibility Implementation Plan

> **For agentic workers:** REQUIRED EXECUTION MODE: the primary Codex agent must use Codex-native subagents task-by-task, review every returned diff, and run the stated verification before accepting a task. Repository instructions prohibit `superpowers:subagent-driven-development` and `superpowers:executing-plans`; do not invoke either skill.

**Goal:** Extend the Go CDP Trip.com Things-to-Do scraper from the HK/SG special case to a verified, data-driven market model covering the currently discovered regional hosts and advertised locale contexts without weakening price, anti-bot, session-isolation, or Hermes HK-only delivery gates.

**Architecture:** Model Trip market identity as three explicit axes: regional site/host, locale, and currency. Resolve them once during CLI parsing into an immutable `MarketContext`, propagate it through navigation and extraction, and reject prices when the final host or returned bootstrap context disagrees. Prefer API ISO currency and numeric amounts; use expected-currency-aware DOM parsing only as fallback and quality evidence.

**Tech Stack:** Go 1.24, standard `flag`/`net/url`/`regexp`/`encoding/json`, Rod/CDP, existing Go tests, Python `unittest` only for the unchanged Hermes delivery guard.

## Global Constraints

- Default remains `hk.trip.com` + `zh-HK` + `HKD`.
- Preserve the intentional HK/HKD-only Hermes manual contract; do not modify `hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md`, `hermes-agent/plugins/trip-manual-delivery-guard/__init__.py`, or `docs/2026-08-03-hermes-manual-scrape-stability-spec.md`.
- Preserve `--fresh-profile`: no saved Chrome profile or `session.json` may be copied into cross-market acceptance runs.
- Preserve headed/normal operation and existing Whaleguard/Cloudflare classification. Add no bypasses, credential handling, cookie export, storage injection, or unbounded retries.
- Every Trip navigation explicitly sets `locale` and `curr`. Host/site, locale, and currency remain independent.
- Unknown `--site`, unadvertised `--locale`, and unsupported ISO `--currency` fail during argument parsing with exit code 2; never fall back silently to HK.
- Final-host disagreement is `market_context_mismatch`. Missing returned locale/currency evidence is `market_context_unverified`. Neither may produce trusted price rows.
- Preserve raw price text, numeric amount, and ISO currency independently. Interpret ambiguous symbols (`$`, `¥`, `kr`) only against the expected currency.
- Keep the workbook schema unchanged. Expose market context through canonical URL, currency column, and structured events.
- Product absence in a verified market is `unavailable`, not a parser failure. `risk_blocked` never counts as compatibility acceptance.
- Registry additions require review, tests, an observation date, and a bounded public probe. Do not auto-enable discoveries.
- No deployment, cron changes, remote writes, or authenticated business-acceptance claim is authorized.

## Verified Discovery Baseline

The initial registry contains 33 official public hosts observed on 2026-08-06:

| Code | Site ID | Host | Default locale | Currency |
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

The advertised Things-to-Do locale allow-list contains exactly these 60 canonical values:

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

This is a locale capability allow-list, not proof that every locale works on every host. A non-default pair must pass the returned-context gate before any price is trusted.

## File Structure

- Create `internal/tripcdp/market_context.go` and `market_context_test.go` for registries, normalization, resolution, and observation validation.
- Create `internal/tripcdp/price_text.go` and `price_text_test.go` for expected-currency DOM parsing.
- Modify `types.go` and `cli.go` for `TaskRequest.Market` and `--site/--locale/--currency`.
- Modify `navigation.go` and `runner.go` for immutable context propagation.
- Modify `browser.go` and `api_sku_prices.go` for returned-context gating and multi-currency extraction.
- Modify focused tests plus `README.md`.
- Create `docs/trip-things-to-do-market-compatibility.md` for dated evidence.

## Codex-Native Execution Protocol

- Execute Tasks 1-6 serially because they share Go package state; start Task 7 only after Task 6 is accepted.
- Only the primary Codex agent spawns workers, at depth 1. Use a fresh `luna_worker` for Tasks 1-4 and 7; use `terra_worker` for Tasks 5-6.
- Each packet repeats objective, deliverable, allowed files, prerequisite SHA, success criteria, verification, forbidden actions, and return format.
- Each worker returns runtime metadata if exposed, changed files, RED/GREEN command output and exit codes, risks, and proposed commit message.
- Results are evidence, not acceptance. The primary inspects the full diff, reruns verification, checks scope, then commits.
- Record `model_route_metadata_unavailable` when runtime metadata is not exposed; self-report is not proof.
- Interrupt any still-running worker after integration.

---

### Task 1: Market Registry

**Files:**
- Create: `internal/tripcdp/market_context.go`
- Create: `internal/tripcdp/market_context_test.go`

**Interfaces:**
- Produces:

```go
type MarketContext struct {
    Code, SiteID, Host, Locale, Currency, AcceptLanguage string
    PricePrefixes []string
}
func lookupSite(code string) (MarketContext, bool)
func advertisedTTDLocale(locale string) bool
func supportedCurrency(currency string) bool
func resolveMarketContext(site, locale, currency string) (MarketContext, error)
func supportedSiteCodes() []string
```

- Site aliases are only `www` and `en` → `global`.
- Locale normalization changes `_` to `-`, lowercases language, uppercases two-letter region, and preserves `XX`.
- Resolution precedence is site defaults, locale override, then uppercase currency override.
- Currency prefixes include all default currencies; ambiguous prefixes remain associated with the expected currency:

```go
var currencyPricePrefixes = map[string][]string{
    "AED":{"AED"}, "AUD":{"AU$","A$","AUD"}, "BRL":{"R$","BRL"},
    "CAD":{"CA$","C$","CAD"}, "DKK":{"DKK","kr"}, "EUR":{"€","EUR"},
    "GBP":{"£","GBP"}, "HKD":{"HK$","HKD"}, "IDR":{"Rp","IDR"},
    "INR":{"₹","INR"}, "JPY":{"¥","￥","JPY"}, "KRW":{"₩","KRW"},
    "MYR":{"RM","MYR"}, "PHP":{"₱","PHP"}, "PLN":{"zł","PLN"},
    "RUB":{"₽","RUB"}, "SAR":{"SAR","ر.س"}, "SEK":{"SEK","kr"},
    "SGD":{"S$","SGD"}, "THB":{"฿","THB"}, "TRY":{"₺","TRY"},
    "TWD":{"NT$","TWD"}, "UAH":{"₴","UAH"}, "USD":{"US$","$","USD"},
    "VND":{"₫","VND"},
}
```

- [ ] **Step 1: Write failing registry tests**

Create a table with all 33 rows from the baseline and compare `SiteID`, `Host`, `Locale`, `Currency`, non-empty `AcceptLanguage`, and non-empty `PricePrefixes`:

```go
func TestSiteRegistryMatchesVerifiedDefaults(t *testing.T) {
    if got := len(supportedSiteCodes()); got != 33 {
        t.Fatalf("site count = %d, want 33", got)
    }
    checks := []struct{ code, site, host, locale, currency string }{
        {"global","EN","www.trip.com","en-XX","USD"},
        {"hk","HK","hk.trip.com","zh-HK","HKD"},
        {"sg","SG","sg.trip.com","en-SG","SGD"},
        {"jp","JP","jp.trip.com","ja-JP","JPY"},
        {"tw","TW","tw.trip.com","zh-TW","TWD"},
        {"de","DE","de.trip.com","de-DE","EUR"},
        {"sa","SA","sa.trip.com","ar-SA","SAR"},
        {"ru","RU","ru.trip.com","ru-RU","RUB"},
    }
    for _, c := range checks {
        got, ok := lookupSite(c.code)
        if !ok || got.SiteID != c.site || got.Host != c.host || got.Locale != c.locale || got.Currency != c.currency || got.AcceptLanguage == "" || len(got.PricePrefixes) == 0 {
            t.Errorf("%s = %+v, found=%v", c.code, got, ok)
        }
    }
}
```

The production table and test fixture must enumerate all 33 literal rows; the compact snippet above shows assertion shape, not permission to omit the other 25.

- [ ] **Step 2: Write locale and override tests**

```go
func TestAdvertisedTTDLocalesAndOverrides(t *testing.T) {
    if len(advertisedTTDLocales) != 60 { t.Fatalf("locale count = %d", len(advertisedTTDLocales)) }
    for _, v := range []string{"zh-HK","en-SG","fr-CH","ru-KZ","en-SA"} {
        if !advertisedTTDLocale(v) { t.Errorf("missing %s", v) }
    }
    if advertisedTTDLocale("ar-SA") { t.Error("ar-SA is a site default, not one of the 60 advertised locales") }
    sa,err := resolveMarketContext("sa","","")
    if err != nil || sa.Locale != "ar-SA" || sa.Currency != "SAR" { t.Fatalf("SA default: %+v %v",sa,err) }
    got, err := resolveMarketContext("sg", "zh_sg", "myr")
    if err != nil { t.Fatal(err) }
    if got.Host != "sg.trip.com" || got.Locale != "zh-SG" || got.Currency != "MYR" { t.Fatalf("%+v", got) }
    for _, bad := range [][3]string{{"xx","",""},{"hk","ja-US","HKD"},{"hk","zh-HK","ZZZ"}} {
        if _, err := resolveMarketContext(bad[0],bad[1],bad[2]); err == nil { t.Errorf("accepted %v", bad) }
    }
}
```

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/tripcdp -run 'TestSiteRegistry|TestAdvertisedTTDLocales' -count=1`
Expected: compile failure because registry interfaces do not exist.

- [ ] **Step 4: Implement minimal registry**

Use literal maps/slices. `AcceptLanguage` is `<locale>,<language>;q=0.9,en;q=0.8` except HK retains the existing exact HK header. Return copied prefix slices.

- [ ] **Step 5: Verify GREEN**

```bash
gofmt -w internal/tripcdp/market_context.go internal/tripcdp/market_context_test.go
go test ./internal/tripcdp -run 'TestSiteRegistry|TestAdvertisedTTDLocales' -count=1
go test ./internal/tripcdp -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/market_context.go internal/tripcdp/market_context_test.go
git commit -m "feat: add Trip market context registry"
```

---

### Task 2: CLI Market Resolution

**Files:**
- Modify: `internal/tripcdp/types.go:57-79`
- Modify: `internal/tripcdp/cli.go:59-185,261-291`
- Modify: `internal/tripcdp/cli_test.go:155-183`

**Interfaces:**
- Consumes `resolveMarketContext`.
- Produces `TaskRequest.Market MarketContext` and flags `--site`, `--locale`, `--currency`.

- [ ] **Step 1: Write failing default/override tests**

```go
func TestParseMarketDefaultsAndOverrides(t *testing.T) {
    r,_,_,_,_,err := parseArgs([]string{"--input","https://trip.com/detail/1"}, &bytes.Buffer{})
    if err != nil { t.Fatal(err) }
    if r.Market.Code!="hk" || r.Market.Locale!="zh-HK" || r.Market.Currency!="HKD" { t.Fatalf("%+v",r.Market) }

    r,_,_,_,_,err = parseArgs([]string{"--site","sg","--locale","zh_sg","--currency","myr","--input","https://trip.com/detail/1"}, &bytes.Buffer{})
    if err != nil { t.Fatal(err) }
    if r.Market.Host!="sg.trip.com" || r.Market.Locale!="zh-SG" || r.Market.Currency!="MYR" { t.Fatalf("%+v",r.Market) }
}
```

- [ ] **Step 2: Write all-site/rejection tests**

```go
func TestParseAllSitesAndRejectsBadMarket(t *testing.T) {
    for _, site := range supportedSiteCodes() {
        r,_,_,_,_,err := parseArgs([]string{"--site",site,"--input","https://trip.com/detail/1"}, &bytes.Buffer{})
        if err != nil || r.Market.Code != site { t.Errorf("%s: %+v %v",site,r.Market,err) }
    }
    cases := [][]string{
        {"--site","xx","--input","https://trip.com/detail/1"},
        {"--locale","ja-US","--input","https://trip.com/detail/1"},
        {"--currency","ZZZ","--input","https://trip.com/detail/1"},
    }
    for _, argv := range cases {
        if _,_,_,_,_,err := parseArgs(argv,&bytes.Buffer{}); err == nil { t.Errorf("accepted %v",argv) }
    }
}
```

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/tripcdp -run 'TestParseMarket|TestParseAllSites' -count=1`
Expected: compile failure because `TaskRequest.Market` and flags do not exist.

- [ ] **Step 4: Implement CLI contract**

Add `Market MarketContext`. Add three flags, call `resolveMarketContext` once after `fs.Parse`, assign the result, and remove `validSite`/the old HK-or-SG error. A temporary `TaskRequest.Site` compatibility field may remain only until Task 6 because untouched browser/API call sites must continue compiling; Task 6 must remove it. Invalid errors name the flag and sorted supported values without session data.

- [ ] **Step 5: Verify GREEN and help**

```bash
gofmt -w internal/tripcdp/types.go internal/tripcdp/cli.go internal/tripcdp/cli_test.go
go test ./internal/tripcdp -run 'TestParseMarket|TestParseAllSites|TestParseFreshProfile' -count=1
go test ./internal/tripcdp -count=1
go run ./cmd/trip-score-cdp --help 2>&1 | rg -- '-(site|locale|currency|fresh-profile)'
```

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/types.go internal/tripcdp/cli.go internal/tripcdp/cli_test.go
git commit -m "feat: resolve Trip market flags in CLI"
```

---

### Task 3: Navigation and Worker Propagation

**Files:**
- Modify: `internal/tripcdp/navigation.go:8-96`
- Modify: `internal/tripcdp/navigation_test.go:8-88`
- Modify: `internal/tripcdp/runner.go:70-92,333-361`
- Modify: `internal/tripcdp/runner_test.go:1-135`

**Interfaces:**
- Consumes `TaskRequest.Market`.
- Produces:

```go
type navigationTarget struct {
    OriginalURL, NavigateURL string
    Market MarketContext
}
func newNavigationTarget(rawURL string, market MarketContext) navigationTarget
func applyMarketBrowserDefaults(request *TaskRequest)
```

- [ ] **Step 1: Write failing navigation table**

```go
func TestNavigationTargetUsesResolvedMarket(t *testing.T) {
    for _, site := range []string{"hk","sg","jp","tw","de","sa"} {
        market,_ := resolveMarketContext(site,"","")
        target := newNavigationTarget("https://hk.trip.com/things-to-do/detail/45892282/?foo=bar&curr=CNY", market)
        u,err := url.Parse(target.NavigateURL); if err != nil { t.Fatal(err) }
        if u.Hostname()!=market.Host || u.Query().Get("locale")!=market.Locale || u.Query().Get("curr")!=market.Currency || u.Query().Get("foo")!="bar" || target.Market.Code!=site {
            t.Errorf("%s: %+v",site,target)
        }
    }
}
```

- [ ] **Step 2: Write failing browser-language test**

```go
func TestApplyMarketBrowserDefaults(t *testing.T) {
    market,_ := resolveMarketContext("jp","","")
    r := TaskRequest{Market:market}
    applyMarketBrowserDefaults(&r)
    if r.BrowserOptions.Language != market.AcceptLanguage { t.Fatalf("%q",r.BrowserOptions.Language) }
}
```

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/tripcdp -run 'TestNavigationTargetUsesResolvedMarket|TestApplyMarketBrowserDefaults' -count=1`
Expected: compile failure for the new signatures/helper.

- [ ] **Step 4: Implement navigation migration**

Stop navigation and runner code from using `siteConfig`, `siteConfigFor`, `validSite`, `IsHK`, or variadic default behavior. Non-Trip URLs remain unchanged; Trip URLs use the exact resolved host/locale/currency. Replace input-host-based `preferredLanguageForItems` with the request market's Accept-Language. Both single and worker runner paths call `applyMarketBrowserDefaults`. Minimal compatibility symbols required by untouched browser/API extraction may remain through Tasks 3-5 so each intermediate commit compiles; Task 6 must delete them after migrating all callers.

- [ ] **Step 5: Verify GREEN**

```bash
gofmt -w internal/tripcdp/navigation.go internal/tripcdp/navigation_test.go internal/tripcdp/runner.go internal/tripcdp/runner_test.go
go test ./internal/tripcdp -run 'TestNavigationTarget|TestApplyMarketBrowserDefaults|TestPrepareRuntimeSessionFresh' -count=1
go test ./internal/tripcdp -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/navigation.go internal/tripcdp/navigation_test.go internal/tripcdp/runner.go internal/tripcdp/runner_test.go
git commit -m "refactor: drive navigation from market context"
```


---

### Task 4: Expected-Currency DOM Price Parsing

**Files:**
- Create: `internal/tripcdp/price_text.go`
- Create: `internal/tripcdp/price_text_test.go`
- Modify: `internal/tripcdp/browser.go:3536-3567`
- Modify: `internal/tripcdp/input_export_test.go:147-152,388-393,1022-1032`

**Interfaces:**
- Consumes `MarketContext.Currency` and `PricePrefixes`.
- Produces:

```go
func parseDisplayPrice(value string, market MarketContext) (currency, amount string)
func isValidDisplayPriceForMarket(value string, market MarketContext) bool
```

- [ ] **Step 1: Write failing representative tests**

```go
func TestParseDisplayPriceUsesExpectedMarket(t *testing.T) {
    cases := []struct{site,text,currency,amount string}{
        {"hk","HK$ 1,234.50","HKD","1234.50"},
        {"sg","S$ 0.11","SGD","0.11"},
        {"tw","NT$ 299","TWD","299"},
        {"jp","¥ 2,500","JPY","2500"},
        {"kr","₩ 15,000","KRW","15000"},
        {"th","฿ 899.00","THB","899.00"},
        {"de","€ 19.95","EUR","19.95"},
        {"uk","£ 8.50","GBP","8.50"},
        {"sa","SAR 42.00","SAR","42.00"},
        {"in","₹ 999","INR","999"},
    }
    for _,c := range cases {
        market,_ := resolveMarketContext(c.site,"","")
        currency,amount := parseDisplayPrice(c.text,market)
        if currency!=c.currency || amount!=c.amount || !isValidDisplayPriceForMarket(c.text,market) {
            t.Errorf("%s: %q %q",c.site,currency,amount)
        }
    }
}
```

- [ ] **Step 2: Write ambiguity/rejection tests**

```go
func TestParseDisplayPriceRejectsWrongPrefix(t *testing.T) {
    hk,_ := resolveMarketContext("hk","","")
    jp,_ := resolveMarketContext("jp","","")
    if c,a := parseDisplayPrice("S$ 10",hk); c!="N/A" || a!="N/A" { t.Fatalf("%q %q",c,a) }
    if c,a := parseDisplayPrice("$ 10",jp); c!="N/A" || a!="N/A" { t.Fatalf("%q %q",c,a) }
    for _,text := range []string{"","N/A","价格未找到","JPY --","€ free"} {
        if isValidDisplayPriceForMarket(text,jp) { t.Errorf("accepted %q",text) }
    }
}
```

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/tripcdp -run 'TestParseDisplayPrice' -count=1`
Expected: compile failure because helpers do not exist.

- [ ] **Step 4: Implement parser**

Sort expected prefixes longest-first, require one at the beginning after trimming, then extract `([0-9][0-9,]*(?:\\.[0-9]+)?)`. Do not convert decimal separators or currencies. Remove `validDisplayPricePattern`. Migrate production call sites to accept `MarketContext`. A temporary `splitPrice` wrapper may remain only until Task 6; Task 6 must remove it when no production caller remains.

- [ ] **Step 5: Verify GREEN**

```bash
gofmt -w internal/tripcdp/price_text.go internal/tripcdp/price_text_test.go internal/tripcdp/browser.go internal/tripcdp/input_export_test.go
go test ./internal/tripcdp -run 'TestParseDisplayPrice|TestIsValidDisplayPrice|TestSplitPrice' -count=1
go test ./internal/tripcdp -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/price_text.go internal/tripcdp/price_text_test.go internal/tripcdp/browser.go internal/tripcdp/input_export_test.go
git commit -m "feat: parse DOM prices by expected market"
```

---

### Task 5: Returned Market-Context Hard Gate

**Files:**
- Modify: `internal/tripcdp/market_context.go`
- Modify: `internal/tripcdp/market_context_test.go`
- Modify: `internal/tripcdp/browser.go:133-184,756-935,2535-2670`
- Modify: `internal/tripcdp/page_health_test.go:1-115`

**Interfaces:**
- Produces:

```go
type marketObservation struct { Host, SiteID, Locale, Currency string }
type marketContextError struct {
    Class string
    Expected MarketContext
    Observed marketObservation
}
func validateMarketObservation(expected MarketContext, observed marketObservation) error
```

- [ ] **Step 1: Write failing validation tests**

```go
func TestValidateMarketObservation(t *testing.T) {
    expected,_ := resolveMarketContext("sg","zh-SG","MYR")
    cases := []struct{name string; observed marketObservation; class string}{
        {"match",marketObservation{"sg.trip.com","SG","zh-SG","MYR"},""},
        {"host",marketObservation{"hk.trip.com","SG","zh-SG","MYR"},"market_context_mismatch"},
        {"locale",marketObservation{"sg.trip.com","SG","en-SG","MYR"},"market_context_mismatch"},
        {"currency",marketObservation{"sg.trip.com","SG","zh-SG","SGD"},"market_context_mismatch"},
        {"missing",marketObservation{Host:"sg.trip.com"},"market_context_unverified"},
    }
    for _,c := range cases {
        err := validateMarketObservation(expected,c.observed)
        if c.class=="" && err!=nil { t.Errorf("%s: %v",c.name,err) }
        if c.class!="" {
            var e *marketContextError
            if !errors.As(err,&e) || e.Class!=c.class { t.Errorf("%s: %#v",c.name,err) }
        }
    }
}
```

- [ ] **Step 2: Write failing page-gate test**

Extend `pageSnapshot` with `Market marketObservation` and `MarketContextClass string`:

```go
func TestMarketContextFailureBlocksPriceExtraction(t *testing.T) {
    for _,class := range []string{"market_context_mismatch","market_context_unverified"} {
        s := pageSnapshot{URL:"https://sg.trip.com/things-to-do/detail/45892282/",ReadyState:"complete",BodyTextLength:2000,HasProductTitle:true,HasPrice:true,MarketContextClass:class}
        if isProductPageExtractable(s,false) { t.Errorf("%s accepted",class) }
    }
}
```

- [ ] **Step 3: Verify RED**

Run: `go test ./internal/tripcdp -run 'TestValidateMarketObservation|TestMarketContextFailureBlocksPriceExtraction' -count=1`
Expected: compile failure for observation/error/snapshot types.

- [ ] **Step 4: Implement browser observation**

Add `readMarketObservationJS`. It must always read final `window.location.hostname`; then read only preference names (`ibusite`, `ibulocale`, `cookiePricesDisplayed`) and scan JSON in `[data-cargo]` for one object containing site, locale, and currency. Normalize public values and return only host/site/locale/currency. Never return or log cookie values, headers, storage, or user identifiers.

The JavaScript algorithm is:

```javascript
() => {
  const out={host:(location.hostname||'').toLowerCase(),site_id:'',locale:'',currency:''};
  const norm=v=>String(v||'').replace('_','-');
  const cookies=Object.fromEntries((document.cookie||'').split(';').map(p=>{
    const i=p.indexOf('='); return i<0?[p.trim(),'']:[p.slice(0,i).trim(),decodeURIComponent(p.slice(i+1))];
  }));
  out.site_id=String(cookies.ibusite||'').toUpperCase();
  out.locale=norm(cookies.ibulocale||'');
  out.currency=String(cookies.cookiePricesDisplayed||'').toUpperCase();
  const visit=v=>{
    if(!v||typeof v!=='object') return false;
    const site=v.site||v.siteId||v.siteID;
    if(site&&v.locale&&v.currency){
      out.site_id=String(site).toUpperCase(); out.locale=norm(v.locale);
      out.currency=String(v.currency).toUpperCase(); return true;
    }
    return Object.values(v).some(visit);
  };
  for(const el of document.querySelectorAll('[data-cargo]')){
    try{if(visit(JSON.parse(el.getAttribute('data-cargo')||'{}')))break;}catch(_){}
  }
  return out;
}
```

- [ ] **Step 5: Gate extraction**

Validate after navigation readiness and before `extractRows`. On failure emit `market_context_failed` with public expected/observed fields, skip SKU/API price acceptance, and produce `currency_mismatch` or `untrusted_market_context`. Use existing retry bounds only. Rating-only mode may continue only when host matches and must still emit unverified context.

- [ ] **Step 6: Verify GREEN**

```bash
gofmt -w internal/tripcdp/market_context.go internal/tripcdp/market_context_test.go internal/tripcdp/browser.go internal/tripcdp/page_health_test.go
go test ./internal/tripcdp -run 'TestValidateMarketObservation|TestMarketContextFailureBlocksPriceExtraction|TestPageSnapshot|TestSessionStorageOriginOnlyOnce' -count=1
go test ./internal/tripcdp -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/tripcdp/market_context.go internal/tripcdp/market_context_test.go internal/tripcdp/browser.go internal/tripcdp/page_health_test.go
git commit -m "feat: reject mismatched Trip market contexts"
```

---

### Task 6: Integrate Context Across Extraction and Quality

**Files:**
- Modify: `internal/tripcdp/browser.go:625-754,935-1000,1792-2176,2535-2573,3311-3374`
- Modify: `internal/tripcdp/api_sku_prices.go:1-150`
- Modify: `internal/tripcdp/api_sku_prices_test.go:1-120`
- Modify: `internal/tripcdp/input_export_test.go:1022-1044`
- Modify: `internal/tripcdp/currency_guard_test.go:1-40`

**Interfaces:**
- Consumes `TaskRequest.Market`, `navigationTarget.Market`, price helpers, and returned-context validation.
- Produces one consistent context for URL, API, DOM, price, row-quality, and event paths.

- [ ] **Step 1: Write failing propagation tests**

```go
func TestUnexpectedCurrencyRowsUseExpectedCurrency(t *testing.T) {
    rows:=[]map[string]string{{"货币":"USD","外显价格":"10","实价":"9","促销标签":"sale"}}
    invalidateUnexpectedCurrencyRows(rows,"JPY")
    if rows[0]["价格状态"]!="currency_mismatch" || rows[0]["货币"]!="" || rows[0]["外显价格"]!="" { t.Fatalf("%+v",rows[0]) }
}
func TestResolvedMarketPricePrefix(t *testing.T) {
    jp,_:=resolveMarketContext("jp","","")
    if !isValidDisplayPriceForMarket("¥ 2,500",jp) || isValidDisplayPriceForMarket("HK$ 2,500",jp) { t.Fatal("JPY gate incorrect") }
}
```

Add an API fixture without response currency and assert `parseCapturedAPISKUPrices(responses,"JPY",true)` produces `currency=JPY` and `real_currency=JPY`.

- [ ] **Step 2: Verify RED/wrong behavior**

Run: `go test ./internal/tripcdp -run 'TestUnexpectedCurrencyRowsUseExpectedCurrency|TestResolvedMarketPricePrefix|TestCapturedAPISKUPrices' -count=1`
Expected: at least one failure while old `siteConfigFor(s.request.Site)` or HK/SG parsing remains.

- [ ] **Step 3: Replace every old lookup**

Inventory:

```bash
rg -n 'siteConfigFor|request\\.Site|PricePrefix|splitPrice\\(|isValidDisplayPrice\\(' internal/tripcdp
```

Replace navigation, expected-row currency, API SKU parsing, batch-promotion fallback, page-health selectors, current-price selectors, and bundle extraction with `s.request.Market`/`target.Market`. Accept DOM price evidence only when the `currency` attribute equals the expected ISO currency or text begins with an expected prefix; never trust a bare number.

- [ ] **Step 4: Add safe structured fields**

Add `market_code`, `market_site_id`, `market_host`, `market_locale`, and `market_currency` to existing `url_started`/`page_loaded`/`url_done`/`run_done` events where applicable. Do not add cookies, auth, user IDs, session contents, or full headers.

- [ ] **Step 5: Prove stale production lookups are gone**

Run: `rg -n 'siteConfigFor|request\\.Site|validDisplayPricePattern' internal/tripcdp --glob '*.go'`
Expected: no production matches.

- [ ] **Step 6: Verify GREEN**

```bash
gofmt -w internal/tripcdp/browser.go internal/tripcdp/api_sku_prices.go internal/tripcdp/api_sku_prices_test.go internal/tripcdp/input_export_test.go internal/tripcdp/currency_guard_test.go
go test ./internal/tripcdp -run 'TestCapturedAPISKUPrices|TestUnexpectedCurrencyRows|TestResolvedMarketPricePrefix|TestBatchPromotionRowUsesRequestedCurrency' -count=1
go test ./... -count=1
go vet ./...
go build -o /tmp/trip-score-cdp-all-sites ./cmd/trip-score-cdp
```

- [ ] **Step 7: Commit**

```bash
git add internal/tripcdp/browser.go internal/tripcdp/api_sku_prices.go internal/tripcdp/api_sku_prices_test.go internal/tripcdp/input_export_test.go internal/tripcdp/currency_guard_test.go
git commit -m "feat: apply market context across Trip extraction"
```


---

### Task 7: Documentation and Representative Public Acceptance

**Files:**
- Modify: `README.md:20-105`
- Create: `docs/trip-things-to-do-market-compatibility.md`
- Verify unchanged: `hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md`
- Verify unchanged: `hermes-agent/plugins/trip-manual-delivery-guard/__init__.py`
- Verify unchanged: `docs/2026-08-03-hermes-manual-scrape-stability-spec.md`

**Interfaces:**
- Consumes the final CLI from Tasks 1-6.
- Produces operator documentation and dated public probe evidence; no code API.

- [ ] **Step 1: Document exact precedence and examples**

README must say: `--site` selects host and defaults; `--locale` and `--currency` override presentation/pricing; non-default combinations succeed only if the returned-context gate confirms them. Include:

```bash
/tmp/trip-score-cdp-all-sites --site jp --fresh-profile --no-headless \
  --delay-profile normal \
  --input "https://hk.trip.com/things-to-do/detail/45892282/" \
  --output /tmp/trip-jp-45892282.xlsx

/tmp/trip-score-cdp-all-sites --site sg --locale zh-SG --currency MYR \
  --fresh-profile --no-headless --delay-profile normal \
  --input "https://hk.trip.com/things-to-do/detail/45892282/" \
  --output /tmp/trip-sg-zh-myr-45892282.xlsx
```

- [ ] **Step 2: Create dated compatibility evidence**

`docs/trip-things-to-do-market-compatibility.md` contains observation date `2026-08-06`, timezone `Asia/Shanghai`, the 33-host table, 60 locales, the distinction between host defaults/advertised locales/product availability, cookie names only (`ibusite`, `ibugroup`, `ibulanguage`, `ibulocale`, `cookiePricesDisplayed`, `ibu_country`), all failure classifications, and a statement that public probes are not authenticated business acceptance.

- [ ] **Step 3: Verify Hermes guard before live runs**

```bash
python3 -m unittest tests/test_trip_manual_delivery_guard.py
git diff --exit-code -- hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md \
  hermes-agent/plugins/trip-manual-delivery-guard/__init__.py \
  docs/2026-08-03-hermes-manual-scrape-stability-spec.md
```

Expected: both commands exit 0.

- [ ] **Step 4: Run representative contexts serially**

Use product `45892282`, one worker, visible Chrome, normal delay, fresh profile, and no reused session. Execute these nine commands separately and inspect each result before starting the next:

```bash
/tmp/trip-score-cdp-all-sites --site hk --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-hk-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site sg --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-sg-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site tw --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-tw-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site jp --fresh-profile --workers 1 \
  --no-headless --delay-profile normal --json-lines \
  --input "https://hk.trip.com/things-to-do/detail/45892282/" \
  --output "/tmp/trip-market-acceptance-jp-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site kr --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-kr-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site th --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-th-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site us --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-us-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site de --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-de-45892282.xlsx"
/tmp/trip-score-cdp-all-sites --site sa --fresh-profile --workers 1 --no-headless --delay-profile normal --json-lines --input "https://hk.trip.com/things-to-do/detail/45892282/" --output "/tmp/trip-market-acceptance-sa-45892282.xlsx"
```

Stop immediately on captcha, MFA, user takeover, or browser task-space ownership loss. Existing bounded risk retry remains the maximum.

- [ ] **Step 5: Inspect and record each run**

Record:

```text
site | requested locale | requested currency | final host | observed site |
observed locale | observed currency | product status | row count |
price statuses | run_done.success | evidence path
```

Acceptance:

- context fields must match;
- sellable products need at least one row, correct ISO currency, and only `trusted_base`/`trusted_promo`;
- `unavailable` is acceptable context evidence only if market fields match;
- `risk_blocked`, unverified/mismatched context, currency mismatch, unexplained zero rows, or absent `run_done.success` blocks that context;
- an XLSX file alone is insufficient.

- [ ] **Step 6: Run final current verification**

```bash
go test ./... -count=1
go vet ./...
go build -o /tmp/trip-score-cdp-all-sites ./cmd/trip-score-cdp
python3 -m unittest tests/test_trip_manual_delivery_guard.py
git status --short
```

Expected: tests/vet/build/guard exit 0. Generated XLSX/log files stay under `/tmp`.

- [ ] **Step 7: Commit**

```bash
git add README.md docs/trip-things-to-do-market-compatibility.md
git commit -m "docs: record Trip multi-market compatibility"
```

---

## Final Primary-Agent Review

- [ ] Confirm seven focused commits and no unrelated files.
- [ ] Inspect the complete diff from the literal base SHA captured before Task 1.
- [ ] Search stale assumptions:

```bash
rg -n 'hk\\|sg|siteConfigFor|request\\.Site|validDisplayPricePattern|Trip subsite to scrape: hk or sg' internal README.md
```

HK references remain only where they test the default or enforce the Hermes manual contract.

- [ ] Run fresh verification:

```bash
go test ./... -count=1
go vet ./...
go build -o /tmp/trip-score-cdp-all-sites ./cmd/trip-score-cdp
python3 -m unittest tests/test_trip_manual_delivery_guard.py
git status --short --branch
```

- [ ] Confirm the evidence separates implementation, automated tests, public probes, and authenticated business acceptance.
- [ ] Confirm no cookies, tokens, authorization, full environment values, session contents, or user identifiers entered the diff.
- [ ] Completion wording lists representative passes/blocks, states that remaining registry entries have configuration/unit coverage only, and says authenticated business acceptance was not performed.

## Self-Review

**Spec coverage**

- Host inventory and 60 locales → Task 1.
- Site/locale/currency separation and CLI precedence → Tasks 1-3.
- URL, Accept-Language, and worker propagation → Task 3.
- Multi-currency DOM and API-first ISO handling → Tasks 4 and 6.
- Returned-context assertion → Task 5.
- Quality/events/extraction integration → Task 6.
- Fresh-profile, public probes, and evidence boundary → Task 7.
- Hermes HK-only preservation → Global Constraints and Task 7.
- No deployment/authenticated acceptance claim → Global Constraints and Final Review.

**Placeholder scan**

No deferred marker, unspecified implementation step, unnamed error handling, or unnamed test command remains. The 33-site baseline enumerates every literal row, and the registry test requires one assertion fixture per row. Operational commands use only the explicitly enumerated site codes.

**Type consistency**

`MarketContext` → `TaskRequest.Market` → `navigationTarget.Market` is unchanged across tasks. `marketObservation` and `marketContextError` are introduced and consumed in Task 5. `parseDisplayPrice`/`isValidDisplayPriceForMarket` are introduced in Task 4 and consumed in Task 6. Tasks are serialized wherever files or interfaces overlap.
