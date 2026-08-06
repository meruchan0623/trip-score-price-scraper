package tripcdp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type tripScraper struct {
	request                    TaskRequest
	emit                       Reporter
	vendor                     string
	workerID                   int
	globalTotal                int
	browser                    *rod.Browser
	page                       *rod.Page
	userDataDir                string
	removeUserDataDir          bool
	maxSKULayers               int
	processed                  int
	driverRuntime              *chromeDriverRuntime
	directChrome               *exec.Cmd
	storageApplied             map[string]bool
	restartAfterURL            bool
	restartReason              string
	riskProtection             bool
	currentScrapeNote          string
	currentQualityFailure      *priceQualityTracker
	currentQualityRetryAttempt int
	currentTarget              navigationTarget
}

type skuLayerInfo struct {
	Index int
	Title string
}

type skuOption struct {
	Text  string `json:"text"`
	Layer int    `json:"layer"`
	ID    string `json:"id"`
}

const (
	skuLayerWaitMax    = 8 * time.Second
	skuLayerWaitPoll   = 300 * time.Millisecond
	skuOptionsWaitMax  = 5 * time.Second
	skuOptionsWaitPoll = 300 * time.Millisecond
)

const (
	priceStatusTrustedPromo           = "trusted_promo"
	priceStatusTrustedBase            = "trusted_base"
	priceStatusUntrustedResourceMiss  = "untrusted_resource_miss"
	priceStatusUntrustedPriceMissing  = "untrusted_price_missing"
	priceStatusUntrustedMarketContext = "untrusted_market_context"
	priceStatusRiskBlocked            = "risk_blocked"
	priceStatusPartialUnhealthy       = "partial_unhealthy"

	priceSourceBatchPromotion = "batchSearchBookingSalesPromotion"
)

const (
	maxConsecutiveResourceMiss = 5
	minSamplesForResourceRate  = 10
	resourceMissRateLimit      = 0.80
	sameFallbackPriceStreak    = 8
)

type priceBundle struct {
	DisplayPrice    string `json:"display_price"`
	RealPrice       string `json:"real_price"`
	PromoTag        string `json:"promo_tag"`
	FooterText      string `json:"footer_text"`
	FooterSignature string `json:"footer_signature"`
	SampleAtMS      int    `json:"sample_at_ms"`
}

type batchPromotionPriceBundle struct {
	PackageID       int64   `json:"package_id"`
	ResourceID      int64   `json:"resource_id"`
	ItemID          int64   `json:"item_id"`
	Currency        string  `json:"currency"`
	OriginalPrice   float64 `json:"original_price"`
	DeductionAmount float64 `json:"deduction_amount"`
	DeductionPrice  float64 `json:"deduction_price"`
	PromoDisplay    string  `json:"promo_display"`
	PromoName       string  `json:"promo_name"`
	PromoMatched    bool    `json:"promo_matched"`
	ResponseStatus  int     `json:"response_status"`
	ResponseURL     string  `json:"response_url"`
	ResponseText    string  `json:"response_text"`
}

type productPackageDiagnostics struct {
	NextDataPresent    bool `json:"next_data_present"`
	PackageCount       int  `json:"package_count"`
	ResourceCount      int  `json:"resource_count"`
	BatchResponseCount int  `json:"batch_response_count"`
	LastResponseStatus int  `json:"last_response_status"`
}

func (d productPackageDiagnostics) ready() bool {
	return d.NextDataPresent && d.PackageCount > 0 && d.ResourceCount > 0
}

type baseRatingBundle struct {
	BookingCount   string `json:"booking_count"`
	ReviewCount    string `json:"review_count"`
	Score          string `json:"score"`
	Customer       string `json:"customer"`
	Convenience    string `json:"convenience"`
	Signal         string `json:"signal"`
	BadReviewCount string `json:"bad_review_count"`
	ProductInfo    string `json:"product_info"`
	Signature      string `json:"signature"`
	SampleAtMS     int    `json:"sample_at_ms"`
}

type capturedAPIResponse struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
	Text   string `json:"text"`
}

type capturedAPIFields struct {
	ProductName    string
	ProductInfo    string
	ReviewCount    string
	Score          string
	GoodRate       string
	Customer       string
	Convenience    string
	Signal         string
	BadReviewCount string
}

type productSalesVolume struct {
	Monthly int64 `json:"monthly_sales"`
	Yearly  int64 `json:"yearly_sales"`
	Total   int64 `json:"total_sales"`
}

type pageSnapshot struct {
	URL                string            `json:"url"`
	ReadyState         string            `json:"ready_state"`
	BodyTextLength     int               `json:"body_text_length"`
	Title              string            `json:"title"`
	HasProductTitle    bool              `json:"has_product_title"`
	HasSKU             bool              `json:"has_sku"`
	HasPrice           bool              `json:"has_price"`
	HasScore           bool              `json:"has_score"`
	RiskChallenge      bool              `json:"risk_challenge"`
	RiskKind           string            `json:"risk_kind"`
	CloudflareBlock    bool              `json:"cloudflare_block"`
	HTTPStatus         int               `json:"http_status"`
	ResponseURL        string            `json:"response_url"`
	BodyPreview        string            `json:"body_preview"`
	EvalError          string            `json:"eval_error"`
	Market             marketObservation `json:"market"`
	MarketContextClass string            `json:"market_context_class"`
}

const readMarketObservationJS = `() => {
	const out = {host: (location.hostname || '').toLowerCase(), site_id: '', locale: '', currency: ''};
	const norm = value => String(value || '').replace('_', '-');
	const preference = name => {
		for (const part of (document.cookie || '').split(';')) {
			const index = part.indexOf('=');
			if (index < 0 || part.slice(0, index).trim() !== name) continue;
			try { return decodeURIComponent(part.slice(index + 1)); } catch (_) { return part.slice(index + 1); }
		}
		return '';
	};
	out.site_id = String(preference('ibusite') || '').toUpperCase();
	out.locale = norm(preference('ibulocale'));
	out.currency = String(preference('cookiePricesDisplayed') || '').toUpperCase();
	const visit = value => {
		if (!value || typeof value !== 'object') return false;
		const site = value.site || value.siteId || value.siteID;
		if (site && value.locale && value.currency) {
			out.site_id = String(site).toUpperCase();
			out.locale = norm(value.locale);
			out.currency = String(value.currency).toUpperCase();
			return true;
		}
		return Object.values(value).some(visit);
	};
	for (const element of document.querySelectorAll('[data-cargo]')) {
		try { if (visit(JSON.parse(element.getAttribute('data-cargo') || '{}'))) break; } catch (_) {}
	}
	return out;
}`

type navigationMonitor struct {
	mu             sync.Mutex
	mainStatus     int
	mainURL        string
	mainStatusText string
	stop           func()
	cancel         context.CancelFunc
}

const currentPriceBundleJS = `(displaySelectors, includeReal, currency, pricePrefixes) => {
	const realSelector = 'div[class*="price-comp_m_footer_left_total"] div div div span span';
	const footerSelector = 'div[class*="price-comp_m_footer_left"]';
	const promoSelector = footerSelector + ' div span';
	const isVisible = (el) => {
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
		return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
	};
	const textOf = (el) => (el && (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ')) || '';
	const hasExpectedPrefix = (text) => pricePrefixes.some(prefix => text.startsWith(prefix));
	const validPrice = (el, text) => text && /[0-9,]+(?:\.\d+)?/.test(text) && (
		String(el.getAttribute('currency') || '').toUpperCase() === currency || hasExpectedPrefix(text)
	);
	let displayPrice = '';
	for (const selector of displaySelectors) {
		for (const el of document.querySelectorAll(selector)) {
			if (!isVisible(el)) continue;
			const text = textOf(el);
			if (validPrice(el, text)) {
				displayPrice = text;
				break;
			}
		}
		if (displayPrice) break;
	}
	if (!displayPrice) {
		for (const el of document.querySelectorAll('span,div')) {
			if (!isVisible(el)) continue;
			const text = textOf(el);
			if (validPrice(el, text)) {
				displayPrice = text;
				break;
			}
		}
	}
	let realPrice = '';
	if (includeReal) {
		for (const el of document.querySelectorAll(realSelector)) {
			if (!isVisible(el)) continue;
			const text = textOf(el);
			if (validPrice(el, text)) {
				realPrice = text;
				break;
			}
		}
	}
	let footerText = '';
	for (const el of document.querySelectorAll(footerSelector)) {
		if (!isVisible(el)) continue;
		const text = textOf(el);
		if (text) {
			footerText = footerText ? footerText + ' | ' + text : text;
		}
	}
	let promoTag = '';
	for (const el of document.querySelectorAll(promoSelector)) {
		if (!isVisible(el)) continue;
		const text = textOf(el);
		if (text && !hasExpectedPrefix(text) && (text.includes('減') || text.includes('%') || text.includes('折') || /off/i.test(text))) {
			promoTag = text;
			break;
		}
	}
	return {display_price: displayPrice, real_price: realPrice, promo_tag: promoTag, footer_text: footerText, footer_signature: [displayPrice, realPrice, promoTag, footerText].join('||')};
}`

const totalPromoTextJS = `() => {
	const selectors = ['span[testid="act-promotionText"]', 'span[testid="act-discountTextOtherDesc"]'];
	const values = [];
	for (const selector of selectors) {
		for (const el of document.querySelectorAll(selector)) {
			if (el.offsetParent === null) continue;
			const text = (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ');
			if (text && !values.includes(text)) values.push(text);
		}
	}
	return values;
}`

const totalPromoContentJS = `() => {
	const nextScript = document.querySelector('script#__NEXT_DATA__');
	try {
		const data = JSON.parse((nextScript && nextScript.textContent) || '{}');
		const product = data.props.pageProps.initialState.__STATE_MAP__.PageModel.ds.getProductInfo[0];
		const preferences = (((product || {}).saleDiscountInfo || {}).preferences) || [];
		return preferences.map((item) => String((item || {}).tagDesc || '').trim().replace(/\s+/g, ' ')).filter(Boolean);
	} catch (e) {
		return [];
	}
}`

const productSalesVolumeJS = `(expectedProductID) => {
	const nextScript = document.querySelector('script#__NEXT_DATA__');
	try {
		const data = JSON.parse((nextScript && nextScript.textContent) || '{}');
		const product = data?.props?.pageProps?.initialState?.__STATE_MAP__?.PageModel?.ds?.getProductInfo?.[0] || {};
		const productID = String(product.productId || product.basicInfo?.productId || '');
		if (!expectedProductID || productID !== String(expectedProductID)) return {};
		const volume = product.salesVolume || {};
		return {monthly_sales: Number(volume.monthlySales || 0), yearly_sales: Number(volume.yearlySales || 0), total_sales: Number(volume.twoYearSales || 0)};
	} catch (e) {
		return {};
	}
}`

const promotionContentCaptureJS = `(() => {
	window.__tripCdpProductPromotionResponses = [];
	const keep = (url) => {
		const value = String(url || '');
		return value.includes('/restapi/soa2/21052/getProductInfo') ||
			value.includes('/restapi/soa2/14580/getProductPriceCalendar') ||
			value.includes('/restapi/soa2/18084/commonQueryCommentSummary') ||
			value.includes('/restapi/soa2/20047/listComments');
	};
	const push = (url, status, text) => {
		try {
			window.__tripCdpProductPromotionResponses.push({url: String(url || ''), status: Number(status || 0), text: String(text || '')});
			window.__tripCdpProductPromotionResponses = window.__tripCdpProductPromotionResponses.slice(-10);
		} catch (e) {}
	};
	const originalFetch = window.fetch;
	if (typeof originalFetch === 'function') {
		window.fetch = async function(input, init) {
			const response = await originalFetch.apply(this, arguments);
			const url = typeof input === 'string' ? input : (input && input.url) || response.url;
			if (keep(url)) response.clone().text().then((text) => push(url, response.status, text)).catch(() => {});
			return response;
		};
	}
	const OriginalXHR = window.XMLHttpRequest;
	if (OriginalXHR && OriginalXHR.prototype) {
		const originalOpen = OriginalXHR.prototype.open;
		const originalSend = OriginalXHR.prototype.send;
		OriginalXHR.prototype.open = function(method, url) {
			this.__tripCdpProductPromotionURL = url;
			return originalOpen.apply(this, arguments);
		};
		OriginalXHR.prototype.send = function() {
			this.addEventListener('load', function() {
				const url = this.__tripCdpProductPromotionURL || '';
				if (keep(url)) push(url, this.status, this.responseText || '');
			});
			return originalSend.apply(this, arguments);
		};
	}
})();`

const baseRatingBundleJS = `(includeProductInfo) => {
	const isVisible = (el) => {
		if (!el) return false;
		const style = window.getComputedStyle(el);
		if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
		return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
	};
	const textOf = (el) => (el && (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ')) || '';
	const cssText = (selector) => {
		const el = document.querySelectorAll(selector)[0];
		return textOf(el);
	};
	const xpathText = (xpath) => {
		const snapshot = document.evaluate(xpath, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
		return textOf(snapshot.snapshotItem(0));
	};
	const numberText = (text) => {
		const match = (text || '').match(/([0-9,]+(?:\.\d+)?)/);
		return match ? match[1].replace(/,/g, '') : '';
	};
	const parenthesizedNumberText = (text) => {
		const match = (text || '').match(/\(([0-9,]+)/);
		return match ? match[1].replace(/,/g, '') : '';
	};
	const scoreFromText = (text) => {
		const match = (text || '').match(/(\d+(?:\.\d+)?)/);
		return match ? match[1] : '';
	};
	const bookingCountText = (text) => {
		if (!text) return '';
		if (text.includes('次預訂')) {
			return text.split('次預訂')[0].trim().replace(/,/g, '');
		}
		return text.replace(/,/g, '');
	};
	const badReviewNumberText = (text) => {
		const match = (text || '').match(/\(([0-9,]+)\)/);
		return match ? match[1].replace(/,/g, '') : '';
	};
	const bookingText = cssText('span[testid="saleInfoId"]');
	const reviewText = cssText('span[testid="productdetail-productInfo-commentCountDesc"]');
	const score = cssText('span[testid="productdetail-productInfo-commentScore"]');
	const customerText = score ? xpathText("//div[text()='客戶服務']/following-sibling::span") : '';
	const convenienceText = score ? xpathText("//div[text()='方便程度']/following-sibling::span") : '';
	const signalText = score ? xpathText("//div[text()='訊號狀態']/following-sibling::span") : '';
	const badReviewText = score ? xpathText("//div[contains(@class, 'ct-review-list-filiter-label-item') and contains(., '中性評價及負面評價')]") : '';
	let productInfo = '';
	if (includeProductInfo) {
		for (const selector of ['#group_1001 > div > div > div[class*="style_content_box"] > div > div', 'div[class*="activity_product_detail_html_box"]', '#group_1001']) {
			for (const el of document.querySelectorAll(selector)) {
				if (!isVisible(el)) continue;
				let text = textOf(el);
				if (!text) {
					text = Array.from(el.querySelectorAll('p')).map((p) => textOf(p)).filter(Boolean).join('\n');
				}
				if (text) {
					productInfo = text;
					break;
				}
			}
			if (productInfo) break;
		}
	}
	const out = {
		booking_count: bookingCountText(bookingText),
		review_count: score ? parenthesizedNumberText(reviewText) : '',
		score,
		customer: score ? scoreFromText(customerText) : '',
		convenience: score ? scoreFromText(convenienceText) : '',
		signal: score ? scoreFromText(signalText) : '',
		bad_review_count: score ? badReviewNumberText(badReviewText) : '',
		product_info: productInfo,
	};
	out.signature = [out.score, out.customer, out.convenience, out.signal, out.bad_review_count, out.product_info].join('||');
	return out;
}`

func newTripScraper(request TaskRequest, emit Reporter, vendor string, workerID int, globalTotal int) *tripScraper {
	return &tripScraper{request: request, emit: emit, vendor: vendor, workerID: workerID, globalTotal: globalTotal, storageApplied: map[string]bool{}}
}

