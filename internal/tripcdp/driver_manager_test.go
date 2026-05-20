package tripcdp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPatchChromeDriverBinaryReplacesCDCBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chromedriver")
	raw := []byte(`prefix {window.cdc_abc = "detected"; window.cdc_def = "detected"; window.cdc_ghi = "detected";} suffix`)
	if err := os.WriteFile(path, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := patchChromeDriverBinary(path); err != nil {
		t.Fatal(err)
	}
	patched, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !isPatchedChromeDriver(path) {
		t.Fatalf("expected patched marker in %q", string(patched))
	}
	if string(patched) == string(raw) {
		t.Fatal("binary was not changed")
	}
}

func TestFindCachedPatchedDriverReusesNewestPatchedBinary(t *testing.T) {
	dir := t.TempDir()
	oldPath := driverCachePath(dir, "147")
	newPath := driverCachePath(dir, "148")
	plainPath := filepath.Join(dir, "plain_chromedriver")
	fakeDriver := fakeChromeDriverScript("148.0.0.0")
	if err := os.WriteFile(oldPath, []byte(fakeChromeDriverScript("147.0.0.0")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(fakeDriver), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plainPath, []byte("plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-time.Hour)
	newTime := time.Now()
	_ = os.Chtimes(oldPath, oldTime, oldTime)
	_ = os.Chtimes(newPath, newTime, newTime)

	got := findCachedPatchedDriver(dir, "148")
	if got != newPath {
		t.Fatalf("cached driver = %q, want %q", got, newPath)
	}
	if got := findCachedPatchedDriver(dir, "149"); got != "" {
		t.Fatalf("cached driver for mismatched major = %q, want empty", got)
	}
}

func TestDriverCachePathIncludesChromeMajor(t *testing.T) {
	path := driverCachePath("/tmp/cache", "148")
	if filepath.Base(path) != "undetected_chromedriver_148" {
		t.Fatalf("cache path = %s", path)
	}
}

func TestResolveLegacyChromeDriverDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/LATEST_RELEASE_114" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, "114.0.5735.90")
	}))
	defer server.Close()

	version, url, err := resolveLegacyChromeDriverDownloadFromBase(context.Background(), "114", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if version != "114.0.5735.90" {
		t.Fatalf("version = %q", version)
	}
	if url == "" || filepath.Base(url) == "" {
		t.Fatalf("download URL was empty: %q", url)
	}
}

func fakeChromeDriverScript(version string) string {
	return "#!/bin/sh\n# " + undetectedMarker + "\necho 'ChromeDriver " + version + " test'\n"
}
