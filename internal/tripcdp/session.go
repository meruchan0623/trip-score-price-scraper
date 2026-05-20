package tripcdp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type sessionData struct {
	Cookies        []map[string]any  `json:"cookies"`
	LocalStorage   map[string]string `json:"local_storage"`
	SessionStorage map[string]string `json:"session_storage"`
}

type sessionLoadResult struct {
	Loaded      bool
	Data        sessionData
	CookieCount int
	HasStorage  bool
}

func loadSession(ctx context.Context, page *rod.Page, path string, targetURL string) (result sessionLoadResult, err error) {
	defer func() {
		if panicValue := recover(); panicValue != nil {
			result.Loaded = false
			err = fmt.Errorf("session load panic: %v", panicValue)
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if path == "" {
		return result, nil
	}
	raw, err := os.ReadFile(expandPath(path))
	if err != nil {
		return result, nil
	}
	var data sessionData
	if err := json.Unmarshal(raw, &data); err != nil {
		return result, err
	}
	result.Data = data
	result.HasStorage = len(data.LocalStorage) > 0 || len(data.SessionStorage) > 0
	if len(data.Cookies) == 0 && len(data.LocalStorage) == 0 && len(data.SessionStorage) == 0 {
		return result, nil
	}
	if page == nil {
		return result, fmt.Errorf("session load requires a browser page")
	}
	for _, cookie := range data.Cookies {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		request, ok := sessionCookieRequest(cookie, targetURL)
		if !ok {
			continue
		}
		if err := withPageTimeout(ctx, page, 3*time.Second, func(p *rod.Page) error {
			_, err := request.Call(p)
			return err
		}); err == nil {
			result.Loaded = true
			result.CookieCount++
		}
	}
	return result, nil
}

func applySessionStorage(ctx context.Context, page *rod.Page, data sessionData) (loaded bool, err error) {
	defer func() {
		if panicValue := recover(); panicValue != nil {
			loaded = false
			err = fmt.Errorf("session storage load panic: %v", panicValue)
		}
	}()
	for key, value := range data.LocalStorage {
		if err := ctx.Err(); err != nil {
			return loaded, err
		}
		if setBrowserStorage(ctx, page, "local", key, value) {
			loaded = true
		}
	}
	for key, value := range data.SessionStorage {
		if err := ctx.Err(); err != nil {
			return loaded, err
		}
		if setBrowserStorage(ctx, page, "session", key, value) {
			loaded = true
		}
	}
	return loaded, nil
}

func saveSession(page *rod.Page, path string) (saved bool) {
	defer func() {
		if recover() != nil {
			saved = false
		}
	}()
	if path == "" {
		return false
	}
	data := sessionData{
		Cookies:        []map[string]any{},
		LocalStorage:   map[string]string{},
		SessionStorage: map[string]string{},
	}
	if page == nil {
		return false
	}
	var cookies *proto.NetworkGetAllCookiesResult
	if err := withPageTimeout(context.Background(), page, 5*time.Second, func(p *rod.Page) error {
		var err error
		cookies, err = proto.NetworkGetAllCookies{}.Call(p)
		return err
	}); err == nil && cookies != nil {
		for _, cookie := range cookies.Cookies {
			entry := map[string]any{
				"name":     cookie.Name,
				"value":    cookie.Value,
				"domain":   cookie.Domain,
				"path":     cookie.Path,
				"secure":   cookie.Secure,
				"httpOnly": cookie.HTTPOnly,
			}
			if cookie.SameSite != "" {
				entry["sameSite"] = string(cookie.SameSite)
			}
			if cookie.Expires != 0 {
				entry["expires"] = float64(cookie.Expires)
			}
			data.Cookies = append(data.Cookies, entry)
		}
	}
	var storage struct {
		Local   map[string]string `json:"local"`
		Session map[string]string `json:"session"`
	}
	_ = withPageTimeout(context.Background(), page, 5*time.Second, func(p *rod.Page) error {
		res, err := p.Evaluate(rod.Eval(`() => {
			const read = (storage) => {
				const out = {};
				for (let i = 0; i < storage.length; i++) {
					const key = storage.key(i);
					out[key] = storage.getItem(key);
				}
				return out;
			};
			return {local: read(localStorage), session: read(sessionStorage)};
		}`))
		if err != nil {
			return err
		}
		return res.Value.Unmarshal(&storage)
	})
	if storage.Local != nil {
		data.LocalStorage = storage.Local
	}
	if storage.Session != nil {
		data.SessionStorage = storage.Session
	}
	if err := ensureParent(path); err != nil {
		return false
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return false
	}
	return os.WriteFile(expandPath(path), raw, 0o644) == nil
}

func withPageTimeout(ctx context.Context, page *rod.Page, timeout time.Duration, fn func(*rod.Page) error) (err error) {
	if page == nil {
		return fmt.Errorf("browser page is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timedPage := page.Context(ctx).Timeout(timeout)
	defer func() {
		if panicValue := recover(); panicValue != nil {
			err = fmt.Errorf("browser page operation panic: %v", panicValue)
		}
		func() {
			defer func() { _ = recover() }()
			_ = timedPage.CancelTimeout()
		}()
	}()
	return fn(timedPage)
}

func stopPageLoad(page *rod.Page) {
	_ = withPageTimeout(context.Background(), page, time.Second, func(p *rod.Page) error {
		_, err := p.Evaluate(rod.Eval(`() => window.stop()`))
		return err
	})
}

func setBrowserStorage(ctx context.Context, page *rod.Page, kind string, key string, value string) bool {
	err := withPageTimeout(ctx, page, 2*time.Second, func(p *rod.Page) error {
		_, err := p.Evaluate(rod.Eval(`(kind, key, value) => {
			const storage = kind === 'session' ? window.sessionStorage : window.localStorage;
			storage.setItem(key, value);
		}`, kind, key, value))
		return err
	})
	return err == nil
}

func sessionCookieRequest(cookie map[string]any, targetURL string) (proto.NetworkSetCookie, bool) {
	name, _ := cookie["name"].(string)
	value, _ := cookie["value"].(string)
	if name == "" {
		return proto.NetworkSetCookie{}, false
	}
	request := proto.NetworkSetCookie{Name: name, Value: value}
	if cookieURL, ok := cookie["url"].(string); ok {
		request.URL = cookieURL
	}
	if domain, ok := cookie["domain"].(string); ok {
		request.Domain = domain
	}
	if cookiePath, ok := cookie["path"].(string); ok {
		request.Path = cookiePath
	}
	if request.Path == "" {
		request.Path = "/"
	}
	if request.URL == "" && request.Domain == "" {
		request.URL = targetOrigin(targetURL)
		if request.URL == "" {
			request.URL = targetURL
		}
	}
	if secure, ok := cookie["secure"].(bool); ok {
		request.Secure = secure
	}
	if httpOnly, ok := cookie["httpOnly"].(bool); ok {
		request.HTTPOnly = httpOnly
	}
	if sameSite, ok := cookie["sameSite"].(string); ok {
		switch sameSite {
		case string(proto.NetworkCookieSameSiteStrict):
			request.SameSite = proto.NetworkCookieSameSiteStrict
		case string(proto.NetworkCookieSameSiteLax):
			request.SameSite = proto.NetworkCookieSameSiteLax
		case string(proto.NetworkCookieSameSiteNone):
			request.SameSite = proto.NetworkCookieSameSiteNone
		}
	}
	if expires, ok := numericCookieValue(cookie["expires"]); ok {
		request.Expires = proto.TimeSinceEpoch(expires)
	}
	return request, true
}

func numericCookieValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