func (s *tripScraper) start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.request.BrowserOptions.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.request.BrowserOptions.Timeout)
		defer cancel()
	}
	s.emit(Event{"event": "worker_ready", "message": "worker ready"})
	s.emit(Event{"event": "chrome_detect_started", "stage": "chrome_detect", "message": "detecting Chrome"})
	started := time.Now()
	driverInfo, err := prepareUndetectedChromeDriver(ctx, s.request.BrowserOptions)
	if err != nil {
		s.emit(Event{"event": "chrome_detect_failed", "stage": "chrome_detect", "error_type": fmt.Sprintf("%T", err), "error": err.Error()})
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.emit(Event{"event": "chrome_detect_done", "stage": "chrome_detect", "message": fmt.Sprintf("chrome=%s version_main=%s", driverInfo.ChromePath, valueOr(driverInfo.VersionMain, "unknown")), "chrome_path": driverInfo.ChromePath, "version_main": driverInfo.VersionMain})
	s.emit(Event{"event": "browser_prepare", "stage": "driver_prepare", "message": fmt.Sprintf("chromedriver=%s reused=%v", driverInfo.DriverPath, driverInfo.DriverReused), "driver_path": driverInfo.DriverPath, "driver_cache_dir": driverInfo.CacheDir, "driver_reused": driverInfo.DriverReused, "driver_version": driverInfo.VersionFull, "elapsed_ms": driverInfo.PrepareMS})
	s.emit(Event{"event": "browser_start_started", "message": "starting Chrome"})
	profileSourceDir := resolveChromeUserDataDir(s.request.BrowserOptions.UserDataDir)
	userDataDir := profileSourceDir
	profileMode := "configured_profile"
	removeUserDataDir := false
	if isDefaultChromeUserDataDir(s.request.BrowserOptions.UserDataDir) {
		userDataDir, err = os.MkdirTemp("", "trip_cdp_chrome_profile_*")
		if err != nil {
			return err
		}
		profileMode = "copied_local_profile"
		removeUserDataDir = true
		s.emit(Event{"event": "browser_prepare", "stage": "profile_copy", "message": "copying local Chrome profile for remote debugging", "source_profile_dir": profileSourceDir, "runtime_profile_dir": userDataDir, "profile_dir": userDataDir, "profile_mode": profileMode})
		if err := copyChromeProfile(profileSourceDir, userDataDir); err != nil {
			s.close()
			return err
		}
	} else {
		if err := os.MkdirAll(userDataDir, 0o755); err != nil {
			return err
		}
		if chromeProfileLocked(userDataDir) {
			return fmt.Errorf("Chrome profile is already in use: %s; close Chrome or use a copied worker profile", userDataDir)
		}
	}
	if err := fixChromeExitType(userDataDir); err != nil {
		s.emit(Event{"event": "warning", "stage": "browser_start", "message": "failed to fix Chrome profile exit_type", "error": err.Error(), "profile_dir": userDataDir})
	}
	s.userDataDir = userDataDir
	s.removeUserDataDir = removeUserDataDir
	debuggerPort, err := freeTCPPort()
	if err != nil {
		s.close()
		return err
	}
	args := chromeArguments(userDataDir, s.request.BrowserOptions.Headless, debuggerPort, s.request.BrowserOptions.Language)
	cmd := exec.Command(driverInfo.ChromePath, args...)
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		s.emit(Event{"event": "browser_start_failed", "message": "Chrome subprocess failed", "stage": "browser_start", "error_type": fmt.Sprintf("%T", err), "error": err.Error(), "elapsed_ms": elapsedMS(started)})
		s.close()
		return err
	}
	s.directChrome = cmd
	go func() { _ = cmd.Wait() }()
	debuggerAddress := fmt.Sprintf("127.0.0.1:%d", debuggerPort)
	controlURL, err := waitChromeDebugger(ctx, debuggerAddress)
	if err != nil {
		s.emit(Event{"event": "browser_start_failed", "message": "Chrome debugger failed", "stage": "browser_start", "error_type": fmt.Sprintf("%T", err), "error": err.Error(), "elapsed_ms": elapsedMS(started)})
		s.close()
		return err
	}
	s.emit(Event{"event": "browser_prepare", "stage": "browser_start", "message": "profile=" + userDataDir, "source_profile_dir": profileSourceDir, "runtime_profile_dir": userDataDir, "profile_dir": userDataDir, "profile_mode": profileMode, "debugger_address": debuggerAddress})
	runtime, err := startChromeDriver(ctx, driverInfo.DriverPath)
	if err != nil {
		s.emit(Event{"event": "browser_start_failed", "message": "chromedriver start failed", "stage": "browser_start", "error_type": fmt.Sprintf("%T", err), "error": err.Error(), "elapsed_ms": elapsedMS(started)})
		s.close()
		return err
	}
	s.driverRuntime = runtime
	if err := ctx.Err(); err != nil {
		s.close()
		return err
	}
	if err := runtime.attachSession(ctx, debuggerAddress); err != nil {
		s.emit(Event{"event": "browser_start_failed", "message": "chromedriver attach failed", "stage": "browser_start", "error_type": fmt.Sprintf("%T", err), "error": err.Error(), "elapsed_ms": elapsedMS(started)})
		s.close()
		return err
	}
	s.emit(Event{"event": "browser_prepare", "stage": "browser_start", "message": "chromedriver attached to debugger", "debugger_address": runtime.debuggerAddress})
	browser := rod.New().ControlURL(controlURL)
	if s.request.BrowserOptions.Timeout > 0 {
		browser = browser.Timeout(s.request.BrowserOptions.Timeout)
	}
	if err := connectRodBrowser(ctx, browser, runtime); err != nil {
		s.emit(Event{"event": "browser_start_failed", "message": "Chrome connect failed", "stage": "browser_start", "error_type": fmt.Sprintf("%T", err), "error": err.Error(), "elapsed_ms": elapsedMS(started)})
		s.close()
		return err
	}
	s.browser = browser
	s.emit(Event{"event": "browser_prepare", "stage": "browser_start", "message": "rod connected"})
	page, err := createRodPage(ctx, browser, runtime, s.request.BrowserOptions.Language)
	if err != nil {
		s.emit(Event{"event": "browser_start_failed", "message": "Chrome page failed", "stage": "browser_start", "error_type": fmt.Sprintf("%T", err), "error": err.Error(), "elapsed_ms": elapsedMS(started)})
		s.close()
		return err
	}
	s.page = page
	if err := ctx.Err(); err != nil {
		s.close()
		return err
	}
	s.emit(Event{"event": "log", "stage": "session_load", "message": "session load deferred until target navigation", "session_nav_skipped": true})
	s.emit(Event{"event": "browser_start_done", "message": "Chrome started", "elapsed_ms": elapsedMS(started)})
	return nil
}

func connectRodBrowser(ctx context.Context, browser *rod.Browser, runtime *chromeDriverRuntime) error {
	errCh := make(chan error, 1)
	go func() { errCh <- browser.Connect() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		if runtime != nil {
			runtime.stop(context.Background())
		}
		_ = browser.Close()
		return ctx.Err()
	}
}

func createRodPage(ctx context.Context, browser *rod.Browser, runtime *chromeDriverRuntime, acceptLanguage string) (*rod.Page, error) {
	pageCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	type result struct {
		page *rod.Page
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		page, err := reusableRodPage(browser)
		if err != nil || page == nil {
			page, err = browser.Page(proto.TargetCreateTarget{})
		}
		ch <- result{page: page, err: err}
	}()
	select {
	case result := <-ch:
		if result.err == nil && result.page != nil {
			_ = applyUCStealth(pageCtx, result.page, acceptLanguage)
			closeUnusedBlankPages(browser, result.page)
		}
		return result.page, result.err
	case <-pageCtx.Done():
		go func() {
			if runtime != nil {
				runtime.stop(context.Background())
			}
			_ = browser.Close()
		}()
		return nil, pageCtx.Err()
	}
}

func reusableRodPage(browser *rod.Browser) (*rod.Page, error) {
	pages, err := browser.Pages()
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		info, err := page.Info()
		if err != nil {
			continue
		}
		if isBlankPageURL(info.URL) {
			return page, nil
		}
	}
	return nil, nil
}

func closeUnusedBlankPages(browser *rod.Browser, keep *rod.Page) {
	pages, err := browser.Pages()
	if err != nil {
		return
	}
	for _, page := range pages {
		if keep != nil && page.TargetID == keep.TargetID {
			continue
		}
		info, err := page.Info()
		if err != nil || !isBlankPageURL(info.URL) {
			continue
		}
		_ = page.Close()
	}
}

func isBlankPageURL(pageURL string) bool {
	pageURL = strings.TrimSpace(strings.ToLower(pageURL))
	return pageURL == "" || pageURL == "about:blank" || strings.HasPrefix(pageURL, "chrome://newtab")
}

func (s *tripScraper) scrapeOne(ctx context.Context, item ScrapeItem, index int, total int) []map[string]string {
	started := time.Now()
	if s.request.Runtime.URLTotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.request.Runtime.URLTotalTimeout)
		defer cancel()
	}
	base := Event{"vendor": s.vendor, "worker_id": s.workerID, "url_index": index, "url_total": total, "url": item.URL, "global_total": s.globalTotal}
	target := newNavigationTarget(item.URL, s.request.Market)
	s.currentTarget = target
	canonicalItem := item
	canonicalItem.URL = target.NavigateURL
	s.restartAfterURL = false
	s.restartReason = ""
	s.currentScrapeNote = ""
	s.currentQualityFailure = nil
	s.currentQualityRetryAttempt = 0
	s.emit(mergeEvent(mergeEvent(Event{"event": "url_started", "first_trip_navigation_url": target.NavigateURL, "navigation_locale": target.Market.Locale, "navigation_currency": target.Market.Currency, "accept_language": target.Market.AcceptLanguage, "session_nav_skipped": true}, marketEventFields(target.Market)), base))
	var lastSnapshot pageSnapshot
	pageAttempt := 1
	priceQualityRetries := 0
	var firstQualityFailure *priceQualityTracker
	for pageAttempt <= 2 {
		if ctx.Err() != nil {
			return nil
		}
		s.currentQualityFailure = nil
		s.currentQualityRetryAttempt = priceQualityRetries
		s.restartAfterURL = false
		s.restartReason = ""
		rows, snapshot, retry, err := s.scrapeOneAttempt(ctx, canonicalItem, target, base, pageAttempt)
		lastSnapshot = snapshot
		if err != nil {
			return s.errorRows(canonicalItem, err, started, base)
		}
		invalidateUnexpectedCurrencyRows(rows, target.Market.Currency)
		if !retry {
			restartAfterURL := s.restartAfterURL
			restartReason := s.restartReason
			trustedRows, untrustedRows, qualityFailed := countPriceQualityRows(rows)
			qualityFailure := s.currentQualityFailure
			if s.shouldRetryPriceQuality(qualityFailure, trustedRows, priceQualityRetries) {
				if firstQualityFailure == nil {
					firstQualityFailure = qualityFailure
				}
				priceQualityRetries++
				retryStrategy := "reload_url"
				if priceQualityRetries >= 2 {
					retryStrategy = "restart_browser"
				}
				s.emit(mergeEvent(Event{"event": "price_quality_retry", "stage": "sku", "message": "price quality failed; retrying URL", "reason": qualityFailure.failReason, "failure_class": qualityFailure.failureClass, "retry_attempt": priceQualityRetries, "max_retries": s.request.PriceQualityRetry.MaxRetries, "retry_strategy": retryStrategy, "trusted_rows": trustedRows, "untrusted_rows": untrustedRows, "top_fallback_prices": qualityFailure.topFallbackPrices(3)}, base))
				s.currentScrapeNote = ""
				s.currentQualityFailure = nil
				s.clearBatchPromotionResponses()
				if priceQualityRetries >= 2 {
					if err := s.restartBrowser(ctx, "price quality retry: "+qualityFailure.failReason); err != nil {
						return s.errorRows(canonicalItem, err, started, base)
					}
				} else {
					s.cleanupAfterURL()
				}
				continue
			}
			if priceQualityRetries > 0 && trustedRows > 0 && firstQualityFailure != nil {
				firstQualityFailure.recovered = true
				s.emitPriceQualityRecovered(canonicalItem, firstQualityFailure, priceQualityRetries, trustedRows, untrustedRows, base)
			}
			if qualityFailure != nil && qualityFailure.failed {
				s.emitURLQualityFailed(canonicalItem, qualityFailure)
			} else if priceQualityRetries > 0 && trustedRows > 0 && firstQualityFailure != nil {
				qualityFailed = false
			}
			s.emit(mergeEvent(mergeEvent(Event{"event": "url_done", "rows": len(rows), "elapsed_ms": elapsedMS(started), "final_url": snapshot.URL, "page_valid": isProductPageReady(snapshot), "page_retry_count": pageAttempt - 1, "price_quality_retry_count": priceQualityRetries, "restart_after_url": restartAfterURL, "trusted_rows": trustedRows, "untrusted_rows": untrustedRows, "price_quality_failed": qualityFailed}, marketEventFields(target.Market)), base))
			if restartAfterURL {
				_ = s.restartBrowser(ctx, restartReason)
			}
			return rows
		}
		if isWhaleguardSnapshot(snapshot) {
			if pageAttempt >= 2 {
				break
			}
			cooldown := riskCooldownDelay(s.request.DelayOptions.Profile)
			s.emit(mergeEvent(Event{"event": "risk_challenge_detected", "stage": "risk", "message": "whaleguard block; cooling down and retrying URL", "risk_kind": snapshot.RiskKind, "http_status": snapshot.HTTPStatus, "body_preview": snapshot.BodyPreview, "cooldown_ms": cooldown.Milliseconds(), "retry_after_risk": true, "risk_retry_count": pageAttempt, "final_url": snapshot.URL}, base))
			s.cleanupAfterURL()
			if err := s.restartBrowserNoSessionSave(ctx, "whaleguard block"); err != nil {
				return s.errorRows(canonicalItem, err, started, base)
			}
			s.enableRiskProtection()
			if !sleepFixed(ctx, cooldown) {
				return nil
			}
			pageAttempt++
			continue
		}
		s.emit(mergeEvent(Event{"event": "page_retry", "stage": "page_open", "message": "page did not load valid product content; restarting browser and retrying URL", "attempt": pageAttempt, "max_attempts": 2, "snapshot": snapshot}, base))
		if err := s.restartBrowser(ctx, "page unhealthy before extraction"); err != nil {
			return s.errorRows(canonicalItem, err, started, base)
		}
		pageAttempt++
	}
	if isWhaleguardSnapshot(lastSnapshot) {
		s.emit(mergeEvent(Event{"event": "risk_challenge_detected", "stage": "risk", "message": "whaleguard block after retry", "risk_kind": lastSnapshot.RiskKind, "http_status": lastSnapshot.HTTPStatus, "body_preview": lastSnapshot.BodyPreview, "retry_after_risk": false, "risk_retry_count": 1, "final_url": lastSnapshot.URL}, base))
		rows := []map[string]string{s.riskBlockedRow(canonicalItem, "whaleguard block")}
		s.emit(mergeEvent(mergeEvent(Event{"event": "url_done", "rows": len(rows), "elapsed_ms": elapsedMS(started), "final_url": lastSnapshot.URL, "page_valid": false, "page_retry_count": 1, "restart_after_url": false, "risk_kind": lastSnapshot.RiskKind, "http_status": lastSnapshot.HTTPStatus}, marketEventFields(target.Market)), base))
		s.cleanupAfterURL()
		_ = s.restartBrowserNoSessionSave(ctx, "whaleguard block after retry")
		return rows
	}
	s.emit(mergeEvent(Event{"event": "page_unhealthy", "stage": "extract", "message": "页面未加载有效商品内容", "snapshot": lastSnapshot}, base))
	rows := []map[string]string{s.errorRow(canonicalItem, "页面未加载有效商品内容")}
	s.emit(mergeEvent(mergeEvent(Event{"event": "url_done", "rows": len(rows), "elapsed_ms": elapsedMS(started), "final_url": lastSnapshot.URL, "page_valid": false, "page_retry_count": 2, "restart_after_url": false}, marketEventFields(target.Market)), base))
	return rows
}

func invalidateUnexpectedCurrencyRows(rows []map[string]string, expected string) {
	expected = strings.ToUpper(strings.TrimSpace(expected))
	for _, row := range rows {
		currency := strings.ToUpper(strings.TrimSpace(row["货币"]))
		if currency == "" || currency == "N/A" || currency == expected {
			continue
		}
		for _, key := range []string{"货币", "外显价格", "实价", "促销标签"} {
			row[key] = ""
		}
		row["价格状态"] = "currency_mismatch"
		note := "非" + expected + "价格已拒绝"
		if existing := strings.TrimSpace(row["抓取备注"]); existing != "" {
			row["抓取备注"] = existing + "；" + note
		} else {
			row["抓取备注"] = note
		}
	}
}

