package tripcdp

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	undetectedMarker = "undetected chromedriver"
	cftMilestonesURL = "https://googlechromelabs.github.io/chrome-for-testing/latest-versions-per-milestone-with-downloads.json"
	cftKnownGoodURL  = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"
	legacyDriverURL  = "https://chromedriver.storage.googleapis.com"
)

type chromeDriverInfo struct {
	ChromePath   string
	VersionMain  string
	DriverPath   string
	CacheDir     string
	PrepareMS    int
	DriverReused bool
	VersionFull  string
}

type cftDownload struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

type cftVersion struct {
	Version   string `json:"version"`
	Downloads struct {
		ChromeDriver []cftDownload `json:"chromedriver"`
	} `json:"downloads"`
}

func prepareUndetectedChromeDriver(ctx context.Context, options BrowserOptions) (chromeDriverInfo, error) {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return chromeDriverInfo{}, err
	}
	chromePath := options.ChromePath
	if chromePath == "" {
		chromePath = detectChromePath()
	}
	if chromePath == "" {
		return chromeDriverInfo{}, fmt.Errorf("Google Chrome not found")
	}
	versionMain := options.ChromeVersionMain
	if versionMain == "" {
		versionMain = chromeMajorVersion(chromePath)
	}
	if versionMain == "" {
		return chromeDriverInfo{}, fmt.Errorf("Chrome version detection failed")
	}
	cacheDir, err := undetectedCacheDir()
	if err != nil {
		return chromeDriverInfo{}, err
	}
	if err := ensureWritableDir(cacheDir); err != nil {
		return chromeDriverInfo{}, err
	}
	if custom := expandPath(options.DriverExecutablePath); custom != "" {
		if err := patchChromeDriverBinary(custom); err != nil {
			return chromeDriverInfo{}, err
		}
		if err := validateChromeDriverBinary(custom, versionMain); err != nil {
			return chromeDriverInfo{}, err
		}
		return chromeDriverInfo{ChromePath: chromePath, VersionMain: versionMain, DriverPath: custom, CacheDir: cacheDir, PrepareMS: elapsedMS(started), DriverReused: true, VersionFull: chromeDriverFullVersion(custom)}, nil
	}
	if cached := findCachedPatchedDriver(cacheDir, versionMain); cached != "" {
		return chromeDriverInfo{ChromePath: chromePath, VersionMain: versionMain, DriverPath: cached, CacheDir: cacheDir, PrepareMS: elapsedMS(started), DriverReused: true, VersionFull: chromeDriverFullVersion(cached)}, nil
	}
	version, downloadURL, err := resolveChromeDriverDownload(ctx, versionMain)
	if err != nil {
		return chromeDriverInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return chromeDriverInfo{}, err
	}
	zipPath, err := downloadFile(ctx, downloadURL, cacheDir)
	if err != nil {
		return chromeDriverInfo{}, err
	}
	defer os.Remove(zipPath)
	driverPath := driverCachePath(cacheDir, versionMain)
	if err := unzipChromeDriver(zipPath, driverPath); err != nil {
		return chromeDriverInfo{}, err
	}
	if err := patchChromeDriverBinary(driverPath); err != nil {
		return chromeDriverInfo{}, err
	}
	if err := validateChromeDriverBinary(driverPath, versionMain); err != nil {
		return chromeDriverInfo{}, err
	}
	return chromeDriverInfo{ChromePath: chromePath, VersionMain: versionMain, DriverPath: driverPath, CacheDir: cacheDir, PrepareMS: elapsedMS(started), VersionFull: version}, nil
}

func undetectedCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "undetected_chromedriver"), nil
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "undetected_chromedriver"), nil
		}
		return filepath.Join(home, "AppData", "Roaming", "undetected_chromedriver"), nil
	case "linux":
		return filepath.Join(home, ".local", "share", "undetected_chromedriver"), nil
	default:
		return filepath.Join(home, ".undetected_chromedriver"), nil
	}
}

func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe := filepath.Join(dir, ".write_test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("undetected_chromedriver cache is not writable: %s: %w", dir, err)
	}
	_ = os.Remove(probe)
	return nil
}

