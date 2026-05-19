from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from selenium.common.exceptions import TimeoutException, WebDriverException


class SessionStore:
    def __init__(self, path: str | None) -> None:
        self.path = Path(path).expanduser() if path else None

    def load(self, driver, base_url: str = "https://www.trip.com/", timeout_seconds: int = 8) -> bool:
        if not self.path or not self.path.exists():
            return False
        data = json.loads(self.path.read_text(encoding="utf-8"))
        try:
            driver.set_page_load_timeout(timeout_seconds)
        except Exception:
            pass
        try:
            driver.get(base_url)
        except TimeoutException:
            self._stop_loading(driver)
        except WebDriverException:
            return False
        loaded = False
        for cookie in data.get("cookies", []):
            try:
                cookie = dict(cookie)
                cookie.pop("sameSite", None)
                driver.add_cookie(cookie)
                loaded = True
            except Exception:
                continue
        for key, value in data.get("local_storage", {}).items():
            try:
                driver.execute_script("window.localStorage.setItem(arguments[0], arguments[1]);", key, value)
                loaded = True
            except Exception:
                continue
        for key, value in data.get("session_storage", {}).items():
            try:
                driver.execute_script("window.sessionStorage.setItem(arguments[0], arguments[1]);", key, value)
                loaded = True
            except Exception:
                continue
        if loaded:
            try:
                driver.refresh()
            except TimeoutException:
                self._stop_loading(driver)
            except WebDriverException:
                pass
        return loaded

    def _stop_loading(self, driver) -> None:
        try:
            driver.execute_script("window.stop();")
        except Exception:
            pass

    def save(self, driver) -> bool:
        if not self.path:
            return False
        self.path.parent.mkdir(parents=True, exist_ok=True)
        data: dict[str, Any] = {
            "cookies": [],
            "local_storage": {},
            "session_storage": {},
        }
        try:
            data["cookies"] = driver.get_cookies()
        except Exception:
            pass
        try:
            data["local_storage"] = driver.execute_script(
                """
                const out = {};
                for (let i = 0; i < window.localStorage.length; i++) {
                  const key = window.localStorage.key(i);
                  out[key] = window.localStorage.getItem(key);
                }
                return out;
                """
            )
        except Exception:
            pass
        try:
            data["session_storage"] = driver.execute_script(
                """
                const out = {};
                for (let i = 0; i < window.sessionStorage.length; i++) {
                  const key = window.sessionStorage.key(i);
                  out[key] = window.sessionStorage.getItem(key);
                }
                return out;
                """
            )
        except Exception:
            pass
        self.path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