func (s *tripScraper) scrapeOneAttempt(ctx context.Context, item ScrapeItem, target navigationTarget, base Event, attempt int) ([]map[string]string, pageSnapshot, bool, error) {
	s.emit(mergeEvent(Event{"event": "log", "stage": "page_open", "message": "opening URL", "attempt": attempt}, base))
	sessionResult, err := loadSession(ctx, s.page, s.request.BrowserOptions.SessionFile, target.NavigateURL)
	if err != nil {
		s.emit(mergeEvent(Event{"event": "warning", "stage": "session_load", "message": "session load failed", "error": err.Error(), "session_nav_skipped": true}, base))
	} else if sessionResult.Loaded {
		s.emit(mergeEvent(Event{"event": "log", "stage": "session_load", "message": "session cookies loaded", "session_cookie_count": sessionResult.CookieCount, "session_nav_skipped": true}, base))
	} else {
		s.emit(mergeEvent(Event{"event": "log", "stage": "session_load", "message": "session not loaded or unavailable", "session_nav_skipped": true}, base))
	}
	if err := applyUCStealth(ctx, s.page, target.Market.AcceptLanguage); err != nil && ctx.Err() != nil {
		return nil, pageSnapshot{}, false, err
	}
	if remove, err := s.page.EvalOnNewDocument(promotionContentCaptureJS); err == nil {
		defer remove()
	}
	monitor := s.startNavigationMonitor(ctx)
	if monitor != nil {
		defer monitor.close()
	}
	navCtx := ctx
	var navCancel context.CancelFunc
	if s.request.Runtime.NavigationTimeout > 0 {
		navCtx, navCancel = context.WithTimeout(ctx, s.request.Runtime.NavigationTimeout)
		defer navCancel()
	}
	if err := s.page.Context(navCtx).Navigate(target.NavigateURL); err != nil {
		return nil, pageSnapshot{}, false, err
	}
	_ = s.page.WaitLoad()
	_ = s.waitReady(ctx)
	navStatus, navURL, _ := monitor.snapshot()
	if sessionResult.HasStorage && s.shouldApplySessionStorage(target.NavigateURL) {
		if loaded, err := applySessionStorage(ctx, s.page, sessionResult.Data); err != nil {
			s.emit(mergeEvent(Event{"event": "warning", "stage": "session_load", "message": "session storage load failed", "error": err.Error(), "session_nav_skipped": true}, base))
		} else if loaded {
			s.emit(mergeEvent(Event{"event": "log", "stage": "session_load", "message": "session storage loaded", "session_nav_skipped": true}, base))
		}
	}
	if ctx.Err() != nil {
		return nil, pageSnapshot{}, false, ctx.Err()
	}
	snapshot, waitMS := s.waitProductPageReady(ctx, 12*time.Second)
	snapshot.withNavigationStatus(navStatus, navURL)
	if s.recordCloudflareIfPresent("page_open", base) {
		return []map[string]string{s.riskBlockedRow(item, "cloudflare overlay detected during navigation")}, snapshot, false, nil
	}
	if isWhaleguardSnapshot(snapshot) {
		s.emit(mergeEvent(Event{"event": "risk_challenge_detected", "stage": "page_open", "message": "whaleguard block detected during navigation", "risk_kind": snapshot.RiskKind, "http_status": snapshot.HTTPStatus, "body_preview": snapshot.BodyPreview, "final_url": snapshot.URL}, base))
		return nil, snapshot, true, nil
	}
	if !isProductPageReady(snapshot) {
		s.emit(mergeEvent(Event{"event": "page_unhealthy", "stage": "page_open", "message": "product DOM not ready after navigation", "wait_ms": waitMS, "snapshot": snapshot}, base))
		return nil, snapshot, true, nil
	}
	if !sleepDelay(ctx, s.emit, "page_open", "page", s.request.DelayOptions.Page) {
		return nil, snapshot, false, ctx.Err()
	}
	if s.recordCloudflareIfPresent("after_page_delay", base) {
		return []map[string]string{s.riskBlockedRow(item, "cloudflare overlay detected before extraction")}, snapshot, false, nil
	}
	risk := s.detectRiskChallenge()
	if len(risk.Scripts) > 0 {
		s.emit(mergeEvent(Event{"event": "log", "stage": "risk", "message": "risk scripts detected", "risk_scripts_detected": risk.Scripts}, base))
	}
	if risk.Challenge {
		snapshot = s.currentPageSnapshot()
		snapshot.applyRisk(risk)
		snapshot.withNavigationStatus(navStatus, navURL)
		s.emit(mergeEvent(Event{"event": "risk_challenge_detected", "stage": "risk", "message": risk.Reason, "risk_kind": risk.Kind, "http_status": snapshot.HTTPStatus, "body_preview": risk.BodyPreview, "risk_scripts_detected": risk.Scripts, "final_url": snapshot.URL}, base))
		if risk.Kind == "cloudflare" {
			s.markCloudflareBlocked("risk", base, risk.Reason, risk.BodyPreview)
			return []map[string]string{s.riskBlockedRow(item, risk.Reason)}, snapshot, false, nil
		} else {
			if risk.Kind == "whaleguard" {
				return nil, snapshot, true, nil
			}
			return []map[string]string{s.riskBlockedRow(item, risk.Reason)}, snapshot, false, nil
		}
	}
	s.emit(mergeEvent(Event{"event": "log", "stage": "lazy_load", "message": "scrolling page"}, base))
	s.scrollPage(ctx)
	if s.recordCloudflareIfPresent("after_scroll", base) {
		return []map[string]string{s.riskBlockedRow(item, "cloudflare overlay detected after scroll")}, snapshot, false, nil
	}
	snapshot, waitMS = s.waitProductPageExtractable(ctx, 12*time.Second, s.request.RatingOnly)
	snapshot.withNavigationStatus(navStatus, navURL)
	if s.recordCloudflareIfPresent("before_extract", base) {
		return []map[string]string{s.riskBlockedRow(item, "cloudflare overlay detected before extraction")}, snapshot, false, nil
	}
	if snapshot.RiskChallenge {
		risk := s.detectRiskChallenge()
		if risk.Challenge {
			reason := firstNonEmpty(risk.Reason, "risk challenge detected after lazy-load scrolling")
			snapshot.applyRisk(risk)
			snapshot.withNavigationStatus(navStatus, navURL)
			s.emit(mergeEvent(Event{"event": "risk_challenge_detected", "stage": "risk", "message": reason, "risk_kind": risk.Kind, "http_status": snapshot.HTTPStatus, "body_preview": risk.BodyPreview, "risk_scripts_detected": risk.Scripts, "final_url": snapshot.URL}, base))
			if risk.Kind == "cloudflare" {
				s.markCloudflareBlocked("lazy_load", base, reason, risk.BodyPreview)
				return []map[string]string{s.riskBlockedRow(item, reason)}, snapshot, false, nil
			} else {
				if risk.Kind == "whaleguard" {
					return nil, snapshot, true, nil
				}
				return []map[string]string{s.riskBlockedRow(item, reason)}, snapshot, false, nil
			}
		}
		snapshot.RiskChallenge = false
		snapshot.RiskKind = ""
	}
	if !isProductPageExtractable(snapshot, s.request.RatingOnly) {
		s.emit(mergeEvent(Event{"event": "page_unhealthy", "stage": "lazy_load", "message": "product DOM is not extractable after lazy-load scrolling", "wait_ms": waitMS, "snapshot": snapshot}, base))
		return nil, snapshot, true, nil
	}
	observation, observationErr := s.readMarketObservation()
	snapshot.Market = observation
	if observationErr != nil {
		snapshot.MarketContextClass = "market_context_unverified"
		observationErr = &marketContextError{Class: snapshot.MarketContextClass, Expected: target.Market, Observed: observation}
	} else if err := validateMarketObservation(target.Market, observation); err != nil {
		var contextErr *marketContextError
		if errors.As(err, &contextErr) {
			snapshot.Market = contextErr.Observed
			snapshot.MarketContextClass = contextErr.Class
			observationErr = contextErr
		} else {
			snapshot.MarketContextClass = "market_context_unverified"
			observationErr = &marketContextError{Class: snapshot.MarketContextClass, Expected: target.Market, Observed: observation}
		}
	}
	if observationErr != nil {
		contextErr, _ := observationErr.(*marketContextError)
		s.emit(mergeEvent(marketContextFailedEvent(target.Market, snapshot.Market, snapshot.MarketContextClass), base))
		if !s.request.RatingOnly || snapshot.MarketContextClass != "market_context_unverified" || !strings.EqualFold(snapshot.Market.Host, target.Market.Host) {
			return []map[string]string{s.marketContextBlockedRow(item, target.Market, contextErr)}, snapshot, false, nil
		}
	}
	s.emit(mergeEvent(Event{"event": "log", "stage": "extract", "message": "extracting page"}, base))
	rows, valid := s.extractRows(ctx, item, snapshot)
	if s.recordCloudflareIfPresent("after_extract", base) {
		s.restartAfterURL = true
		s.restartReason = "cloudflare overlay after extraction"
	}
	s.applyCurrentScrapeNoteToRows(rows)
	if !valid {
		snapshot = s.currentPageSnapshot()
		s.emit(mergeEvent(Event{"event": "page_unhealthy", "stage": "extract", "message": "product DOM unavailable during extraction", "snapshot": snapshot}, base))
		return nil, snapshot, true, nil
	}
	return rows, snapshot, false, nil
}

func (s *tripScraper) errorRows(item ScrapeItem, err error, started time.Time, base Event) []map[string]string {
	s.emit(mergeEvent(Event{"event": "url_failed", "error": err.Error(), "elapsed_ms": elapsedMS(started)}, base))
	return []map[string]string{s.errorRow(item, err.Error())}
}

func (s *tripScraper) marketContextBlockedRow(item ScrapeItem, expected MarketContext, contextErr *marketContextError) map[string]string {
	observed := marketObservation{}
	class := "market_context_unverified"
	if contextErr != nil {
		observed = contextErr.Observed
		class = contextErr.Class
	}
	row := s.errorRow(item, class)
	if s.request.RatingOnly {
		return row
	}
	row["价格状态"] = priceStatusUntrustedMarketContext
	if observed.Currency != "" && !strings.EqualFold(observed.Currency, expected.Currency) {
		row["价格状态"] = "currency_mismatch"
	}
	row["价格来源"] = ""
	row["原始捕获价格"] = ""
	for _, key := range []string{"货币", "外显价格", "实价", "促销标签", "资源ID", "接口ItemID", "促销命中"} {
		row[key] = ""
	}
	row["抓取备注"] = class
	return row
}

func marketContextFailedEvent(expected MarketContext, observed marketObservation, class string) Event {
	return Event{
		"event":                    "market_context_failed",
		"stage":                    "market_context",
		"message":                  "returned market context does not match the requested market",
		"market_context_class":     class,
		"expected_market_host":     expected.Host,
		"expected_market_site_id":  expected.SiteID,
		"expected_market_locale":   expected.Locale,
		"expected_market_currency": expected.Currency,
		"observed_market_host":     observed.Host,
		"observed_market_site_id":  observed.SiteID,
		"observed_market_locale":   observed.Locale,
		"observed_market_currency": observed.Currency,
	}
}

func countPriceQualityRows(rows []map[string]string) (trusted int, untrusted int, failed bool) {
	for _, row := range rows {
		status := strings.TrimSpace(row["价格状态"])
		switch {
		case isTrustedPriceStatus(status):
			trusted++
		case status != "":
			untrusted++
			failed = true
		}
	}
	return trusted, untrusted, failed
}

func (s *tripScraper) shouldRetryPriceQuality(quality *priceQualityTracker, trustedRows int, retriesUsed int) bool {
	if s == nil || s.request.RatingOnly || quality == nil || !quality.failed || trustedRows > 0 {
		return false
	}
	class := quality.failureClass
	if class == "" || class == FailureNone {
		class = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
		quality.failureClass = class
	}
	if class != FailureBusinessUnavailable && !FailureClassRetryableByQualityRerun(class) || !priceQualityReasonRetryable(quality.failReason) {
		return false
	}
	limit := s.request.PriceQualityRetry.MaxRetries
	if s.request.Runtime != (RuntimeOptions{}) && s.request.Runtime.MaxQualityRetriesPerURL >= 0 && s.request.Runtime.MaxQualityRetriesPerURL < limit {
		limit = s.request.Runtime.MaxQualityRetriesPerURL
	}
	if retriesUsed >= max(0, limit) {
		return false
	}
	return true
}

func priceQualityReasonRetryable(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "consecutive_resource_miss", "resource_miss_rate", "same_fallback_price_streak":
		return true
	default:
		return false
	}
}

func (s *tripScraper) extractRows(ctx context.Context, item ScrapeItem, snapshot pageSnapshot) ([]map[string]string, bool) {
	baseRow := s.extractBaseRow(ctx, item)
	baseRow["总促销标签"] = joinTotalPromoTags(s.totalPromoContents())
	s.applyCurrentScrapeNote(baseRow)
	if s.request.RatingOnly {
		s.emit(Event{"event": "log", "stage": "sku", "message": "rating-only mode; skipping SKU price extraction"})
		return []map[string]string{baseRow}, isProductPageExtractable(snapshot, true)
	}
	oldMax := s.maxSKULayers
	combinations, quality := s.extractAllSKUPrices(ctx)
	if s.maxSKULayers > oldMax {
		s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("SKU layers increased to %d", s.maxSKULayers)})
	}
	if len(combinations) == 0 {
		if quality != nil && quality.failed {
			quality.failureClass = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
			s.currentQualityFailure = quality
			row := s.errorRow(item, "价格质量检查失败: "+quality.failReason)
			row["价格状态"] = priceStatusUntrustedResourceMiss
			row["抓取备注"] = "价格质量检查失败: " + quality.failReason
			return []map[string]string{row}, true
		}
		if !allowNoSKUFallback(baseRow, snapshot) {
			return nil, false
		}
		currency, price := parseDisplayPrice(s.currentPrice(ctx, 3), s.request.Market)
		row := cloneRow(baseRow)
		row["货币"] = currency
		row["外显价格"] = price
		row["促销标签"] = s.promoText()
		if s.request.ExtractRealPrice {
			row["实价"] = price
		}
		row["价格状态"] = priceStatusTrustedBase
		layerCount := max(s.maxSKULayers, 1)
		for i := 0; i < layerCount; i++ {
			if i == 0 {
				row["SKU层级1"] = "无SKU选项"
			} else {
				row[fmt.Sprintf("SKU层级%d", i+1)] = ""
			}
		}
		s.ensurePriceFields(row)
		return []map[string]string{row}, true
	}
	rows := make([]map[string]string, 0, len(combinations))
	for index, combo := range combinations {
		row := cloneRow(baseRow)
		if index > 0 {
			clearRepeatedProductFields(row, s.request.ExtractProductInfo)
		}
		row["货币"] = combo["currency"]
		row["外显价格"] = combo["price"]
		if s.request.ExtractRealPrice {
			row["实价"] = combo["real_price"]
		}
		row["促销标签"] = combo["promo_tag"]
		copyPriceQualityFields(row, combo)
		parts := []string{}
		if combo["combination"] != "" {
			parts = strings.Split(combo["combination"], " | ")
		}
		layerCount := max(s.maxSKULayers, len(parts), 1)
		for i := 0; i < layerCount; i++ {
			if i < len(parts) {
				row[fmt.Sprintf("SKU层级%d", i+1)] = parts[i]
			} else {
				row[fmt.Sprintf("SKU层级%d", i+1)] = ""
			}
		}
		s.ensurePriceFields(row)
		rows = append(rows, row)
	}
	if quality != nil && quality.failed {
		quality.failureClass = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
		s.currentQualityFailure = quality
		if FailureClassAllowsBrowserRestart(quality.failureClass) {
			s.restartAfterURL = true
			s.restartReason = "price quality failed: " + quality.failReason
		}
	}
	return rows, true
}

func copyPriceQualityFields(row map[string]string, combo map[string]string) {
	if row == nil || combo == nil {
		return
	}
	fieldMap := map[string]string{
		"price_status":       "价格状态",
		"resource_id":        "资源ID",
		"item_id":            "接口ItemID",
		"promo_matched":      "促销命中",
		"price_source":       "价格来源",
		"raw_captured_price": "原始捕获价格",
	}
	for src, dst := range fieldMap {
		if value := strings.TrimSpace(combo[src]); value != "" {
			row[dst] = value
		}
	}
	if note := strings.TrimSpace(combo["scrape_note"]); note != "" {
		if existing := strings.TrimSpace(row["抓取备注"]); existing != "" {
			if !strings.Contains(existing, note) {
				row["抓取备注"] = existing + "；" + note
			}
		} else {
			row["抓取备注"] = note
		}
	}
}

func (s *tripScraper) emitURLQualityFailed(item ScrapeItem, quality *priceQualityTracker) {
	if quality == nil || !quality.failed {
		return
	}
	s.refreshPriceQualityDiagnostics(quality)
	failureClass := quality.failureClass
	if failureClass == "" || failureClass == FailureNone {
		failureClass = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
		quality.failureClass = failureClass
	}
	diagnosticPath := s.writePriceQualityDiagnostic(item, quality)
	s.emit(Event{
		"event":                "url_quality_failed",
		"stage":                "sku",
		"vendor":               item.Vendor,
		"url":                  item.URL,
		"product_id":           productID(item.URL),
		"reason":               quality.failReason,
		"failure_class":        failureClass,
		"trusted_rows":         quality.trustedRows,
		"untrusted_rows":       quality.untrustedRows,
		"resource_miss_rows":   quality.resourceMissRows,
		"total_rows":           quality.totalRows,
		"resource_miss_rate":   quality.resourceMissRate(),
		"top_fallback_prices":  quality.topFallbackPrices(3),
		"selected_texts":       quality.selectedTexts(5),
		"retry_attempt":        quality.retryAttempt,
		"recovered":            quality.recovered,
		"next_data_present":    quality.diagnostics.NextDataPresent,
		"package_count":        quality.diagnostics.PackageCount,
		"resource_count":       quality.diagnostics.ResourceCount,
		"batch_response_count": quality.diagnostics.BatchResponseCount,
		"last_response_status": quality.diagnostics.LastResponseStatus,
		"diagnostic_path":      diagnosticPath,
	})
}

func (s *tripScraper) emitPriceQualityRecovered(item ScrapeItem, quality *priceQualityTracker, retryAttempt int, trustedRows int, untrustedRows int, base Event) {
	if quality == nil {
		return
	}
	s.refreshPriceQualityDiagnostics(quality)
	s.emit(mergeEvent(Event{
		"event":                "price_quality_recovered",
		"stage":                "sku",
		"message":              "price quality recovered after retry",
		"vendor":               item.Vendor,
		"url":                  item.URL,
		"product_id":           productID(item.URL),
		"reason":               quality.failReason,
		"retry_attempt":        retryAttempt,
		"trusted_rows":         trustedRows,
		"untrusted_rows":       untrustedRows,
		"top_fallback_prices":  quality.topFallbackPrices(3),
		"selected_texts":       quality.selectedTexts(5),
		"next_data_present":    quality.diagnostics.NextDataPresent,
		"package_count":        quality.diagnostics.PackageCount,
		"resource_count":       quality.diagnostics.ResourceCount,
		"batch_response_count": quality.diagnostics.BatchResponseCount,
		"last_response_status": quality.diagnostics.LastResponseStatus,
	}, base))
}

func (s *tripScraper) refreshPriceQualityDiagnostics(quality *priceQualityTracker) {
	if s == nil || quality == nil || s.page == nil {
		return
	}
	latest := s.productPackageDiagnostics()
	if latest.NextDataPresent || latest.PackageCount > 0 || latest.ResourceCount > 0 || latest.BatchResponseCount > 0 || latest.LastResponseStatus > 0 {
		quality.diagnostics = latest
	}
}

func (s *tripScraper) writePriceQualityDiagnostic(item ScrapeItem, quality *priceQualityTracker) string {
	if quality == nil {
		return ""
	}
	dir := expandPath(s.request.OutputDir)
	if dir == "" && strings.TrimSpace(s.request.OutputPath) != "" {
		dir = filepath.Dir(expandPath(s.request.OutputPath))
	}
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, "_diagnostics", "price_quality_failures.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	payload := map[string]any{
		"event":                "price_quality_failure",
		"vendor":               item.Vendor,
		"url":                  item.URL,
		"product_id":           productID(item.URL),
		"reason":               quality.failReason,
		"trusted_rows":         quality.trustedRows,
		"untrusted_rows":       quality.untrustedRows,
		"resource_miss_rows":   quality.resourceMissRows,
		"total_rows":           quality.totalRows,
		"resource_miss_rate":   quality.resourceMissRate(),
		"top_fallback_prices":  quality.topFallbackPrices(5),
		"selected_texts":       quality.selectedTexts(5),
		"retry_attempt":        quality.retryAttempt,
		"recovered":            quality.recovered,
		"next_data_present":    quality.diagnostics.NextDataPresent,
		"package_count":        quality.diagnostics.PackageCount,
		"resource_count":       quality.diagnostics.ResourceCount,
		"batch_response_count": quality.diagnostics.BatchResponseCount,
		"last_response_status": quality.diagnostics.LastResponseStatus,
		"examples":             quality.examples,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return ""
	}
	return absPath(path)
}

func (s *tripScraper) extractBaseRow(ctx context.Context, item ScrapeItem) map[string]string {
	text := s.pageText()
	bundle, waitMS := s.waitBaseRatingBundleStable(ctx, 2500*time.Millisecond)
	if waitMS > 1000 {
		s.emit(Event{"event": "log", "stage": "base_fields", "message": fmt.Sprintf("base fields settled slowly wait_ms=%d score=%q customer=%q convenience=%q signal=%q bad_reviews=%q", waitMS, bundle.Score, bundle.Customer, bundle.Convenience, bundle.Signal, bundle.BadReviewCount)})
	}
	if bundle.Score == "" || (bundle.Score != "" && (bundle.Customer == "" || bundle.Convenience == "" || bundle.Signal == "")) {
		s.emit(Event{"event": "log", "stage": "base_fields", "message": fmt.Sprintf("base fields incomplete score=%q customer=%q convenience=%q signal=%q bad_reviews=%q", bundle.Score, bundle.Customer, bundle.Convenience, bundle.Signal, bundle.BadReviewCount)})
	}
	if bundle.Score == "" {
		bundle.Score = s.firstText([]string{`span[testid="productdetail-productInfo-commentScore"]`})
	}
	title := firstNonEmpty(s.firstText([]string{"h1", `[data-testid="product-title"]`, `[class*="title"]`}), s.titleFromDocument())
	row := baseRowFromBundle(item, title, text, bundle, s.request.ExtractProductInfo)
	applyProductSalesVolumeFields(row, s.productSalesVolume(productID(item.URL)))
	applyCapturedAPIFields(row, s.capturedAPIFields(), s.request.ExtractProductInfo)
	return row
}

func (s *tripScraper) productSalesVolume(expectedProductID string) productSalesVolume {
	var volume productSalesVolume
	if expectedProductID == "" {
		return volume
	}
	_ = s.evalJSON(&volume, productSalesVolumeJS, expectedProductID)
	return volume
}

