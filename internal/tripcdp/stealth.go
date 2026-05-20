package tripcdp

import (
	"context"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/ysmood/gson"
)

func ucStealthScript() string {
	return `
(() => {
  const patchNavigatorWebdriver = () => {
    try {
      Object.defineProperty(window, "navigator", {
        value: new Proxy(navigator, {
          has: (target, key) => (key === "webdriver" ? false : key in target),
          get: (target, key) => {
            if (key === "webdriver") return false;
            const value = target[key];
            return typeof value === "function" ? value.bind(target) : value;
          },
        }),
        configurable: true,
      });
    } catch (error) {
      try {
        Object.defineProperty(navigator, "webdriver", { get: () => false, configurable: true });
      } catch (_) {}
    }
  };

  patchNavigatorWebdriver();
  try { Object.defineProperty(navigator, "maxTouchPoints", { get: () => 1, configurable: true }); } catch (_) {}
  try {
    if (navigator.connection) {
      Object.defineProperty(navigator.connection, "rtt", { get: () => 100, configurable: true });
    }
  } catch (_) {}

  if (!window.chrome) window.chrome = {};
  if (!window.chrome.app) {
    window.chrome.app = {
      isInstalled: false,
      InstallState: { DISABLED: "disabled", INSTALLED: "installed", NOT_INSTALLED: "not_installed" },
      RunningState: { CANNOT_RUN: "cannot_run", READY_TO_RUN: "ready_to_run", RUNNING: "running" },
    };
  }
  if (!window.chrome.runtime) {
    window.chrome.runtime = {
      OnInstalledReason: { CHROME_UPDATE: "chrome_update", INSTALL: "install", SHARED_MODULE_UPDATE: "shared_module_update", UPDATE: "update" },
      OnRestartRequiredReason: { APP_UPDATE: "app_update", OS_UPDATE: "os_update", PERIODIC: "periodic" },
      PlatformArch: { ARM: "arm", ARM64: "arm64", MIPS: "mips", MIPS64: "mips64", X86_32: "x86-32", X86_64: "x86-64" },
      PlatformNaclArch: { ARM: "arm", MIPS: "mips", MIPS64: "mips64", X86_32: "x86-32", X86_64: "x86-64" },
      PlatformOs: { ANDROID: "android", CROS: "cros", LINUX: "linux", MAC: "mac", OPENBSD: "openbsd", WIN: "win" },
      RequestUpdateCheckStatus: { NO_UPDATE: "no_update", THROTTLED: "throttled", UPDATE_AVAILABLE: "update_available" },
    };
  }

  if (!window.Notification) window.Notification = { permission: "denied" };
  try {
    const originalQuery = window.navigator.permissions && window.navigator.permissions.query;
    if (originalQuery) {
      window.navigator.permissions.__proto__.query = function(parameters) {
        return parameters && parameters.name === "notifications"
          ? Promise.resolve({ state: window.Notification.permission })
          : originalQuery.call(this, parameters);
      };
    }
  } catch (_) {}

  try {
    const oldCall = Function.prototype.call;
    function call() { return oldCall.apply(this, arguments); }
    Function.prototype.call = call;
    const nativeToStringFunctionString = Error.toString().replace(/Error/g, "toString");
    const oldToString = Function.prototype.toString;
    function functionToString() {
      if (this === window.navigator.permissions.query) return "function query() { [native code] }";
      if (this === functionToString) return nativeToStringFunctionString;
      return oldCall.call(oldToString, this);
    }
    Function.prototype.toString = functionToString;
  } catch (_) {}
})();
`
}

func applyUCStealth(ctx context.Context, page *rod.Page, acceptLanguage string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if page == nil {
		return nil
	}
	acceptLanguage = chromeLanguageOverride(acceptLanguage)
	ua := ""
	if value, err := page.Context(ctx).Evaluate(rod.Eval(`() => navigator.userAgent`)); err == nil {
		ua = strings.ReplaceAll(value.Value.Str(), "Headless", "")
	}
	if strings.TrimSpace(ua) != "" {
		_ = proto.NetworkSetUserAgentOverride{
			UserAgent:      ua,
			AcceptLanguage: acceptLanguage,
		}.Call(page)
	}
	_ = proto.NetworkSetExtraHTTPHeaders{
		Headers: proto.NetworkHeaders{"Accept-Language": gson.New(acceptLanguage)},
	}.Call(page)
	_, err := proto.PageAddScriptToEvaluateOnNewDocument{
		Source:         ucStealthScript(),
		RunImmediately: true,
	}.Call(page)
	return err
}