func findCachedPatchedDriver(cacheDir string, versionMain string) string {
	if versionMain != "" {
		path := driverCachePath(cacheDir, versionMain)
		if isReusableChromeDriver(path, versionMain) {
			return path
		}
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(cacheDir, "*chromedriver*"))
	type candidate struct {
		path string
		mod  time.Time
	}
	candidates := []candidate{}
	for _, path := range matches {
		stat, err := os.Stat(path)
		if err != nil || stat.IsDir() {
			continue
		}
		if !isReusableChromeDriver(path, versionMain) {
			continue
		}
		candidates = append(candidates, candidate{path: path, mod: stat.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].path
}

func isPatchedChromeDriver(path string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && bytes.Contains(raw, []byte(undetectedMarker))
}

func isReusableChromeDriver(path string, versionMain string) bool {
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() || !isPatchedChromeDriver(path) {
		return false
	}
	if versionMain != "" && chromeDriverMajorVersion(path) != versionMain {
		return false
	}
	return validateChromeDriverBinary(path, versionMain) == nil
}

func patchChromeDriverBinary(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if bytes.Contains(raw, []byte(undetectedMarker)) {
		return nil
	}
	re := regexp.MustCompile(`\{window\.cdc.*?;\}`)
	loc := re.FindIndex(raw)
	if loc == nil {
		return nil
	}
	replacement := []byte(`{console.log("undetected chromedriver 1337!")}`)
	targetLen := loc[1] - loc[0]
	if len(replacement) > targetLen {
		replacement = replacement[:targetLen]
	} else if len(replacement) < targetLen {
		replacement = append(replacement, bytes.Repeat([]byte(" "), targetLen-len(replacement))...)
	}
	patched := append([]byte{}, raw[:loc[0]]...)
	patched = append(patched, replacement...)
	patched = append(patched, raw[loc[1]:]...)
	if err := os.WriteFile(path, patched, 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}

func resolveChromeDriverDownload(ctx context.Context, versionMain string) (string, string, error) {
	if major, _ := strconv.Atoi(versionMain); major > 0 && major <= 114 {
		return resolveLegacyChromeDriverDownload(ctx, versionMain)
	}
	platform := chromeForTestingPlatform()
	if platform == "" {
		return "", "", fmt.Errorf("unsupported platform for Chrome for Testing: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if versionMain == "" {
		var payload struct {
			Channels map[string]cftVersion `json:"channels"`
		}
		if err := getJSON(ctx, cftKnownGoodURL, &payload); err != nil {
			return "", "", err
		}
		stable := payload.Channels["Stable"]
		for _, item := range stable.Downloads.ChromeDriver {
			if item.Platform == platform {
				return stable.Version, item.URL, nil
			}
		}
		return "", "", fmt.Errorf("chromedriver download not found for %s", platform)
	}
	var payload struct {
		Milestones map[string]cftVersion `json:"milestones"`
	}
	if err := getJSON(ctx, cftMilestonesURL, &payload); err != nil {
		return "", "", err
	}
	version, ok := payload.Milestones[versionMain]
	if !ok {
		return "", "", fmt.Errorf("chromedriver milestone %s not found", versionMain)
	}
	for _, item := range version.Downloads.ChromeDriver {
		if item.Platform == platform {
			return version.Version, item.URL, nil
		}
	}
	return "", "", fmt.Errorf("chromedriver download not found for %s milestone %s", platform, versionMain)
}

func resolveLegacyChromeDriverDownload(ctx context.Context, major string) (string, string, error) {
	return resolveLegacyChromeDriverDownloadFromBase(ctx, major, legacyDriverURL)
}

func resolveLegacyChromeDriverDownloadFromBase(ctx context.Context, major string, baseURL string) (string, string, error) {
	platform := legacyChromeDriverPlatform()
	if platform == "" {
		return "", "", fmt.Errorf("unsupported platform for legacy ChromeDriver: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/LATEST_RELEASE_"+major, nil)
	if err != nil {
		return "", "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("GET %s returned %s", req.URL.String(), resp.Status)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	version := strings.TrimSpace(string(raw))
	if version == "" {
		return "", "", fmt.Errorf("legacy chromedriver version not found for milestone %s", major)
	}
	return version, fmt.Sprintf("%s/%s/chromedriver_%s.zip", strings.TrimRight(baseURL, "/"), version, platform), nil
}

func chromeForTestingPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-arm64"
		}
		return "mac-x64"
	case "windows":
		if runtime.GOARCH == "386" {
			return "win32"
		}
		return "win64"
	case "linux":
		return "linux64"
	default:
		return ""
	}
}

func legacyChromeDriverPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac_arm64"
		}
		return "mac64"
	case "windows":
		if runtime.GOARCH == "386" {
			return "win32"
		}
		return "win32"
	case "linux":
		return "linux64"
	default:
		return ""
	}
}

func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func downloadFile(ctx context.Context, url string, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s returned %s", url, resp.Status)
	}
	path := filepath.Join(dir, "chromedriver_download.zip")
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", err
	}
	return path, nil
}

func unzipChromeDriver(zipPath string, driverPath string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	exeName := "chromedriver"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || filepath.Base(file.Name) != exeName {
			continue
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		defer src.Close()
		tmp := driverPath + ".tmp"
		dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			_ = dst.Close()
			return err
		}
		if err := dst.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp, driverPath); err != nil {
			return err
		}
		return os.Chmod(driverPath, 0o755)
	}
	return fmt.Errorf("chromedriver executable not found in %s", zipPath)
}

func driverCacheName() string {
	if runtime.GOOS == "windows" {
		return "undetected_chromedriver.exe"
	}
	return "undetected_chromedriver"
}

func driverCachePath(cacheDir string, chromeMajor string) string {
	name := "undetected_chromedriver"
	if chromeMajor != "" {
		name += "_" + chromeMajor
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(cacheDir, name)
}

func chromeDriverMajorVersion(path string) string {
	version := chromeDriverFullVersion(path)
	if version == "" {
		return ""
	}
	match := regexp.MustCompile(`^(\d+)\.`).FindStringSubmatch(version)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func chromeDriverFullVersion(path string) string {
	out, err := execCommandOutput(path, "--version")
	if err != nil {
		return ""
	}
	match := regexp.MustCompile(`ChromeDriver\s+([0-9.]+)`).FindStringSubmatch(out)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func validateChromeDriverBinary(path string, chromeMajor string) error {
	version := chromeDriverFullVersion(path)
	if version == "" {
		return fmt.Errorf("chromedriver --version failed for %s", path)
	}
	if chromeMajor != "" && !strings.HasPrefix(version, chromeMajor+".") {
		return fmt.Errorf("chromedriver version %s does not match Chrome major %s", version, chromeMajor)
	}
	return nil
}

func execCommandOutput(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