func applyProductSalesVolumeFields(row map[string]string, volume productSalesVolume) {
	if row == nil {
		return
	}
	if volume.Monthly > 0 {
		row["月销量"] = strconv.FormatInt(volume.Monthly, 10)
	}
	if volume.Yearly > 0 {
		row["年销量"] = strconv.FormatInt(volume.Yearly, 10)
	}
	if volume.Total > 0 {
		row["总销量"] = strconv.FormatInt(volume.Total, 10)
	}
}

func (s *tripScraper) capturedAPIFields() capturedAPIFields {
	return parseCapturedAPIFields(s.capturedAPIResponses())
}

func (s *tripScraper) capturedAPIResponses() []capturedAPIResponse {
	if s == nil || s.page == nil {
		return nil
	}
	var responses []capturedAPIResponse
	_ = s.evalJSON(&responses, `() => (window.__tripCdpProductPromotionResponses || []).slice()`)
	return responses
}

func parseCapturedAPIFields(responses []capturedAPIResponse) capturedAPIFields {
	var fields capturedAPIFields
	for _, response := range responses {
		if response.Status < 200 || response.Status >= 300 || strings.TrimSpace(response.Text) == "" {
			continue
		}
		if strings.Contains(response.URL, "/restapi/soa2/21052/getProductInfo") {
			var payload struct {
				ProductInfos []struct {
					BasicInfo struct {
						ProductName string `json:"productName"`
					} `json:"basicInfo"`
					AddInfos []struct {
						Code     string `json:"code"`
						Contents []struct {
							Value string `json:"value"`
						} `json:"contents"`
					} `json:"addInfos"`
				} `json:"productInfos"`
			}
			if json.Unmarshal([]byte(response.Text), &payload) != nil || len(payload.ProductInfos) == 0 {
				continue
			}
			product := payload.ProductInfos[0]
			fields.ProductName = firstNonEmpty(fields.ProductName, strings.TrimSpace(product.BasicInfo.ProductName))
			for _, info := range product.AddInfos {
				if info.Code != "ProductIntroduction" {
					continue
				}
				parts := make([]string, 0, len(info.Contents))
				for _, content := range info.Contents {
					if text := compactHTMLText(content.Value); text != "" {
						parts = append(parts, text)
					}
				}
				fields.ProductInfo = firstNonEmpty(fields.ProductInfo, strings.Join(parts, "\n"))
			}
		}
		if strings.Contains(response.URL, "/restapi/soa2/18084/commonQueryCommentSummary") {
			var payload struct {
				CommentAggregation struct {
					TotalCount int     `json:"totalCount"`
					ScoreAvg   float64 `json:"scoreAvg"`
					CommonTags []struct {
						TagName    string `json:"tagName"`
						TotalCount int    `json:"totalCount"`
					} `json:"commonTags"`
				} `json:"commentAggregation"`
			}
			if json.Unmarshal([]byte(response.Text), &payload) != nil {
				continue
			}
			aggregation := payload.CommentAggregation
			if aggregation.TotalCount > 0 {
				fields.ReviewCount = strconv.Itoa(aggregation.TotalCount)
			}
			if aggregation.ScoreAvg > 0 {
				fields.Score = formatPriceNumber(aggregation.ScoreAvg)
			}
			for _, tag := range aggregation.CommonTags {
				if tag.TagName == "MIDBAD" && tag.TotalCount >= 0 {
					fields.BadReviewCount = strconv.Itoa(tag.TotalCount)
				}
			}
		}
		if strings.Contains(response.URL, "/restapi/soa2/20047/listComments") {
			var payload struct {
				CommentAggregation struct {
					GoodRate    float64 `json:"goodRate"`
					SubItemTags []struct {
						TagName  string  `json:"tagName"`
						ScoreAvg float64 `json:"scoreAvg"`
					} `json:"subItemTags"`
				} `json:"commentAggregation"`
			}
			if json.Unmarshal([]byte(response.Text), &payload) != nil {
				continue
			}
			if payload.CommentAggregation.GoodRate > 0 {
				fields.GoodRate = fmt.Sprintf("%.1f%%", payload.CommentAggregation.GoodRate*100)
			}
			for _, tag := range payload.CommentAggregation.SubItemTags {
				if tag.ScoreAvg <= 0 {
					continue
				}
				score := formatPriceNumber(tag.ScoreAvg)
				switch tag.TagName {
				case "42":
					fields.Customer = score
				case "43":
					fields.Convenience = score
				case "44":
					fields.Signal = score
				}
			}
		}
	}
	return fields
}

