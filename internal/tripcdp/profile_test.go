package tripcdp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestChromeArgumentsIncludeUCSubprocessFlags(t *testing.T) {
	market, err := resolveMarketContext("hk", "", "")
	if err != nil {
		t.Fatal(err)
	}
	args := chromeArguments("/tmp/profile", true, 45678, market.AcceptLanguage)
	for _, want := range []string{
		"--user-data-dir=/tmp/profile",
		"--remote-debugging-host=127.0.0.1",
		"--remote-debugging-port=45678",
		"--lang=zh-HK",
		"--no-sandbox",
		"--test-type",
		"--start-maximized",
		"--log-level=0",
		"--headless=new",
	} {
		if !slices.Contains(args, want) {
			t.Fatalf("chrome arguments missing %q: %#v", want, args)
		}
	}
	foundLang := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "--lang=") {
			foundLang = true
			break
		}
	}
	if !foundLang {
		t.Fatalf("chrome arguments missing --lang: %#v", args)
	}
	for _, blocked := range []string{
		"--disable-extensions",
		"--disable-application-cache",
		"--aggressive-cache-discard",
		"--disk-cache-size=",
		"--media-cache-size=",
		"--js-flags=",
	} {
		for _, arg := range args {
			if strings.HasPrefix(arg, blocked) {
				t.Fatalf("chrome arguments should not include %q: %#v", blocked, args)
			}
		}
	}
}

func TestDefaultChromeUserDataDir(t *testing.T) {
	dir := defaultChromeUserDataDir()
	if dir == "" {
		t.Fatal("default Chrome user data dir is empty")
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(dir, filepath.Join("Library", "Application Support", "Google", "Chrome")) {
			t.Fatalf("unexpected darwin Chrome dir: %s", dir)
		}
	case "windows":
		if !strings.Contains(dir, filepath.Join("Google", "Chrome", "User Data")) {
			t.Fatalf("unexpected windows Chrome dir: %s", dir)
		}
	default:
		if !strings.Contains(dir, filepath.Join(".config", "google-chrome")) {
			t.Fatalf("unexpected linux Chrome dir: %s", dir)
		}
	}
}

func TestChromeMajorVersionFromInstallDir(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "chrome.exe"), []byte("chrome"))
	if err := os.Mkdir(filepath.Join(dir, "147.0.1.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "148.0.7778.168"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "not-a-version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := chromeMajorVersionFromInstallDir(filepath.Join(dir, "chrome.exe")); got != "148" {
		t.Fatalf("chrome major = %q, want 148", got)
	}
}

func TestCopyChromeProfileSkipsLocksAndKeepsCoreFiles(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "Chrome")
	dst := filepath.Join(root, "worker-profile")
	mustWrite(t, filepath.Join(src, "Local State"), []byte(`{"ok":true}`))
	mustWrite(t, filepath.Join(src, "SingletonLock"), []byte("lock"))
	mustWrite(t, filepath.Join(src, "Default", "Cookies"), []byte("cookies"))
	mustWrite(t, filepath.Join(src, "Default", "Network", "Cookies"), []byte("network-cookies"))
	mustWrite(t, filepath.Join(src, "Default", "Local Storage", "leveldb", "000003.log"), []byte("local-storage"))
	mustWrite(t, filepath.Join(src, "Default", "Session Storage", "000003.log"), []byte("session-storage"))
	mustWrite(t, filepath.Join(src, "Default", "IndexedDB", "https_trip.com_0.indexeddb.leveldb", "000003.log"), []byte("indexeddb"))
	mustWrite(t, filepath.Join(src, "Default", "Login Data"), []byte("login-data"))
	mustWrite(t, filepath.Join(src, "Default", "Cache", "Cache_Data", "data_0"), []byte("cache"))
	mustWrite(t, filepath.Join(src, "Default", "Code Cache", "js", "data_0"), []byte("code-cache"))
	mustWrite(t, filepath.Join(src, "Default", "Extensions", "extension", "manifest.json"), []byte("extension"))
	mustWrite(t, filepath.Join(src, "component_crx_cache", "metadata.json"), []byte("component-cache"))
	mustWrite(t, filepath.Join(src, "Default", "Cookies-wal"), []byte("wal"))
	mustWrite(t, filepath.Join(src, "Crashpad", "x"), []byte("crash"))

	if err := copyChromeProfile(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Local State")); err != nil {
		t.Fatalf("Local State was not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Default", "Cookies")); err != nil {
		t.Fatalf("Default/Cookies was not copied: %v", err)
	}
	for _, kept := range []string{
		filepath.Join("Default", "Network", "Cookies"),
		filepath.Join("Default", "Local Storage", "leveldb", "000003.log"),
		filepath.Join("Default", "Session Storage", "000003.log"),
		filepath.Join("Default", "IndexedDB", "https_trip.com_0.indexeddb.leveldb", "000003.log"),
		filepath.Join("Default", "Login Data"),
	} {
		if _, err := os.Stat(filepath.Join(dst, kept)); err != nil {
			t.Fatalf("%s was not copied: %v", kept, err)
		}
	}
	for _, skipped := range []string{
		"SingletonLock",
		filepath.Join("Default", "Cookies-wal"),
		filepath.Join("Crashpad", "x"),
		filepath.Join("Default", "Cache", "Cache_Data", "data_0"),
		filepath.Join("Default", "Code Cache", "js", "data_0"),
		filepath.Join("Default", "Extensions", "extension", "manifest.json"),
		filepath.Join("component_crx_cache", "metadata.json"),
	} {
		if _, err := os.Stat(filepath.Join(dst, skipped)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been skipped, stat err=%v", skipped, err)
		}
	}
}

func TestFixChromeExitType(t *testing.T) {
	dir := t.TempDir()
	preferencesPath := filepath.Join(dir, "Default", "Preferences")
	mustWrite(t, preferencesPath, []byte(`{"profile":{"exit_type":"Crashed","other":true}}`))
	if err := fixChromeExitType(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(preferencesPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	profile := payload["profile"].(map[string]any)
	if profile["exit_type"] != nil {
		t.Fatalf("exit_type = %#v, want nil", profile["exit_type"])
	}
	if profile["other"] != true {
		t.Fatalf("other profile setting was not preserved: %#v", profile)
	}
}

func mustWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