func compactHTMLText(value string) string {
	value = regexp.MustCompile(`(?i)<br\s*/?>|</p\s*>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, "")
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}

func applyCapturedAPIFields(row map[string]string, fields capturedAPIFields, includeProductInfo bool) {
	if row == nil {
		return
	}
	for key, value := range map[string]string{
		"商品名称":  fields.ProductName,
		"评价数":   fields.ReviewCount,
		"评分":    fields.Score,
		"好评率":   fields.GoodRate,
		"客户服务":  fields.Customer,
		"方便程度":  fields.Convenience,
		"讯号状态":  fields.Signal,
		"中差评价数": fields.BadReviewCount,
	} {
		if value = strings.TrimSpace(value); value != "" {
			row[key] = value
		}
	}
	if includeProductInfo && strings.TrimSpace(fields.ProductInfo) != "" {
		row["商品资讯"] = fields.ProductInfo
	}
}

func baseRowFromBundle(item ScrapeItem, title string, pageText string, bundle baseRatingBundle, extractProductInfo bool) map[string]string {
	bundle = normalizeBaseRatingBundle(bundle)
	score := bundle.Score
	customer := ""
	convenience := ""
	signal := ""
	badReviews := ""
	customer = bundle.Customer
	convenience = bundle.Convenience
	signal = bundle.Signal
	badReviews = bundle.BadReviewCount
	row := map[string]string{
		"厂商":    item.Vendor,
		"商品ID":  productID(item.URL),
		"URL":   item.URL,
		"商品名称":  title,
		"预定数":   bundle.BookingCount,
		"月销量":   "",
		"年销量":   "",
		"总销量":   "",
		"评价数":   bundle.ReviewCount,
		"评分":    score,
		"客户服务":  customer,
		"方便程度":  convenience,
		"讯号状态":  signal,
		"中差评价数": badReviews,
	}
	if extractProductInfo {
		row["商品资讯"] = firstNonEmpty(bundle.ProductInfo, productInfo(pageText))
	}
	return row
}

func (s *tripScraper) extractAllSKUPrices(ctx context.Context) ([]map[string]string, *priceQualityTracker) {
	s.emit(Event{"event": "log", "stage": "sku", "message": "starting SKU option and price extraction"})
	if rows, coverage, waitMS := s.waitCapturedAPISKUPrices(ctx, 3*time.Second); coverage.usable() {
		for _, row := range rows {
			if combination := strings.TrimSpace(row["combination"]); combination != "" {
				s.maxSKULayers = max(s.maxSKULayers, len(strings.Split(combination, " | ")))
			}
		}
		s.emit(Event{
			"event":               "sku_api_batch_used",
			"stage":               "sku",
			"message":             "using captured product price calendar batch",
			"price_source":        priceSourceProductPriceCalendar,
			"tree_count":          coverage.TreeCount,
			"priced_count":        coverage.PricedCount,
			"missing_price_count": coverage.MissingPriceCount,
			"coverage_rate":       float64(coverage.PricedCount) / float64(coverage.TreeCount),
			"wait_ms":             waitMS,
		})
		return rows, nil
	} else if coverage.TreeCount > 0 || coverage.PricedCount > 0 {
		s.emit(Event{
			"event":               "sku_api_batch_fallback",
			"stage":               "sku",
			"message":             "captured API batch coverage is below the trusted threshold; using browser traversal",
			"tree_count":          coverage.TreeCount,
			"priced_count":        coverage.PricedCount,
			"missing_price_count": coverage.MissingPriceCount,
			"wait_ms":             waitMS,
		})
	}
	count, waitMS := s.waitSKULayerCount(ctx, skuLayerWaitMax)
	if count == 0 {
		s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("SKU container not found after wait; product may have no SKU options wait_ms=%d", waitMS)})
		return nil, nil
	}
	if waitMS > 1000 {
		s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("SKU container appeared after wait_ms=%d", waitMS)})
	}
	s.maxSKULayers = max(s.maxSKULayers, count)
	s.enableBatchPromotionCapture()
	packageDiagnostics, packageWaitMS := s.waitProductPackageDiagnostics(ctx, 5*time.Second)
	if packageDiagnostics.ready() {
		if packageWaitMS > 1000 {
			s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("product package data ready after wait_ms=%d", packageWaitMS), "package_count": packageDiagnostics.PackageCount, "resource_count": packageDiagnostics.ResourceCount, "batch_response_count": packageDiagnostics.BatchResponseCount})
		}
	} else {
		s.emit(Event{"event": "product_package_not_ready", "stage": "sku", "message": "product package/resource data not ready before SKU traversal", "wait_ms": packageWaitMS, "next_data_present": packageDiagnostics.NextDataPresent, "package_count": packageDiagnostics.PackageCount, "resource_count": packageDiagnostics.ResourceCount, "batch_response_count": packageDiagnostics.BatchResponseCount, "last_response_status": packageDiagnostics.LastResponseStatus})
	}
	s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("found %d SKU layers", count)})
	layers := make([]skuLayerInfo, 0, count)
	for i := 0; i < count; i++ {
		title := s.layerTitle(i)
		layers = append(layers, skuLayerInfo{Index: i, Title: title})
		s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d: %s", i+1, title)})
	}
	quality := newPriceQualityTracker()
	quality.retryAttempt = s.currentQualityRetryAttempt
	quality.diagnostics = packageDiagnostics
	results := traverseSKUCombinationsWithQuality(ctx, layers, func(index int) []skuOption {
		options, waitMS := s.waitLayerOptions(ctx, index, skuOptionsWaitMax)
		if waitMS > 1000 && len(options) > 0 {
			s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d options appeared after wait_ms=%d", index+1, waitMS)})
		}
		return options
	}, func(option skuOption, deselect bool) bool {
		return s.clickSKUOption(ctx, option, deselect)
	}, func(selected []skuOption) map[string]string {
		return s.currentCombinationPrice(ctx, selected)
	}, quality, s.emit)
	s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("recursive SKU traversal complete; captured %d trusted combinations", len(results))})
	return results, quality
}

func (s *tripScraper) waitSKULayerCount(ctx context.Context, maxWait time.Duration) (int, int) {
	return waitForSKULayerCount(ctx, s.skuLayerCount, maxWait, skuLayerWaitPoll)
}

func waitForSKULayerCount(ctx context.Context, countLayers func() int, maxWait time.Duration, pollInterval time.Duration) (int, int) {
	started := time.Now()
	deadline := started.Add(maxWait)
	for {
		if ctx.Err() != nil {
			return 0, elapsedMS(started)
		}
		count := countLayers()
		if count > 0 {
			return count, elapsedMS(started)
		}
		if time.Now().After(deadline) {
			return 0, elapsedMS(started)
		}
		select {
		case <-ctx.Done():
			return 0, elapsedMS(started)
		case <-time.After(pollInterval):
		}
	}
}

func (s *tripScraper) waitLayerOptions(ctx context.Context, index int, maxWait time.Duration) ([]skuOption, int) {
	return waitForLayerOptions(ctx, s.layerOptions, index, maxWait, skuOptionsWaitPoll)
}

func waitForLayerOptions(ctx context.Context, getOptions func(int) []skuOption, index int, maxWait time.Duration, pollInterval time.Duration) ([]skuOption, int) {
	started := time.Now()
	deadline := started.Add(maxWait)
	for {
		if ctx.Err() != nil {
			return nil, elapsedMS(started)
		}
		options := getOptions(index)
		if len(options) > 0 {
			return options, elapsedMS(started)
		}
		if time.Now().After(deadline) {
			return nil, elapsedMS(started)
		}
		select {
		case <-ctx.Done():
			return nil, elapsedMS(started)
		case <-time.After(pollInterval):
		}
	}
}

func (s *tripScraper) waitProductPackageDiagnostics(ctx context.Context, maxWait time.Duration) (productPackageDiagnostics, int) {
	started := time.Now()
	deadline := started.Add(maxWait)
	var latest productPackageDiagnostics
	for {
		if ctx.Err() != nil {
			return latest, elapsedMS(started)
		}
		latest = s.productPackageDiagnostics()
		if latest.ready() || time.Now().After(deadline) {
			return latest, elapsedMS(started)
		}
		if !sleepFixed(ctx, 250*time.Millisecond) {
			return latest, elapsedMS(started)
		}
	}
}

func (s *tripScraper) productPackageDiagnostics() productPackageDiagnostics {
	var diagnostics productPackageDiagnostics
	_ = s.evalJSON(&diagnostics, `() => {
		const out = {
			next_data_present: false,
			package_count: 0,
			resource_count: 0,
			batch_response_count: 0,
			last_response_status: 0
		};
		const nextScript = document.querySelector('script#__NEXT_DATA__');
		if (nextScript && nextScript.textContent) {
			out.next_data_present = true;
			try {
				const data = JSON.parse(nextScript.textContent || '{}');
				const product = data && data.props && data.props.pageProps &&
					data.props.pageProps.initialState &&
					data.props.pageProps.initialState.__STATE_MAP__ &&
					data.props.pageProps.initialState.__STATE_MAP__.PageModel &&
					data.props.pageProps.initialState.__STATE_MAP__.PageModel.ds &&
					data.props.pageProps.initialState.__STATE_MAP__.PageModel.ds.getProductInfo &&
					data.props.pageProps.initialState.__STATE_MAP__.PageModel.ds.getProductInfo[0];
				const packages = (product && (product.packageInfos || (product.basicInfo && product.basicInfo.packages))) || [];
				const resources = (product && product.resourceInfos) || [];
				out.package_count = Array.isArray(packages) ? packages.length : 0;
				out.resource_count = Array.isArray(resources) ? resources.length : 0;
			} catch (e) {}
		}
		const responses = window.__tripCdpBatchPromotionResponses || [];
		out.batch_response_count = Array.isArray(responses) ? responses.length : 0;
		if (out.batch_response_count > 0) {
			const last = responses[out.batch_response_count - 1] || {};
			out.last_response_status = Number(last.status || 0);
		}
		return out;
	}`)
	return diagnostics
}

type priceQualityDecision struct {
	Stop   bool
	Reason string
}

type priceQualityTracker struct {
	totalRows               int
	trustedRows             int
	untrustedRows           int
	resourceMissRows        int
	consecutiveResourceMiss int
	lastFallbackPrice       string
	fallbackPriceStreak     int
	fallbackCounts          map[string]int
	examples                []map[string]string
	failed                  bool
	failReason              string
	retryAttempt            int
	recovered               bool
	diagnostics             productPackageDiagnostics
	failureClass            FailureClass
}

func newPriceQualityTracker() *priceQualityTracker {
	return &priceQualityTracker{fallbackCounts: map[string]int{}}
}

func (t *priceQualityTracker) observe(row map[string]string) priceQualityDecision {
	if t == nil || row == nil {
		return priceQualityDecision{}
	}
	if t.failed {
		return priceQualityDecision{Stop: true, Reason: t.failReason}
	}
	t.totalRows++
	status := strings.TrimSpace(row["price_status"])
	switch status {
	case priceStatusTrustedPromo, priceStatusTrustedBase:
		t.trustedRows++
		t.consecutiveResourceMiss = 0
		t.lastFallbackPrice = ""
		t.fallbackPriceStreak = 0
	case priceStatusUntrustedResourceMiss:
		t.untrustedRows++
		t.resourceMissRows++
		t.consecutiveResourceMiss++
		t.addExample(row)
		rawPrice := strings.TrimSpace(row["raw_captured_price"])
		if rawPrice != "" {
			t.fallbackCounts[rawPrice]++
			if rawPrice == t.lastFallbackPrice {
				t.fallbackPriceStreak++
			} else {
				t.lastFallbackPrice = rawPrice
				t.fallbackPriceStreak = 1
			}
		} else {
			t.lastFallbackPrice = ""
			t.fallbackPriceStreak = 0
		}
	case priceStatusUntrustedPriceMissing, priceStatusRiskBlocked, priceStatusPartialUnhealthy:
		t.untrustedRows++
		t.addExample(row)
		t.consecutiveResourceMiss = 0
		t.lastFallbackPrice = ""
		t.fallbackPriceStreak = 0
	default:
		if strings.TrimSpace(row["price"]) != "" || strings.TrimSpace(row["real_price"]) != "" {
			t.trustedRows++
		}
		t.consecutiveResourceMiss = 0
	}
	if t.consecutiveResourceMiss >= maxConsecutiveResourceMiss {
		return t.stop("consecutive_resource_miss")
	}
	if t.totalRows >= minSamplesForResourceRate && float64(t.resourceMissRows)/float64(t.totalRows) >= resourceMissRateLimit {
		return t.stop("resource_miss_rate")
	}
	if t.fallbackPriceStreak >= sameFallbackPriceStreak {
		return t.stop("same_fallback_price_streak")
	}
	return priceQualityDecision{}
}

func (t *priceQualityTracker) addExample(row map[string]string) {
	if len(t.examples) >= 5 {
		return
	}
	example := map[string]string{}
	for _, key := range []string{"combination", "selected_texts", "price_status", "resource_id", "item_id", "promo_matched", "raw_captured_price", "response_status", "response_url", "response_preview", "scrape_note"} {
		if value := strings.TrimSpace(row[key]); value != "" {
			example[key] = value
		}
	}
	t.examples = append(t.examples, example)
}

func (t *priceQualityTracker) stop(reason string) priceQualityDecision {
	t.failed = true
	t.failReason = reason
	return priceQualityDecision{Stop: true, Reason: reason}
}

func (t *priceQualityTracker) resourceMissRate() float64 {
	if t == nil || t.totalRows == 0 {
		return 0
	}
	return float64(t.resourceMissRows) / float64(t.totalRows)
}

func (t *priceQualityTracker) selectedTexts(limit int) []string {
	if t == nil || limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, example := range t.examples {
		value := strings.TrimSpace(firstNonEmpty(example["selected_texts"], example["combination"]))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (t *priceQualityTracker) topFallbackPrices(limit int) []string {
	if t == nil || limit <= 0 || len(t.fallbackCounts) == 0 {
		return nil
	}
	type entry struct {
		price string
		count int
	}
	entries := make([]entry, 0, len(t.fallbackCounts))
	for price, count := range t.fallbackCounts {
		entries = append(entries, entry{price: price, count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count == entries[j].count {
			return entries[i].price < entries[j].price
		}
		return entries[i].count > entries[j].count
	})
	out := make([]string, 0, min(limit, len(entries)))
	for i := 0; i < len(entries) && i < limit; i++ {
		out = append(out, fmt.Sprintf("%s:%d", entries[i].price, entries[i].count))
	}
	return out
}

func traverseSKUCombinations(ctx context.Context, layers []skuLayerInfo, getOptions func(int) []skuOption, click func(skuOption, bool) bool, currentPrice func([]skuOption) map[string]string, emit Reporter) []map[string]string {
	return traverseSKUCombinationsWithQuality(ctx, layers, getOptions, click, currentPrice, nil, emit)
}

func traverseSKUCombinationsWithQuality(ctx context.Context, layers []skuLayerInfo, getOptions func(int) []skuOption, click func(skuOption, bool) bool, currentPrice func([]skuOption) map[string]string, quality *priceQualityTracker, emit Reporter) []map[string]string {
	results := []map[string]string{}
	stopTraversal := false
	var walk func(int, []skuOption)
	walk = func(layerIndex int, selected []skuOption) {
		if ctx.Err() != nil || stopTraversal {
			return
		}
		if layerIndex >= len(layers) {
			if result := currentPrice(selected); result != nil {
				if quality != nil {
					decision := quality.observe(result)
					if isTrustedPriceStatus(result["price_status"]) || result["price_status"] == "" {
						results = append(results, result)
					}
					if decision.Stop {
						stopTraversal = true
					}
				} else {
					results = append(results, result)
				}
			}
			return
		}
		layer := layers[layerIndex]
		options := getOptions(layer.Index)
		if len(options) == 0 {
			emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d %s has no available options; skipping", layerIndex+1, layer.Title)})
			return
		}
		emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("traversing layer %d %s: %d options", layerIndex+1, layer.Title, len(options))})
		for _, option := range options {
			if ctx.Err() != nil || stopTraversal {
				return
			}
			if !click(option, false) {
				continue
			}
			walk(layerIndex+1, append(selected, option))
			_ = click(option, true)
			if stopTraversal {
				return
			}
		}
	}
	walk(0, nil)
	return results
}

func (s *tripScraper) currentCombinationPrice(ctx context.Context, selected []skuOption) map[string]string {
	if len(selected) == 0 {
		return nil
	}
	if !sleepDelay(ctx, s.emit, "sku", "sku_price", s.request.DelayOptions.SKUPrice) {
		return nil
	}
	batchBundle, waitMS := s.waitBatchPromotionPriceBundle(ctx, selected, 5*time.Second)
	if ctx.Err() != nil {
		return nil
	}
	result := currentCombinationPriceRowFromBatchPromotion(selected, batchBundle, s.request.ExtractRealPrice, s.request.Market.Currency)
	if isTrustedPriceStatus(result["price_status"]) {
		if waitMS > 1000 {
			s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("batch promotion price settled slowly: %s wait_ms=%d promo_matched=%v", result["combination"], waitMS, batchBundle.PromoMatched)})
		}
		s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("combination price: %s = display:%s%s, real:%s%s, promo:%s, price_source=batchSearchBookingSalesPromotion, resourceId=%d, itemId=%d, promo_matched=%v", result["combination"], result["currency"], result["price"], result["real_currency"], result["real_price"], result["promo_tag"], batchBundle.ResourceID, batchBundle.ItemID, batchBundle.PromoMatched)})
		if !sleepDelay(ctx, s.emit, "sku", "sku_between", s.request.DelayOptions.SKUBetween) {
			return nil
		}
		return result
	}
	s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("combination price untrusted: %s, status=%s, raw=%s, price_source=batchSearchBookingSalesPromotion, resourceId=%d, itemId=%d, promo_matched=%v", result["combination"], result["price_status"], result["raw_captured_price"], batchBundle.ResourceID, batchBundle.ItemID, batchBundle.PromoMatched)})
	return result
}

func (s *tripScraper) enableBatchPromotionCapture() {
	_ = s.evalJSON(&struct{}{}, `() => {
		window.__tripCdpBatchPromotionResponses = [];
		if (window.__tripCdpBatchPromotionHooked) return {};
		window.__tripCdpBatchPromotionHooked = true;
		const keep = (url) => String(url || '').includes('/restapi/soa2/31644/batchSearchBookingSalesPromotion');
		const push = (kind, url, status, text) => {
			try {
				window.__tripCdpBatchPromotionResponses.push({
					kind,
					url: String(url || ''),
					status: Number(status || 0),
					text: String(text || ''),
					ts: Date.now()
				});
				window.__tripCdpBatchPromotionResponses = window.__tripCdpBatchPromotionResponses.slice(-80);
			} catch (e) {}
		};
		const originalFetch = window.fetch;
		if (typeof originalFetch === 'function') {
			window.fetch = async function(input, init) {
				const response = await originalFetch.apply(this, arguments);
				const url = typeof input === 'string' ? input : (input && input.url) || response.url;
				if (keep(url)) {
					response.clone().text().then((text) => push('fetch', url, response.status, text)).catch(() => push('fetch', url, response.status, ''));
				}
				return response;
			};
		}
		const OriginalXHR = window.XMLHttpRequest;
		if (OriginalXHR && OriginalXHR.prototype) {
			const originalOpen = OriginalXHR.prototype.open;
			const originalSend = OriginalXHR.prototype.send;
			OriginalXHR.prototype.open = function(method, url) {
				this.__tripCdpPromotionURL = url;
				return originalOpen.apply(this, arguments);
			};
			OriginalXHR.prototype.send = function() {
				this.addEventListener('load', function() {
					const url = this.__tripCdpPromotionURL || '';
					if (keep(url)) push('xhr', url, this.status, this.responseText || '');
				});
				return originalSend.apply(this, arguments);
			};
		}
		return {};
	}`)
}

func (s *tripScraper) clearBatchPromotionResponses() {
	_ = s.evalJSON(&struct{}{}, `() => {
		window.__tripCdpBatchPromotionResponses = [];
		return {};
	}`)
}

func (s *tripScraper) waitBatchPromotionPriceBundle(ctx context.Context, selected []skuOption, maxWait time.Duration) (batchPromotionPriceBundle, int) {
	return waitForBatchPromotionPriceBundle(ctx, selected, s.currentBatchPromotionPriceBundle, batchPromotionWaitOptions{
		MaxWait:             maxWait,
		PollInterval:        150 * time.Millisecond,
		NoPromoFallbackWait: 2500 * time.Millisecond,
		FastNoPromoReturn:   true,
	})
}

type batchPromotionWaitOptions struct {
	MaxWait             time.Duration
	PollInterval        time.Duration
	NoPromoFallbackWait time.Duration
	FastNoPromoReturn   bool
	Now                 func() time.Time
	Sleep               func(context.Context, time.Duration) bool
}

func batchPromotionBundleHasCompleteBasePrice(bundle batchPromotionPriceBundle) bool {
	return bundle.ResourceID > 0 && bundle.OriginalPrice > 0
}

func waitForBatchPromotionPriceBundle(ctx context.Context, selected []skuOption, current func([]string) batchPromotionPriceBundle, options batchPromotionWaitOptions) (batchPromotionPriceBundle, int) {
	nowFn := options.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	sleepFn := options.Sleep
	if sleepFn == nil {
		sleepFn = sleepFixed
	}
	maxWait := options.MaxWait
	if maxWait <= 0 {
		maxWait = 3 * time.Second
	}
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 150 * time.Millisecond
	}
	noPromoFallbackWait := options.NoPromoFallbackWait
	if noPromoFallbackWait <= 0 {
		noPromoFallbackWait = 2500 * time.Millisecond
	}

	started := nowFn()
	deadline := started.Add(maxWait)
	fallbackDeadline := started.Add(noPromoFallbackWait)
	var latest batchPromotionPriceBundle
	selectedTexts := make([]string, 0, len(selected))
	for _, option := range selected {
		selectedTexts = append(selectedTexts, option.Text)
	}
	for {
		if ctx.Err() != nil {
			return latest, int(nowFn().Sub(started).Milliseconds())
		}
		latest = current(selectedTexts)
		now := nowFn()
		if batchPromotionBundleHasCompleteBasePrice(latest) {
			if latest.PromoMatched || latest.ResponseStatus > 0 || options.FastNoPromoReturn || !now.Before(fallbackDeadline) {
				return latest, int(now.Sub(started).Milliseconds())
			}
		}
		if !now.Before(deadline) {
			return latest, int(now.Sub(started).Milliseconds())
		}
		if !sleepFn(ctx, pollInterval) {
			return latest, int(nowFn().Sub(started).Milliseconds())
		}
	}
}

func (s *tripScraper) currentBatchPromotionPriceBundle(selectedTexts []string) batchPromotionPriceBundle {
	var bundle batchPromotionPriceBundle
	_ = s.evalJSON(&bundle, `(selectedTexts, expectedCurrency) => {
		const normalizeText = (value) => String(value || '').trim().replace(/\s+/g, ' ').toLowerCase();
		const textIncludesAll = (value, parts) => {
			const text = normalizeText(value);
			return (parts || []).every((part) => text.includes(normalizeText(part)));
		};
		const packageText = (item) => {
			const values = ((item && item.salesProperties) || []).flatMap((property) =>
				(property.salePropertyValues || []).map((value) => value && value.name)
			);
			return [item && item.packageName, ...values].filter(Boolean).join(' | ');
		};
		const nextScript = document.querySelector('script#__NEXT_DATA__');
		let product = null;
		try {
			const data = JSON.parse((nextScript && nextScript.textContent) || '{}');
			product = data.props.pageProps.initialState.__STATE_MAP__.PageModel.ds.getProductInfo[0];
		} catch (e) {}
		const packages = (product && (product.packageInfos || (product.basicInfo && product.basicInfo.packages))) || [];
		const resources = (product && product.resourceInfos) || [];
		const resourceByID = new Map(resources.map((resource) => [Number(resource.resourceId), resource]));
		const packageInfo = packages.find((item) => textIncludesAll(packageText(item), selectedTexts)) || null;
		const resourceID = Number((packageInfo && packageInfo.resourceIds && packageInfo.resourceIds[0]) || 0);
		const resource = resourceByID.get(resourceID) || null;
		const basic = (resource && resource.basicInfo) || {};
		const visible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
			return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		};
		const textOf = (el) => (el && (el.innerText || el.textContent || '').trim().replace(/\s+/g, ' ')) || '';
		const firstRenderedBasePrice = () => {
			const footer = Array.from(document.querySelectorAll('div[class*="price-comp_m_footer_left"], div[class*="default_new_title_foot_warp"], #select_quantity')).find(visible);
			const root = footer || document.body;
			for (const el of Array.from(root.querySelectorAll('span,div'))) {
				if (!visible(el)) continue;
				const text = textOf(el);
				const match = text.match(/HK\$\s*([0-9,]+(?:\.\d+)?)/);
				if (match) return Number(match[1].replace(/,/g, ''));
			}
			return 0;
		};
		const out = {
			package_id: Number((packageInfo && packageInfo.packageId) || 0),
			resource_id: resourceID,
			item_id: 0,
			currency: basic.originMarketCurrency || expectedCurrency,
			original_price: Number(basic.minPrice || 0) || firstRenderedBasePrice(),
			deduction_amount: 0,
			deduction_price: 0,
			promo_display: '',
			promo_name: '',
			promo_matched: false,
			response_status: 0,
			response_url: '',
			response_text: ''
		};
		const responses = (window.__tripCdpBatchPromotionResponses || []).slice().reverse();
		for (const response of responses) {
			let parsed = null;
			try { parsed = JSON.parse(response.text || '{}'); } catch (e) {}
			const aggregations = (parsed && parsed.searchItemAggregations) || [];
			for (const aggregation of aggregations) {
				for (const item of (aggregation.itemList || [])) {
					if (Number(item.itemId) !== resourceID) continue;
					const promos = item.salespromotionList || [];
					const promo = promos[0] || {};
					out.response_status = Number(response.status || 0);
					out.response_url = String(response.url || '');
					out.response_text = String(response.text || '');
					out.item_id = Number(item.itemId || 0);
					out.promo_matched = promos.length > 0;
					out.promo_display = String(promo.displayName || '');
					out.promo_name = String(promo.name || '');
					out.deduction_amount = Number(promo.deductionAmount || 0);
					out.deduction_price = Number(promo.deductionPrice || 0);
					if (!out.deduction_amount && Array.isArray(promo.deductionList) && promo.deductionList.length && out.original_price) {
						const hit = promo.deductionList.find((entry) => entry && entry.hit) || promo.deductionList[0];
						const deduction = Number(hit && hit.deduction || 0);
						const type = Number(hit && hit.type || promo.deductionType || 0);
						if (type === 1 && deduction > 0 && deduction < 1) {
							out.deduction_amount = out.original_price * deduction;
						}
					}
					return out;
				}
			}
		}
		return out;
	}`, selectedTexts, s.request.Market.Currency)
	if bundle.ResourceID > 0 && bundle.ResponseText != "" {
		applyBatchPromotionResponse(&bundle, bundle.ResponseText, bundle.ResponseStatus, bundle.ResponseURL)
	}
	return bundle
}

func applyBatchPromotionResponse(bundle *batchPromotionPriceBundle, responseText string, status int, responseURL string) bool {
	if bundle == nil || bundle.ResourceID == 0 || strings.TrimSpace(responseText) == "" {
		return false
	}
	var payload struct {
		SearchItemAggregations []struct {
			ItemList []struct {
				ItemID             int64 `json:"itemId"`
				SalesPromotionList []struct {
					DisplayName     string  `json:"displayName"`
					Name            string  `json:"name"`
					DeductionAmount float64 `json:"deductionAmount"`
					DeductionPrice  float64 `json:"deductionPrice"`
					DeductionType   int     `json:"deductionType"`
					DeductionList   []struct {
						Hit       bool    `json:"hit"`
						Deduction float64 `json:"deduction"`
						Type      int     `json:"type"`
					} `json:"deductionList"`
				} `json:"salespromotionList"`
			} `json:"itemList"`
		} `json:"searchItemAggregations"`
	}
	if err := json.Unmarshal([]byte(responseText), &payload); err != nil {
		return false
	}
	for _, aggregation := range payload.SearchItemAggregations {
		for _, item := range aggregation.ItemList {
			if item.ItemID != bundle.ResourceID {
				continue
			}
			bundle.ItemID = item.ItemID
			bundle.ResponseStatus = status
			bundle.ResponseURL = responseURL
			bundle.PromoMatched = len(item.SalesPromotionList) > 0
			if !bundle.PromoMatched {
				bundle.PromoDisplay = ""
				bundle.PromoName = ""
				bundle.DeductionAmount = 0
				bundle.DeductionPrice = 0
				return true
			}
			promo := item.SalesPromotionList[0]
			bundle.PromoDisplay = promo.DisplayName
			bundle.PromoName = promo.Name
			bundle.DeductionAmount = promo.DeductionAmount
			bundle.DeductionPrice = promo.DeductionPrice
			if bundle.DeductionAmount == 0 && bundle.OriginalPrice > 0 && len(promo.DeductionList) > 0 {
				hit := promo.DeductionList[0]
				for _, entry := range promo.DeductionList {
					if entry.Hit {
						hit = entry
						break
					}
				}
				deductionType := hit.Type
				if deductionType == 0 {
					deductionType = promo.DeductionType
				}
				if deductionType == 1 && hit.Deduction > 0 && hit.Deduction < 1 {
					bundle.DeductionAmount = bundle.OriginalPrice * hit.Deduction
				}
			}
			return true
		}
	}
	return false
}

func currentCombinationPriceRowFromBatchPromotion(selected []skuOption, bundle batchPromotionPriceBundle, extractRealPrice bool, expectedCurrency string) map[string]string {
	if len(selected) == 0 {
		return nil
	}
	parts := make([]string, 0, len(selected))
	for _, option := range selected {
		parts = append(parts, option.Text)
	}
	combination := strings.Join(parts, " | ")
	expectedCurrency = strings.ToUpper(strings.TrimSpace(expectedCurrency))
	bundleCurrency := strings.ToUpper(strings.TrimSpace(bundle.Currency))
	if bundleCurrency != "" && bundleCurrency != expectedCurrency {
		return map[string]string{
			"combination":    combination,
			"selected_texts": combination,
			"currency":       "",
			"price":          "",
			"real_currency":  "",
			"real_price":     "",
			"promo_tag":      "",
			"resource_id":    strconv.FormatInt(bundle.ResourceID, 10),
			"item_id":        strconv.FormatInt(bundle.ItemID, 10),
			"promo_matched":  strconv.FormatBool(bundle.PromoMatched),
			"price_source":   priceSourceBatchPromotion,
			"price_status":   "currency_mismatch",
			"scrape_note":    "returned currency does not match requested market",
		}
	}
	currency := firstNonEmpty(bundleCurrency, expectedCurrency)
	base := map[string]string{
		"combination":    combination,
		"selected_texts": combination,
		"currency":       currency,
		"real_currency":  "N/A",
		"promo_tag":      "",
		"resource_id":    strconv.FormatInt(bundle.ResourceID, 10),
		"item_id":        strconv.FormatInt(bundle.ItemID, 10),
		"promo_matched":  strconv.FormatBool(bundle.PromoMatched),
		"price_source":   priceSourceBatchPromotion,
	}
	if bundle.ResponseStatus > 0 {
		base["response_status"] = strconv.Itoa(bundle.ResponseStatus)
	}
	if strings.TrimSpace(bundle.ResponseURL) != "" {
		base["response_url"] = strings.TrimSpace(bundle.ResponseURL)
	}
	if strings.TrimSpace(bundle.ResponseText) != "" {
		base["response_preview"] = previewText(bundle.ResponseText, 300)
	}
	if bundle.OriginalPrice <= 0 {
		base["currency"] = "N/A"
		base["price"] = ""
		base["real_price"] = ""
		base["price_status"] = priceStatusUntrustedPriceMissing
		base["scrape_note"] = fmt.Sprintf("价格未获取: resourceId=%d itemId=%d promoMatched=%v", bundle.ResourceID, bundle.ItemID, bundle.PromoMatched)
		return base
	}
	price := formatPriceNumber(bundle.OriginalPrice)
	base["raw_captured_price"] = price
	if bundle.ResourceID <= 0 {
		base["price"] = ""
		base["real_price"] = ""
		base["price_status"] = priceStatusUntrustedResourceMiss
		base["scrape_note"] = fmt.Sprintf("价格未可信匹配: resourceId=%d itemId=%d promoMatched=%v fallback=%s%s", bundle.ResourceID, bundle.ItemID, bundle.PromoMatched, currency, price)
		return base
	}
	status := priceStatusTrustedBase
	if bundle.PromoMatched && bundle.ItemID == bundle.ResourceID {
		status = priceStatusTrustedPromo
	}
	base["price_status"] = status
	realCurrency, realPrice := "N/A", "N/A"
	if extractRealPrice {
		realCurrency = currency
		if status == priceStatusTrustedPromo && bundle.DeductionAmount > 0 {
			realPrice = formatPriceNumber(bundle.OriginalPrice - bundle.DeductionAmount)
		} else {
			realPrice = price
		}
	}
	promoTag := ""
	if bundle.PromoMatched {
		promoText := firstNonEmpty(bundle.PromoDisplay, bundle.PromoName)
		if promoText != "" {
			promoTag = "即" + promoText
		}
	}
	base["price"] = price
	base["real_currency"] = realCurrency
	base["real_price"] = realPrice
	base["promo_tag"] = promoTag
	return base
}

func isTrustedPriceStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case priceStatusTrustedPromo, priceStatusTrustedBase:
		return true
	default:
		return false
	}
}

func (s *tripScraper) waitBaseRatingBundleStable(ctx context.Context, maxWait time.Duration) (baseRatingBundle, int) {
	started := time.Now()
	samples := []baseRatingBundle{}
	deadline := started.Add(maxWait)
	reviewMetricsNudged := false
	for {
		if ctx.Err() != nil {
			return selectBaseRatingBundle(samples), elapsedMS(started)
		}
		bundle := s.currentBaseRatingBundle()
		bundle.SampleAtMS = elapsedMS(started)
		samples = append(samples, bundle)
		if stable, ok := stableBaseRatingBundle(samples); ok {
			s.scrollToPageTop()
			return stable, elapsedMS(started)
		}
		if !reviewMetricsNudged && bundle.Score != "" && (bundle.Customer == "" || bundle.Convenience == "" || bundle.Signal == "" || bundle.BadReviewCount == "") {
			reviewMetricsNudged = true
			s.nudgeReviewMetricsLoad()
		}
		if time.Now().After(deadline) {
			s.scrollToPageTop()
			return selectBaseRatingBundle(samples), elapsedMS(started)
		}
		select {
		case <-ctx.Done():
			s.scrollToPageTop()
			return selectBaseRatingBundle(samples), elapsedMS(started)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *tripScraper) currentBaseRatingBundle() baseRatingBundle {
	var bundle baseRatingBundle
	_ = s.evalJSON(&bundle, baseRatingBundleJS, s.request.ExtractProductInfo)
	return normalizeBaseRatingBundle(bundle)
}

func (s *tripScraper) nudgeReviewMetricsLoad() {
	if s.page == nil {
		return
	}
	_, _ = s.page.Evaluate(rod.Eval(`() => {
		const textOf = (el) => (el && (el.textContent || '').trim()) || '';
		const reviewMetric = Array.from(document.querySelectorAll('div')).find((el) => {
			const text = textOf(el);
			return text === '客戶服務' || text === '方便程度' || text === '訊號狀態' || text.includes('中性評價及負面評價');
		});
		if (reviewMetric) {
			reviewMetric.scrollIntoView({block: 'center'});
			return;
		}
		window.scrollTo(0, Math.floor(document.body.scrollHeight * 0.55));
	}`))
}

func (s *tripScraper) scrollToPageTop() {
	if s.page == nil {
		return
	}
	_, _ = s.page.Evaluate(rod.Eval(`() => window.scrollTo(0, 0)`))
}

func stableBaseRatingBundle(samples []baseRatingBundle) (baseRatingBundle, bool) {
	if len(samples) < 2 {
		return baseRatingBundle{}, false
	}
	last := samples[len(samples)-1]
	prev := samples[len(samples)-2]
	if last.Score == "" {
		return baseRatingBundle{}, false
	}
	if last.ReviewCount == "" || last.Customer == "" || last.Convenience == "" || last.Signal == "" || last.BadReviewCount == "" {
		return baseRatingBundle{}, false
	}
	if baseRatingSignature(last) == "" || baseRatingSignature(last) != baseRatingSignature(prev) {
		return baseRatingBundle{}, false
	}
	return last, true
}

func selectBaseRatingBundle(samples []baseRatingBundle) baseRatingBundle {
	if len(samples) == 0 {
		return baseRatingBundle{}
	}
	if stable, ok := stableBaseRatingBundle(samples); ok {
		return stable
	}
	best := samples[len(samples)-1]
	bestScore := baseRatingCompleteness(best)
	for _, sample := range samples {
		score := baseRatingCompleteness(sample)
		if score > bestScore || (score == bestScore && sample.SampleAtMS > best.SampleAtMS) {
			best = sample
			bestScore = score
		}
	}
	return best
}

func baseRatingCompleteness(bundle baseRatingBundle) int {
	score := 0
	for _, value := range []string{bundle.Score, bundle.Customer, bundle.Convenience, bundle.Signal, bundle.BadReviewCount, bundle.ProductInfo, bundle.BookingCount, bundle.ReviewCount} {
		if strings.TrimSpace(value) != "" {
			score++
		}
	}
	return score
}

func baseRatingSignature(bundle baseRatingBundle) string {
	if strings.TrimSpace(bundle.Signature) != "" {
		return strings.TrimSpace(bundle.Signature)
	}
	return strings.Join([]string{
		strings.TrimSpace(bundle.Score),
		strings.TrimSpace(bundle.Customer),
		strings.TrimSpace(bundle.Convenience),
		strings.TrimSpace(bundle.Signal),
		strings.TrimSpace(bundle.BadReviewCount),
		strings.TrimSpace(bundle.ProductInfo),
	}, "||")
}

func normalizeBaseRatingBundle(bundle baseRatingBundle) baseRatingBundle {
	bundle.BookingCount = numericText(bundle.BookingCount)
	bundle.ReviewCount = numericText(bundle.ReviewCount)
	bundle.Score = scoreText(bundle.Score)
	bundle.Customer = scoreText(bundle.Customer)
	bundle.Convenience = scoreText(bundle.Convenience)
	bundle.Signal = scoreText(bundle.Signal)
	bundle.BadReviewCount = numericText(bundle.BadReviewCount)
	bundle.ProductInfo = compactText(bundle.ProductInfo)
	bundle.Signature = baseRatingSignature(bundle)
	return bundle
}

func (s *tripScraper) waitCurrentPriceBundleStable(ctx context.Context, maxWait time.Duration) (priceBundle, int) {
	started := time.Now()
	samples := []priceBundle{}
	deadline := started.Add(maxWait)
	for {
		if ctx.Err() != nil {
			return selectStablePriceBundle(samples, time.Since(started), s.request.Market), elapsedMS(started)
		}
		bundle := s.currentPriceBundle()
		bundle.SampleAtMS = elapsedMS(started)
		samples = append(samples, bundle)
		if stable, ok := stablePriceBundle(samples, 350*time.Millisecond, s.request.Market); ok {
			return stable, elapsedMS(started)
		}
		if time.Now().After(deadline) {
			return selectStablePriceBundle(samples, time.Since(started), s.request.Market), elapsedMS(started)
		}
		select {
		case <-ctx.Done():
			return selectStablePriceBundle(samples, time.Since(started), s.request.Market), elapsedMS(started)
		case <-time.After(120 * time.Millisecond):
		}
	}
}

func stablePriceBundle(samples []priceBundle, minAge time.Duration, market MarketContext) (priceBundle, bool) {
	if len(samples) < 2 {
		return priceBundle{}, false
	}
	last := samples[len(samples)-1]
	prev := samples[len(samples)-2]
	if time.Duration(last.SampleAtMS)*time.Millisecond < minAge {
		return priceBundle{}, false
	}
	if !isValidDisplayPriceForMarket(last.DisplayPrice, market) {
		return priceBundle{}, false
	}
	if bundleSignature(last) == "" || bundleSignature(last) != bundleSignature(prev) {
		return priceBundle{}, false
	}
	return last, true
}

func selectStablePriceBundle(samples []priceBundle, elapsed time.Duration, market MarketContext) priceBundle {
	if len(samples) == 0 {
		return priceBundle{}
	}
	if stable, ok := stablePriceBundle(samples, 0, market); ok {
		return stable
	}
	valid := func(value string) bool { return isValidDisplayPriceForMarket(value, market) }
	for i := len(samples) - 1; i >= 0; i-- {
		if valid(samples[i].DisplayPrice) && time.Duration(samples[i].SampleAtMS)*time.Millisecond >= min(elapsed, 350*time.Millisecond) {
			return samples[i]
		}
	}
	for i := len(samples) - 1; i >= 0; i-- {
		if valid(samples[i].DisplayPrice) {
			return samples[i]
		}
	}
	return samples[len(samples)-1]
}

func bundleSignature(bundle priceBundle) string {
	signature := strings.TrimSpace(bundle.FooterSignature)
	if signature != "" {
		return signature
	}
	return strings.Join([]string{
		strings.TrimSpace(bundle.DisplayPrice),
		strings.TrimSpace(bundle.RealPrice),
		strings.TrimSpace(bundle.PromoTag),
		strings.TrimSpace(bundle.FooterText),
	}, "||")
}

func currentCombinationPriceRow(selected []skuOption, bundle priceBundle, fallbackDisplayPrice string, extractRealPrice bool, market MarketContext) map[string]string {
	if len(selected) == 0 {
		return nil
	}
	parts := make([]string, 0, len(selected))
	for _, option := range selected {
		parts = append(parts, option.Text)
	}
	combination := strings.Join(parts, " | ")
	displayPriceText := bundle.DisplayPrice
	if displayPriceText == "" {
		displayPriceText = fallbackDisplayPrice
	}
	currency, price := parseDisplayPrice(displayPriceText, market)
	realCurrency, realPrice := "N/A", "N/A"
	if extractRealPrice {
		realCurrency, realPrice = parseDisplayPrice(bundle.RealPrice, market)
		if realPrice == "N/A" {
			realCurrency, realPrice = "N/A", ""
		}
	}
	if price != "" && price != "价格未找到" {
		return map[string]string{"combination": combination, "currency": currency, "price": price, "real_currency": realCurrency, "real_price": realPrice, "promo_tag": bundle.PromoTag}
	}
	return map[string]string{"combination": combination, "currency": "N/A", "price": "价格获取失败", "real_currency": "N/A", "real_price": "N/A", "promo_tag": ""}
}

func (s *tripScraper) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state := s.evalString(`() => document.readyState || ""`)
		if state == "interactive" || state == "complete" {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func (s *tripScraper) startNavigationMonitor(ctx context.Context) *navigationMonitor {
	if s.page == nil {
		return nil
	}
	_ = proto.NetworkEnable{}.Call(s.page)
	eventCtx, cancel := context.WithCancel(ctx)
	monitor := &navigationMonitor{cancel: cancel}
	wait := s.page.Context(eventCtx).EachEvent(func(e *proto.NetworkResponseReceived) {
		if e == nil || e.Response == nil || e.Type != proto.NetworkResourceTypeDocument {
			return
		}
		monitor.mu.Lock()
		monitor.mainStatus = e.Response.Status
		monitor.mainURL = e.Response.URL
		monitor.mainStatusText = e.Response.StatusText
		monitor.mu.Unlock()
	})
	monitor.stop = wait
	go func() {
		wait()
	}()
	return monitor
}

func (m *navigationMonitor) snapshot() (int, string, string) {
	if m == nil {
		return 0, "", ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mainStatus, m.mainURL, m.mainStatusText
}

func (m *navigationMonitor) close() {
	if m == nil {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
}

func (s *tripScraper) waitProductPageReady(ctx context.Context, maxWait time.Duration) (pageSnapshot, int) {
	started := time.Now()
	deadline := started.Add(maxWait)
	var last pageSnapshot
	for {
		if ctx.Err() != nil {
			return last, elapsedMS(started)
		}
		last = s.currentPageSnapshot()
		if isProductPageReady(last) || last.RiskChallenge {
			return last, elapsedMS(started)
		}
		if time.Now().After(deadline) {
			return last, elapsedMS(started)
		}
		select {
		case <-ctx.Done():
			return last, elapsedMS(started)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (s *tripScraper) waitProductPageExtractable(ctx context.Context, maxWait time.Duration, ratingOnly bool) (pageSnapshot, int) {
	started := time.Now()
	deadline := started.Add(maxWait)
	var last pageSnapshot
	for {
		if ctx.Err() != nil {
			return last, elapsedMS(started)
		}
		last = s.currentPageSnapshot()
		if isProductPageExtractable(last, ratingOnly) || last.RiskChallenge {
			return last, elapsedMS(started)
		}
		if time.Now().After(deadline) {
			return last, elapsedMS(started)
		}
		select {
		case <-ctx.Done():
			return last, elapsedMS(started)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (s *tripScraper) currentPageSnapshot() pageSnapshot {
	var snapshot pageSnapshot
	market := s.request.Market
	err := s.evalJSON(&snapshot, `(currency, pricePrefixes) => {
		const visible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
			return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		};
		const body = document.body;
		const bodyText = body ? ((body.innerText || body.textContent || '').trim().replace(/\s+/g, ' ')) : '';
		const lower = bodyText.toLowerCase();
		const hasAny = (selectors) => selectors.some(selector => Array.from(document.querySelectorAll(selector)).some(visible));
		const hasText = (patterns) => patterns.some(pattern => bodyText.includes(pattern));
		const hasExpectedPrice = () => {
			for (const el of document.querySelectorAll('span,div')) {
				if (!visible(el)) continue;
				const text = (el.innerText || el.textContent || '').trim();
				if (!/[0-9,]+(?:\.\d+)?/.test(text)) continue;
				if (String(el.getAttribute('currency') || '').toUpperCase() === currency || pricePrefixes.some(prefix => text.startsWith(prefix))) return true;
			}
			return false;
		};
		const title = document.title || '';
		const explicitCloudflareOverlay = document.querySelector('#guestLogin-sliderTntLoginBanneribu_nonmemberlogin_online_pic_cf_container');
		const cloudflareOverlay = !!explicitCloudflareOverlay && visible(explicitCloudflareOverlay);
		const whaleguard = lower.includes('whaleguard');
		const riskChallenge = whaleguard || lower.includes('verify you are human') || lower.includes('captcha') || lower.includes('security verification') || bodyText.includes('安全验证') || bodyText.includes('安全驗證') || bodyText.includes('人机验证') || bodyText.includes('人機驗證') || bodyText.includes('請完成驗證') || bodyText.includes('请完成验证');
		return {
			url: window.location.href || '',
			ready_state: document.readyState || '',
			body_text_length: bodyText.length,
			title,
			has_product_title: hasAny(['h1', '[data-testid="product-title"]']) || hasText(['套餐與價格', '套餐与价格', '商品詳情', '商品详情']),
			has_sku: hasAny(['div[testid="act_sku_wrapper"]', 'div[class*="style_sku_wrapper_container"]', 'div[class*="sku_tab_ceil"]']) || hasText(['套餐與價格', '套餐与价格']),
			has_price: hasExpectedPrice(),
			has_score: hasAny(['span[testid="productdetail-productInfo-commentScore"]', '[class*="commentScore"]']),
			risk_challenge: riskChallenge,
			risk_kind: whaleguard ? 'whaleguard' : (cloudflareOverlay ? 'cloudflare' : (riskChallenge ? 'challenge' : '')),
			cloudflare_block: cloudflareOverlay,
			body_preview: bodyText.slice(0, 200),
		};
	}`, market.Currency, market.PricePrefixes)
	if err != nil {
		snapshot.EvalError = err.Error()
	}
	return snapshot
}

func (snapshot *pageSnapshot) withNavigationStatus(status int, responseURL string) {
	if snapshot == nil {
		return
	}
	if status > 0 {
		snapshot.HTTPStatus = status
	}
	if strings.TrimSpace(responseURL) != "" {
		snapshot.ResponseURL = responseURL
	}
	if status == 432 && snapshot.RiskKind == "" {
		snapshot.RiskKind = "whaleguard"
		snapshot.RiskChallenge = true
	}
}

func (snapshot *pageSnapshot) applyRisk(risk riskDiagnostics) {
	if snapshot == nil {
		return
	}
	if risk.Challenge {
		snapshot.RiskChallenge = true
	}
	if risk.Kind != "" {
		snapshot.RiskKind = risk.Kind
	}
	if risk.HTTPStatus > 0 {
		snapshot.HTTPStatus = risk.HTTPStatus
	}
	if risk.BodyPreview != "" {
		snapshot.BodyPreview = risk.BodyPreview
	}
}

func isProductPageReady(snapshot pageSnapshot) bool {
	if strings.TrimSpace(snapshot.EvalError) != "" {
		return false
	}
	if strings.TrimSpace(snapshot.URL) == "" {
		return false
	}
	if snapshot.BodyTextLength == 0 {
		return false
	}
	if snapshot.RiskChallenge {
		return true
	}
	if snapshot.HasProductTitle || snapshot.HasSKU || snapshot.HasPrice || snapshot.HasScore {
		return true
	}
	return snapshot.BodyTextLength >= 1200 && strings.TrimSpace(snapshot.Title) != ""
}

func isProductPageExtractable(snapshot pageSnapshot, ratingOnly bool) bool {
	if !isProductPageReady(snapshot) {
		return false
	}
	if snapshot.RiskChallenge {
		return false
	}
	if snapshot.MarketContextClass != "" {
		return ratingOnly && snapshot.MarketContextClass == "market_context_unverified" && strings.TrimSpace(snapshot.Market.Host) != ""
	}
	if ratingOnly {
		return snapshot.HasProductTitle || snapshot.HasScore || snapshot.HasPrice || snapshot.HasSKU
	}
	return snapshot.HasSKU || snapshot.HasPrice
}

func allowNoSKUFallback(row map[string]string, snapshot pageSnapshot) bool {
	if !isProductPageExtractable(snapshot, false) {
		return false
	}
	if !snapshot.HasPrice {
		return false
	}
	for _, key := range []string{"商品名称", "评分", "评价数", "预定数", "月销量", "年销量", "总销量"} {
		if strings.TrimSpace(row[key]) != "" {
			return true
		}
	}
	return snapshot.HasProductTitle || snapshot.HasPrice
}

func isWhaleguardSnapshot(snapshot pageSnapshot) bool {
	return snapshot.HTTPStatus == 432 || strings.EqualFold(strings.TrimSpace(snapshot.RiskKind), "whaleguard") || strings.Contains(strings.ToLower(snapshot.BodyPreview), "whaleguard block")
}

func (s *tripScraper) enableRiskProtection() {
	if s.riskProtection {
		return
	}
	current := s.request.DelayOptions.Profile
	if current == "slow" || current == "safe" {
		s.riskProtection = true
		return
	}
	safeOptions, err := delayProfileOptions("safe")
	if err != nil {
		return
	}
	s.request.DelayOptions = safeOptions
	s.riskProtection = true
	s.emit(Event{"event": "log", "stage": "risk", "message": "risk protection enabled; delay profile raised to safe", "delay_profile": "safe"})
}

func riskCooldownDelay(profile string) time.Duration {
	var delayRange DelayRange
	switch profile {
	case "safe":
		delayRange = secondsRange(60, 120)
	case "slow":
		delayRange = secondsRange(120, 240)
	default:
		delayRange = secondsRange(30, 60)
	}
	return randomDelay(delayRange.Min, delayRange.Max)
}

func (s *tripScraper) scrollPage(ctx context.Context) {
	for i := 0; i < 4; i++ {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.page.Evaluate(rod.Eval(`() => window.scrollTo(0, document.body.scrollHeight)`))
		if !sleepDelay(ctx, s.emit, "lazy_load", "scroll", s.request.DelayOptions.Scroll) {
			return
		}
	}
	_, _ = s.page.Evaluate(rod.Eval(`() => window.scrollTo(0, 0)`))
}

func (s *tripScraper) cleanupAfterURL() {
	if s.page == nil {
		return
	}
	_, _ = s.page.Evaluate(rod.Eval(`() => window.stop()`))
	_ = proto.NetworkClearBrowserCache{}.Call(s.page)
	_ = proto.HeapProfilerCollectGarbage{}.Call(s.page)
}

func (s *tripScraper) maybeRestart(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	options := s.request.BrowserOptions
	if !options.MemoryGuardEnabled {
		return nil
	}
	s.processed++
	reason := ""
	if options.MemoryLimitMB > 0 {
		if rss := managedBrowserRSSMB(s.userDataDir); rss >= float64(options.MemoryLimitMB) {
			reason = fmt.Sprintf("Chrome memory %.0fMB exceeded %dMB", rss, options.MemoryLimitMB)
		}
	}
	if reason == "" && options.RestartEvery > 0 && s.processed >= options.RestartEvery {
		reason = fmt.Sprintf("processed %d URLs", s.processed)
	}
	if reason == "" {
		return nil
	}
	return s.restartBrowser(ctx, reason)
}

func (s *tripScraper) restartBrowser(ctx context.Context, reason string) error {
	return s.restartBrowserWithSessionSave(ctx, reason, true)
}

func (s *tripScraper) restartBrowserNoSessionSave(ctx context.Context, reason string) error {
	return s.restartBrowserWithSessionSave(ctx, reason, false)
}

func (s *tripScraper) restartBrowserWithSessionSave(ctx context.Context, reason string, saveSessionBeforeRestart bool) error {
	if reason == "" {
		reason = "runtime recovery"
	}
	s.emit(Event{"event": "log", "stage": "browser_restart", "message": "restarting Chrome: " + reason})
	if saveSessionBeforeRestart && isProductPageReady(s.currentPageSnapshot()) {
		s.saveSession()
	} else {
		message := "skip session save before restart because current page is unhealthy"
		if !saveSessionBeforeRestart {
			message = "skip session save before restart because current page hit risk challenge"
		}
		s.emit(Event{"event": "warning", "stage": "browser_restart", "message": message})
	}
	s.close()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.processed = 0
	s.storageApplied = map[string]bool{}
	s.restartAfterURL = false
	s.restartReason = ""
	return s.start(ctx)
}

func (s *tripScraper) saveSession() bool {
	if s.page == nil {
		return false
	}
	return saveSession(s.page, s.request.BrowserOptions.SessionFile)
}

func (s *tripScraper) close() {
	if s.page != nil {
		_, _ = s.page.Evaluate(rod.Eval(`() => window.stop()`))
		s.page = nil
	}
	if s.browser != nil {
		_ = s.browser.Close()
		s.browser = nil
	}
	if s.driverRuntime != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		s.driverRuntime.stop(ctx)
		cancel()
		s.driverRuntime = nil
	}
	if s.directChrome != nil {
		terminateProcessTree(s.directChrome)
		s.directChrome = nil
	}
	if s.userDataDir != "" && (s.removeUserDataDir || strings.Contains(s.userDataDir, string(filepath.Separator)+"_workers"+string(filepath.Separator))) {
		killChromeByUserDataDir(s.userDataDir)
	}
	if s.userDataDir != "" && s.removeUserDataDir {
		_ = os.RemoveAll(s.userDataDir)
	}
	s.userDataDir = ""
	s.removeUserDataDir = false
}

func (s *tripScraper) shouldApplySessionStorage(targetURL string) bool {
	origin := targetOrigin(targetURL)
	if origin == "" {
		origin = targetURL
	}
	if s.storageApplied == nil {
		s.storageApplied = map[string]bool{}
	}
	if s.storageApplied[origin] {
		return false
	}
	s.storageApplied[origin] = true
	return true
}

func (s *tripScraper) firstText(selectors []string) string {
	for _, selector := range selectors {
		text := s.evalString(`(selector) => {
			for (const el of document.querySelectorAll(selector)) {
				const text = (el.innerText || el.textContent || '').trim();
				if (text) return text.replace(/\s+/g, ' ');
			}
			return '';
		}`, selector)
		if text != "" {
			return text
		}
	}
	return ""
}

func (s *tripScraper) titleFromDocument() string {
	title := s.evalString(`() => document.title || ''`)
	return strings.TrimSpace(strings.Split(title, "|")[0])
}

func (s *tripScraper) currentURL() string {
	url := s.evalString(`() => window.location.href || ''`)
	if url != "" || s.page == nil {
		return url
	}
	if info, err := s.page.Info(); err == nil && info != nil {
		return info.URL
	}
	return ""
}

func (s *tripScraper) pageText() string {
	return s.evalString(`() => document.body ? document.body.innerText : ''`)
}

type riskDiagnostics struct {
	Challenge   bool
	Reason      string
	Kind        string
	HTTPStatus  int
	BodyPreview string
	Scripts     []string
}

func (s *tripScraper) detectRiskChallenge() riskDiagnostics {
	var payload struct {
		URL     string   `json:"url"`
		Body    string   `json:"body"`
		Scripts []string `json:"scripts"`
		HasSKU  bool     `json:"has_sku"`
	}
	_ = s.evalJSON(&payload, `() => {
		const riskPatterns = ['jigsaw-captcha', 'guestverify', 'ubtrms', '/rms', 'c-sec', 'whaleguard'];
		const scripts = Array.from(document.scripts || [])
			.map(script => script.src || '')
			.filter(src => src && riskPatterns.some(pattern => src.toLowerCase().includes(pattern)));
		const body = (document.body && (document.body.innerText || document.body.textContent) || '').trim().replace(/\s+/g, ' ');
		const hasSKU = !!document.querySelector('div[testid="act_sku_wrapper"], div[class*="style_sku_wrapper_container"], div[class*="sku_tab_ceil"]') || body.includes('套餐與價格') || body.includes('套餐与价格');
		return {url: window.location.href || '', body, scripts, has_sku: hasSKU};
	}`)
	return classifyRiskChallenge(payload.Body, payload.URL, payload.Scripts, payload.HasSKU, 0)
}

type cloudflareOverlayDiagnostics struct {
	Detected    bool
	Reason      string
	BodyPreview string
}

func (s *tripScraper) detectCloudflareOverlay() cloudflareOverlayDiagnostics {
	var payload struct {
		Detected bool   `json:"detected"`
		Reason   string `json:"reason"`
		Body     string `json:"body"`
	}
	_ = s.evalJSON(&payload, `() => {
		const body = (document.body && (document.body.innerText || document.body.textContent) || '').trim().replace(/\s+/g, ' ');
		const visible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
			return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		};
		const explicit = document.querySelector('#guestLogin-sliderTntLoginBanneribu_nonmemberlogin_online_pic_cf_container');
		if (visible(explicit)) {
			return {detected: true, reason: 'cloudflare overlay: guestLogin-sliderTntLoginBanneribu_nonmemberlogin_online_pic_cf_container', body};
		}
		const close = document.querySelector('#guestLogin-sliderTntLoginBanneribu_nonmemberlogin_online_pic_cf_container .cf-close-container .cf-close, .cf-close-container .cf-close');
		if (visible(close) && body.toLowerCase().includes('cloudflare')) {
			return {detected: true, reason: 'cloudflare overlay close control visible', body};
		}
		const turnstileLink = document.querySelector('a[href*="cloudflare.com/products/turnstile"], a[href*="/cdn-cgi/challenge-platform/help"]');
		if (visible(turnstileLink)) {
			return {detected: true, reason: 'cloudflare turnstile widget link visible', body};
		}
		const humanVerifyCheckbox = Array.from(document.querySelectorAll('label input[type="checkbox"]')).find(input => {
			const label = input.closest('label');
			const text = label ? ((label.innerText || label.textContent || '').trim()) : '';
			return visible(input) && (text.includes('驗證您是人類') || text.includes('验证您是人类') || text.toLowerCase().includes('verify you are human'));
		});
		if (humanVerifyCheckbox) {
			return {detected: true, reason: 'cloudflare human verification checkbox visible', body};
		}
		const challengeError = document.querySelector('#challenge-error-text');
		if (visible(challengeError)) {
			return {detected: true, reason: 'cloudflare challenge error element visible', body};
		}
		const iframe = Array.from(document.querySelectorAll('iframe')).find(f => ((f.src || '') + ' ' + (f.title || '')).toLowerCase().includes('cloudflare') || (f.src || '').toLowerCase().includes('challenges.cloudflare.com'));
		if (visible(iframe)) {
			return {detected: true, reason: 'cloudflare iframe visible', body};
		}
		const lower = body.toLowerCase();
		if (body.includes('驗證您是人類') || body.includes('验证您是人类') || body.includes('正在驗證') || body.includes('正在验证') || body.includes('驗證失敗') || body.includes('验证失败') || body.includes('驗證已過期') || body.includes('验证已过期')) {
			return {detected: true, reason: 'cloudflare turnstile verification text visible', body};
		}
		if (lower.includes('cloudflare') && (lower.includes('verify you are human') || lower.includes('just a moment') || lower.includes('checking if the site connection is secure'))) {
			return {detected: true, reason: 'cloudflare challenge text visible', body};
		}
		return {detected: false, reason: '', body};
	}`)
	if !payload.Detected {
		return cloudflareOverlayDiagnostics{}
	}
	return cloudflareOverlayDiagnostics{Detected: true, Reason: firstNonEmpty(payload.Reason, "cloudflare overlay detected"), BodyPreview: previewText(payload.Body, 200)}
}

func (s *tripScraper) recordCloudflareIfPresent(stage string, base Event) bool {
	diag := s.detectCloudflareOverlay()
	if !diag.Detected {
		return false
	}
	s.markCloudflareBlocked(stage, base, diag.Reason, diag.BodyPreview)
	return true
}

func (s *tripScraper) markCloudflareBlocked(stage string, base Event, reason string, bodyPreview string) {
	s.currentScrapeNote = "被Cloudflare拦截"
	s.emit(mergeEvent(Event{
		"event":        "cloudflare_overlay_detected",
		"stage":        stage,
		"message":      firstNonEmpty(reason, "Cloudflare overlay detected; current URL must not be treated as successful"),
		"scrape_note":  s.currentScrapeNote,
		"body_preview": bodyPreview,
	}, base))
}

func classifyRiskChallenge(body string, pageURL string, scripts []string, hasSKU bool, httpStatus int) riskDiagnostics {
	lowerBody := strings.ToLower(body)
	lowerURL := strings.ToLower(pageURL)
	reason := ""
	kind := ""
	if httpStatus == 432 || strings.Contains(lowerURL, "whaleguard") || strings.Contains(lowerBody, "whaleguard") {
		reason = "risk challenge detected: whaleguard"
		kind = "whaleguard"
	} else if strings.Contains(lowerURL, "cloudflare") || strings.Contains(lowerURL, "cdn-cgi/challenge-platform") || strings.Contains(lowerBody, "cloudflare") || strings.Contains(lowerBody, "checking if the site connection is secure") || strings.Contains(lowerBody, "just a moment") || strings.Contains(body, "驗證您是人類") || strings.Contains(body, "验证您是人类") || strings.Contains(body, "正在驗證") || strings.Contains(body, "正在验证") || strings.Contains(body, "驗證失敗") || strings.Contains(body, "验证失败") || strings.Contains(body, "驗證已過期") || strings.Contains(body, "验证已过期") || strings.Contains(body, "challenge-error-text") || strings.Contains(body, "turnstile") {
		reason = "cloudflare challenge detected"
		kind = "cloudflare"
	}
	challengeMarkers := []string{
		"verify you are human",
		"captcha",
		"guestverify",
		"security verification",
		"安全验证",
		"安全驗證",
		"人机验证",
		"人機驗證",
		"滑块验证",
		"滑塊驗證",
		"访问异常",
		"訪問異常",
		"請完成驗證",
		"请完成验证",
	}
	if reason == "" {
		for _, marker := range challengeMarkers {
			if strings.Contains(lowerBody, strings.ToLower(marker)) || strings.Contains(lowerURL, strings.ToLower(marker)) {
				reason = "risk challenge detected: " + marker
				kind = "challenge"
				break
			}
		}
	}
	if reason != "" && !hasSKU {
		return riskDiagnostics{Challenge: true, Reason: reason, Kind: kind, HTTPStatus: httpStatus, BodyPreview: previewText(body, 200), Scripts: scripts}
	}
	if strings.Contains(reason, "whaleguard") {
		return riskDiagnostics{Challenge: true, Reason: reason, Kind: "whaleguard", HTTPStatus: httpStatus, BodyPreview: previewText(body, 200), Scripts: scripts}
	}
	return riskDiagnostics{Challenge: false, Reason: "", Kind: "", HTTPStatus: httpStatus, BodyPreview: previewText(body, 200), Scripts: scripts}
}

func (s *tripScraper) promoText() string {
	text := s.pageText()
	for _, line := range strings.Split(text, "\n") {
		lowered := strings.ToLower(line)
		if strings.Contains(lowered, "off") || strings.Contains(lowered, "save") || strings.Contains(line, "优惠") || strings.Contains(line, "折") || strings.Contains(line, "減") {
			runes := []rune(strings.TrimSpace(line))
			if len(runes) > 80 {
				runes = runes[:80]
			}
			return string(runes)
		}
	}
	return ""
}

func (s *tripScraper) totalPromoTexts() []string {
	values := []string{}
	_ = s.evalJSON(&values, totalPromoTextJS)
	return values
}

func (s *tripScraper) totalPromoContents() []string {
	values := []string{}
	_ = s.evalJSON(&values, `() => {
		const responses = (window.__tripCdpProductPromotionResponses || []).slice().reverse();
		for (const response of responses) {
			if (Number((response || {}).status || 0) < 200 || Number((response || {}).status || 0) >= 300) continue;
			try {
				const data = JSON.parse((response || {}).text || '{}');
				const preferences = (((data || {}).productInfos || [])[0] || {}).saleDiscountInfo?.preferences || [];
				const contents = preferences.map((item) => String((item || {}).tagDesc || '').trim().replace(/\s+/g, ' ')).filter(Boolean);
				if (contents.length) return contents;
			} catch (e) {}
		}
		return [];
	}`)
	if len(values) > 0 {
		return values
	}
	_ = s.evalJSON(&values, totalPromoContentJS)
	if len(values) == 0 {
		return s.totalPromoTexts()
	}
	return values
}

func clearRepeatedProductFields(row map[string]string, includeProductInfo bool) {
	for _, key := range []string{"厂商", "商品ID", "URL", "商品名称", "评分", "好评率", "评价数", "预定数", "月销量", "年销量", "总销量", "客户服务", "方便程度", "讯号状态", "中差评价数", "货币", "总促销标签"} {
		row[key] = ""
	}
	if includeProductInfo {
		row["商品资讯"] = ""
	}
}

func joinTotalPromoTags(values []string) string {
	seen := map[string]bool{}
	joined := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		joined = append(joined, value)
	}
	return strings.Join(joined, " | ")
}

func (s *tripScraper) skuLayerCount() int {
	var count int
	_ = s.evalJSON(&count, `() => {
		const containerSelectors = ['div[testid="act_sku_wrapper"]', 'div.style_sku_wrapper_container__Lq9RG', 'div.style_m_container__ssZE4', 'div[class*="style_sku_wrapper_container"]'];
		let container = null;
		for (const selector of containerSelectors) {
			container = document.querySelector(selector);
			if (container) break;
		}
		if (!container) return 0;
		for (const selector of ['div.sku_tab_ceil.style_m_sku_tab_ceil__D9OYQ', 'div.xtaro-xview.xt-xview.sku_tab_ceil', 'div[class*="sku_tab_ceil"]']) {
			const elements = Array.from(container.querySelectorAll(selector)).filter(el => el.offsetParent !== null);
			if (elements.length && elements.length <= 5) return elements.length;
		}
		return 0;
	}`)
	return count
}

func (s *tripScraper) layerTitle(index int) string {
	title := s.evalString(`(index) => {
		const layers = Array.from(document.querySelectorAll('div[class*="sku_tab_ceil"]')).filter(el => el.offsetParent !== null);
		const layer = layers[index];
		if (!layer) return '未知层级';
		const title = layer.querySelector('span.style_m_tab_title_text__ldPLP');
		return ((title && (title.innerText || title.textContent)) || '未知层级').trim();
	}`, index)
	if title == "" {
		return "未知层级"
	}
	return title
}

func (s *tripScraper) layerOptions(index int) []skuOption {
	var options []skuOption
	_ = s.evalJSON(&options, `(index) => {
		const layers = Array.from(document.querySelectorAll('div[class*="sku_tab_ceil"]')).filter(el => el.offsetParent !== null);
		const layer = layers[index];
		if (!layer) return [];
		const optionElements = Array.from(layer.querySelectorAll('div.m_ceil.style_m_ceil__mUygk'));
		const out = [];
		const seen = new Set();
		const isVisible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
			return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		};
		for (const el of optionElements) {
			const cls = el.getAttribute('class') || '';
			if (!cls.includes('style_select_border__AaoE0')) continue;
			if (!isVisible(el)) continue;
			const textEl = el.querySelector('span[testid^="title_saleproperty_"]');
			const text = ((textEl && (textEl.innerText || textEl.textContent)) || '').trim();
			if (!text) continue;
			const textColor = getComputedStyle(textEl || el).color;
			if (textColor.includes('172, 180, 191') || textColor.includes('206, 210, 217')) continue;
			const elementColor = getComputedStyle(el).color;
			if (elementColor.includes('206, 210, 217')) continue;
			if (seen.has(text)) continue;
			seen.add(text);
			out.push({text, layer: index + 1, id: el.id || ''});
		}
		return out;
	}`, index)
	return options
}

func (s *tripScraper) clickSKUOption(ctx context.Context, option skuOption, deselect bool) bool {
	action := "选择"
	if deselect {
		action = "取消选择"
	}
	for retry := 0; retry < 3; retry++ {
		if ctx.Err() != nil {
			return false
		}
		var prepared struct {
			OK       bool `json:"ok"`
			Selected bool `json:"selected"`
		}
		_ = s.evalJSON(&prepared, `(index, optionText, optionID, deselect) => {
		const layers = Array.from(document.querySelectorAll('div[class*="sku_tab_ceil"]')).filter(el => el.offsetParent !== null);
		const layer = layers[index];
		if (!layer) return {ok: false, selected: false};
		const isVisible = (el) => {
			if (!el) return false;
			const style = window.getComputedStyle(el);
			if (!style || style.display === 'none' || style.visibility === 'hidden') return false;
			return !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
		};
		let optionElements = Array.from(layer.querySelectorAll('div.m_ceil.style_m_ceil__mUygk'));
		if (optionID) {
			const byID = document.getElementById(optionID);
			if (byID) optionElements = [byID, ...optionElements.filter(el => el !== byID)];
		}
		for (const el of optionElements) {
			const cls = el.getAttribute('class') || '';
			if (!cls.includes('style_select_border__AaoE0')) continue;
			if (!isVisible(el)) continue;
			const textEl = el.querySelector('span[testid^="title_saleproperty_"]');
			const text = ((textEl && (textEl.innerText || textEl.textContent)) || '').trim();
			if (text !== optionText) continue;
			const textColor = getComputedStyle(textEl || el).color;
			if (textColor.includes('172, 180, 191') || textColor.includes('206, 210, 217')) continue;
			const elementColor = getComputedStyle(el).color;
			if (elementColor.includes('206, 210, 217')) continue;
			const selectedMarker = el.querySelector('[testid$="_select"]');
			const borderColor = getComputedStyle(el).borderColor || '';
			const selected = !!selectedMarker || borderColor.includes('44, 97, 254');
			if (!deselect && selected) return {ok: true, selected: true};
			if (deselect && !selected) return {ok: true, selected: false};
			el.scrollIntoView({block: 'center'});
			el.dataset.tripCdpPendingClick = '1';
			return {ok: true, selected};
		}
		return {ok: false, selected: false};
		}`, option.Layer-1, option.Text, option.ID, deselect)
		if !prepared.OK {
			if retry < 2 {
				if !sleepFixed(ctx, 200*time.Millisecond) {
					return false
				}
				continue
			}
			s.emit(Event{"event": "warning", "stage": "sku", "message": fmt.Sprintf("click layer %d option %s failed", option.Layer, option.Text)})
			s.markRestartAfterURLIfUnhealthy(fmt.Sprintf("SKU click failed while page is unhealthy: layer %d option %s", option.Layer, option.Text))
			return false
		}
		if !deselect && prepared.Selected {
			s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d already selected: %s", option.Layer, option.Text)})
			return true
		}
		if deselect && !prepared.Selected {
			s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d already deselected: %s", option.Layer, option.Text)})
			return true
		}
		if !deselect {
			s.clearBatchPromotionResponses()
		}
		if !sleepFixed(ctx, 50*time.Millisecond) {
			return false
		}
		var clicked bool
		_ = s.evalJSON(&clicked, `(index, optionText, optionID, useMouseEvents, deselect) => {
			const layers = Array.from(document.querySelectorAll('div[class*="sku_tab_ceil"]')).filter(el => el.offsetParent !== null);
			const layer = layers[index];
			if (!layer) return false;
			let optionElements = Array.from(layer.querySelectorAll('div.m_ceil.style_m_ceil__mUygk'));
			if (optionID) {
				const byID = document.getElementById(optionID);
				if (byID) optionElements = [byID, ...optionElements.filter(el => el !== byID)];
			}
			for (const el of optionElements) {
				const textEl = el.querySelector('span[testid^="title_saleproperty_"]');
				const text = ((textEl && (textEl.innerText || textEl.textContent)) || '').trim();
				if (text !== optionText) continue;
				const borderColor = getComputedStyle(el).borderColor || '';
				const selected = !!el.querySelector('[testid$="_select"]') || borderColor.includes('44, 97, 254');
				if (!deselect && selected) return true;
				if (deselect && !selected) return true;
				if (useMouseEvents) {
					el.dispatchEvent(new MouseEvent('mousedown', {bubbles: true, cancelable: true, view: window}));
					el.dispatchEvent(new MouseEvent('mouseup', {bubbles: true, cancelable: true, view: window}));
				}
				el.click();
				return true;
			}
			return false;
		}`, option.Layer-1, option.Text, option.ID, retry > 0, deselect)
		if clicked {
			if deselect {
				if s.request.DelayOptions.Profile != "fast" {
					if !sleepFixed(ctx, 100*time.Millisecond) {
						return false
					}
				}
				s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d %s: %s", option.Layer, action, option.Text)})
				return true
			}
			if !sleepDelay(ctx, s.emit, "sku", "sku_click", s.request.DelayOptions.SKUClick) {
				return false
			}
			var selected bool
			_ = s.evalJSON(&selected, `(index, optionText, optionID) => {
				const layers = Array.from(document.querySelectorAll('div[class*="sku_tab_ceil"]')).filter(el => el.offsetParent !== null);
				const layer = layers[index];
				if (!layer) return false;
				let optionElements = Array.from(layer.querySelectorAll('div.m_ceil.style_m_ceil__mUygk'));
				if (optionID) {
					const byID = document.getElementById(optionID);
					if (byID) optionElements = [byID, ...optionElements.filter(el => el !== byID)];
				}
				for (const el of optionElements) {
					const textEl = el.querySelector('span[testid^="title_saleproperty_"]');
					const text = ((textEl && (textEl.innerText || textEl.textContent)) || '').trim();
					if (text !== optionText) continue;
					const borderColor = getComputedStyle(el).borderColor || '';
					return !!el.querySelector('[testid$="_select"]') || borderColor.includes('44, 97, 254');
				}
				return false;
			}`, option.Layer-1, option.Text, option.ID)
			if selected {
				s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("layer %d %s: %s", option.Layer, action, option.Text)})
				return true
			}
		}
		if retry < 2 {
			s.emit(Event{"event": "log", "stage": "sku", "message": fmt.Sprintf("click layer %d option %s failed; retry %d/3", option.Layer, option.Text, retry+1)})
			if !sleepFixed(ctx, 200*time.Millisecond) {
				return false
			}
		}
	}
	s.emit(Event{"event": "warning", "stage": "sku", "message": fmt.Sprintf("click layer %d option %s failed", option.Layer, option.Text)})
	s.markRestartAfterURLIfUnhealthy(fmt.Sprintf("SKU click failed while page is unhealthy: layer %d option %s", option.Layer, option.Text))
	return false
}

func (s *tripScraper) markRestartAfterURLIfUnhealthy(reason string) {
	if isProductPageReady(s.currentPageSnapshot()) {
		return
	}
	s.restartAfterURL = true
	s.restartReason = reason
}

func (s *tripScraper) currentPrice(ctx context.Context, maxAttempts int) string {
	market := s.request.Market
	selectors := []string{
		`#select_quantity div[class*="style_m_select_quantity_container__"] div div[class*="style_m_select_choice_option_wrapper__"] div[class*="style_m_people_price__"] div div span span`,
		`#select_quantity > div.xtaro-xview > div > div.xtaro-xview > div.xtaro-xview > div > div > span > span`,
		`div[class*="default_new_title_foot_warp"] div[class*="default_new_title_foot_warp_right"] div div div span`,
		fmt.Sprintf(`span[currency="%s"][testid*="price"]`, market.Currency),
		`span.price_m_price_price__VrAgh`,
		fmt.Sprintf(`div.price_m_price__FmWsJ span[currency="%s"]`, market.Currency),
		`span[testid="undefined-price"]`,
		fmt.Sprintf(`span[currency="%s"]`, market.Currency),
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return "价格未找到"
		}
		for _, selector := range selectors {
			text := s.evalString(`(selector, currency, pricePrefixes) => {
				for (const el of document.querySelectorAll(selector)) {
					if (el.offsetParent === null) continue;
					const text = (el.innerText || el.textContent || '').trim();
					const hasPrefix = pricePrefixes.some(prefix => text.startsWith(prefix));
					const hasCurrency = String(el.getAttribute('currency') || '').toUpperCase() === currency;
					if (text && /[0-9,]+(?:\.\d+)?/.test(text) && (hasCurrency || hasPrefix)) return text;
				}
				return '';
			}`, selector, market.Currency, market.PricePrefixes)
			if text != "" {
				return text
			}
		}
		text := s.evalString(`(pricePrefixes) => {
			const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
			let node;
			while ((node = walker.nextNode())) {
				const text = (node.nodeValue || '').trim();
				if (pricePrefixes.some(prefix => text.startsWith(prefix)) && /[0-9,]+(?:\.\d+)?/.test(text)) return text;
			}
			return '';
		}`, market.PricePrefixes)
		if text != "" {
			return text
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "价格未找到"
}

func (s *tripScraper) currentPriceBundle() priceBundle {
	market := s.request.Market
	displaySelectors := []string{
		`#select_quantity div[class*="style_m_select_quantity_container__"] div div[class*="style_m_select_choice_option_wrapper__"] div[class*="style_m_people_price__"] div div span span`,
		`#select_quantity > div.xtaro-xview > div > div.xtaro-xview > div.xtaro-xview > div > div > span > span`,
		`div[class*="default_new_title_foot_warp"] div[class*="default_new_title_foot_warp_right"] div div div span`,
		fmt.Sprintf(`span[currency="%s"][testid*="price"]`, market.Currency),
		`span.price_m_price_price__VrAgh`,
		fmt.Sprintf(`div.price_m_price__FmWsJ span[currency="%s"]`, market.Currency),
		`span[testid="undefined-price"]`,
		fmt.Sprintf(`span[currency="%s"]`, market.Currency),
	}
	var bundle priceBundle
	_ = s.evalJSON(&bundle, currentPriceBundleJS, displaySelectors, s.request.ExtractRealPrice, market.Currency, market.PricePrefixes)
	return bundle
}

func (s *tripScraper) ensurePriceFields(row map[string]string) {
	if s.request.RatingOnly {
		return
	}
	if _, ok := row["SKU层级1"]; !ok {
		row["SKU层级1"] = ""
	}
	for _, key := range []string{"货币", "外显价格", "促销标签"} {
		if _, ok := row[key]; !ok {
			row[key] = ""
		}
	}
	if s.request.ExtractRealPrice {
		if _, ok := row["实价"]; !ok {
			row["实价"] = row["外显价格"]
		}
	}
}

func (s *tripScraper) errorRow(item ScrapeItem, message string) map[string]string {
	row := map[string]string{
		"厂商":    item.Vendor,
		"商品ID":  productID(item.URL),
		"URL":   item.URL,
		"商品名称":  "",
		"评分":    "处理错误: " + message,
		"评价数":   "",
		"预定数":   "",
		"月销量":   "",
		"年销量":   "",
		"总销量":   "",
		"客户服务":  "",
		"方便程度":  "",
		"讯号状态":  "",
		"中差评价数": "",
	}
	if s.request.ExtractProductInfo {
		row["商品资讯"] = ""
	}
	if !s.request.RatingOnly {
		row["SKU层级1"] = ""
		row["货币"] = ""
		row["外显价格"] = ""
		row["促销标签"] = ""
		if s.request.ExtractRealPrice {
			row["实价"] = ""
		}
	}
	s.applyCurrentScrapeNote(row)
	s.ensurePriceFields(row)
	return row
}

func (s *tripScraper) riskBlockedRow(item ScrapeItem, message string) map[string]string {
	row := s.errorRow(item, message)
	if !s.request.RatingOnly {
		row["价格状态"] = priceStatusRiskBlocked
		row["价格来源"] = priceSourceBatchPromotion
		row["原始捕获价格"] = ""
	}
	if message = strings.TrimSpace(message); message != "" {
		if existing := strings.TrimSpace(row["抓取备注"]); existing != "" && !strings.Contains(existing, message) {
			row["抓取备注"] = existing + "；" + message
		} else if existing == "" {
			row["抓取备注"] = message
		}
	}
	return row
}

func (s *tripScraper) applyCurrentScrapeNote(row map[string]string) {
	note := strings.TrimSpace(s.currentScrapeNote)
	if note == "" || row == nil {
		return
	}
	if !s.request.RatingOnly && strings.Contains(strings.ToLower(note), "cloudflare") {
		row["价格状态"] = priceStatusRiskBlocked
		row["价格来源"] = priceSourceBatchPromotion
		for _, key := range []string{"货币", "外显价格", "实价", "促销标签", "资源ID", "接口ItemID", "促销命中", "原始捕获价格"} {
			row[key] = ""
		}
	}
	if existing := strings.TrimSpace(row["抓取备注"]); existing != "" {
		if !strings.Contains(existing, note) {
			row["抓取备注"] = existing + "；" + note
		}
		return
	}
	row["抓取备注"] = note
}

func (s *tripScraper) applyCurrentScrapeNoteToRows(rows []map[string]string) {
	if strings.TrimSpace(s.currentScrapeNote) == "" {
		return
	}
	for _, row := range rows {
		s.applyCurrentScrapeNote(row)
	}
}

func (s *tripScraper) evalString(js string, args ...any) string {
	var out string
	if err := s.evalJSON(&out, js, args...); err != nil {
		return ""
	}
	return out
}

func (s *tripScraper) readMarketObservation() (marketObservation, error) {
	var observed marketObservation
	if err := s.evalJSON(&observed, readMarketObservationJS); err != nil {
		return marketObservation{}, err
	}
	observed.Host = strings.ToLower(strings.TrimSpace(observed.Host))
	observed.SiteID = strings.ToUpper(strings.TrimSpace(observed.SiteID))
	observed.Locale = normalizeLocale(observed.Locale)
	observed.Currency = strings.ToUpper(strings.TrimSpace(observed.Currency))
	return observed, nil
}

func (s *tripScraper) evalJSON(out any, js string, args ...any) error {
	if s.page == nil {
		return fmt.Errorf("browser is not started")
	}
	res, err := s.page.Evaluate(rod.Eval(js, args...))
	if err != nil {
		return err
	}
	return res.Value.Unmarshal(out)
}

func mergeEvent(a Event, b Event) Event {
	out := Event{}
	for key, value := range b {
		out[key] = value
	}
	for key, value := range a {
		out[key] = value
	}
	return out
}

func cloneRow(row map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range row {
		out[key] = value
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func previewText(value string, limit int) string {
	value = compactText(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func formatPriceNumber(value float64) string {
	if value < 0 {
		value = 0
	}
	text := strconv.FormatFloat(value, 'f', 2, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	if text == "" {
		return "0"
	}
	return text
}

func productID(url string) string {
	if match := regexp.MustCompile(`/detail/(\d+)`).FindStringSubmatch(url); len(match) > 1 {
		return match[1]
	}
	if match := regexp.MustCompile(`(\d{5,})`).FindStringSubmatch(url); len(match) > 1 {
		return match[1]
	}
	return ""
}

func bookingCount(text string) string {
	return firstRegex(text, `([\d,]+)\s*(?:次預訂|次预订|booked|Bookings?)`)
}

func reviewCount(text string) string {
	return firstRegex(text, `([\d,]+)\s*(?:則評價|条评价|reviews?)`)
}

func badReviewCount(text string) string {
	return firstRegex(text, `(?:中差|中性評價及負面評價|中性评价及负面评价|negative|bad)[^\d]{0,20}([\d,]+)`)
}

func scoreNear(text string, label string) string {
	return firstRegex(text, regexp.QuoteMeta(label)+`.{0,30}?(\d+(?:\.\d+)?)`)
}

func firstRegex(text string, pattern string) string {
	re := regexp.MustCompile(`(?is)` + pattern)
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return strings.ReplaceAll(match[1], ",", "")
}

func productInfo(text string) string {
	for _, label := range []string{"Product Info", "商品資訊", "商品资讯", "Details"} {
		index := strings.Index(text, label)
		if index >= 0 {
			runes := []rune(text[index:])
			if len(runes) > 500 {
				runes = runes[:500]
			}
			return strings.Join(strings.Fields(string(runes)), " ")
		}
	}
	return ""
}

func numericText(value string) string {
	return firstRegex(value, `([0-9,]+(?:\.\d+)?)`)
}

func scoreText(value string) string {
	return firstRegex(value, `(\d+(?:\.\d+)?)`)
}

func compactText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func detectChromePath() string {
	candidates := []string{}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			filepath.Join(home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
			"google-chrome",
			"chrome",
		}
	case "windows":
		candidates = []string{
			os.ExpandEnv(`${ProgramFiles}\Google\Chrome\Application\chrome.exe`),
			os.ExpandEnv(`${ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe`),
			os.ExpandEnv(`${LocalAppData}\Google\Chrome\Application\chrome.exe`),
		}
	default:
		candidates = []string{"google-chrome", "chromium", "chromium-browser"}
	}
	for _, candidate := range candidates {
		path := candidate
		if !strings.ContainsAny(candidate, `/\`) {
			if found, err := exec.LookPath(candidate); err == nil {
				path = found
			}
		}
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			return path
		}
	}
	return ""
}

func chromeMajorVersion(chromePath string) string {
	if runtime.GOOS == "windows" {
		return chromeMajorVersionFromInstallDir(chromePath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, chromePath, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	match := regexp.MustCompile(`(\d+)\.`).FindStringSubmatch(string(out))
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func chromeMajorVersionFromInstallDir(chromePath string) string {
	entries, err := os.ReadDir(filepath.Dir(chromePath))
	if err != nil {
		return ""
	}
	versionRe := regexp.MustCompile(`^(\d+)\.\d+\.\d+\.\d+$`)
	versionMain := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		match := versionRe.FindStringSubmatch(entry.Name())
		if len(match) < 2 {
			continue
		}
		if versionMain == "" || compareVersionStrings(entry.Name(), versionMain) > 0 {
			versionMain = entry.Name()
		}
	}
	if versionMain == "" {
		return ""
	}
	return strings.SplitN(versionMain, ".", 2)[0]
}

func compareVersionStrings(left string, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for i := 0; i < len(leftParts) || i < len(rightParts); i++ {
		leftValue := 0
		rightValue := 0
		if i < len(leftParts) {
			leftValue, _ = strconv.Atoi(leftParts[i])
		}
		if i < len(rightParts) {
			rightValue, _ = strconv.Atoi(rightParts[i])
		}
		if leftValue > rightValue {
			return 1
		}
		if leftValue < rightValue {
			return -1
		}
	}
	return 0
}

func managedBrowserRSSMB(userDataDir string) float64 {
	if userDataDir == "" || runtime.GOOS == "windows" {
		return 0
	}
	cmd := exec.Command("ps", "-axo", "rss=,command=")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	totalKB := 0
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, userDataDir) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		rss, _ := strconv.Atoi(parts[0])
		totalKB += rss
	}
	return float64(totalKB) / 1024
}

func elapsedMS(start time.Time) int {
	return int(time.Since(start).Milliseconds())
}
