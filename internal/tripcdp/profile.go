package tripcdp

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func defaultChromeUserDataDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "Google", "Chrome", "User Data")
		}
		return filepath.Join(home, "AppData", "Local", "Google", "Chrome", "User Data")
	default:
		return filepath.Join(home, ".config", "google-chrome")
	}
}

func resolveChromeUserDataDir(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return expandPath(configured)
	}
	return defaultChromeUserDataDir()
}

func isDefaultChromeUserDataDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return true
	}
	return filepath.Clean(expandPath(path)) == filepath.Clean(defaultChromeUserDataDir())
}

func chromeProfileLocked(userDataDir string) bool {
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if _, err := os.Lstat(filepath.Join(userDataDir, name)); err == nil {
			return true
		}
	}
	return false
}

func fixChromeExitType(userDataDir string) error {
	preferencesPath := filepath.Join(userDataDir, "Default", "Preferences")
	raw, err := os.ReadFile(preferencesPath)
	if err != nil {
		return nil
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		return err
	}
	profile, ok := config["profile"].(map[string]any)
	if !ok {
		return nil
	}
	if _, ok := profile["exit_type"]; !ok {
		return nil
	}
	profile["exit_type"] = nil
	updated, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(preferencesPath, updated, 0o644)
}

func copyChromeProfile(src string, dst string) error {
	src = expandPath(src)
	dst = expandPath(dst)
	if src == "" || dst == "" {
		return fmt.Errorf("source and destination profile directories are required")
	}
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("Chrome profile source is not a directory: %s", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		if shouldSkipChromeProfilePath(rel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func shouldSkipChromeProfilePath(rel string, entry fs.DirEntry) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	name := entry.Name()
	if strings.HasPrefix(name, "Singleton") || strings.HasSuffix(name, "-journal") || strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") {
		return true
	}
	if isChromeCacheDir(name) || isChromeGeneratedRoot(parts) || isChromeExtensionPath(parts) {
		return true
	}
	if entry.IsDir() {
		switch name {
		case "Crashpad", "BrowserMetrics", "Crash Reports":
			return true
		}
	}
	return false
}

func isChromeCacheDir(name string) bool {
	switch name {
	case "Cache", "Code Cache", "GPUCache", "DawnCache", "ShaderCache", "GrShaderCache", "Media Cache", "blob_storage":
		return true
	default:
		return false
	}
}

func isChromeGeneratedRoot(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "ActorSafetyLists", "AmountExtractionHeuristicRegexes", "AutofillStates", "CertificateRevocation",
		"Crowd Deny", "FileTypePolicies", "FirstPartySetsPreloaded", "hyphen-data", "MEIPreload",
		"OptimizationHints", "OptGuideOnDeviceModel", "OriginTrials", "PKIMetadata", "SafetyTips",
		"SSLErrorAssistant", "Subresource Filter", "TrustTokenKeyCommitments", "WasmTtsEngine",
		"ZxcvbnData", "component_crx_cache", "extensions_crx_cache", "segmentation_platform",
		"optimization_guide_model_store", "MediaFoundationWidevineCdm", "OnDeviceHeadSuggestModel":
		return true
	default:
		return false
	}
}

func isChromeExtensionPath(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	if parts[0] != "Default" && parts[0] != "System Profile" {
		return false
	}
	switch parts[1] {
	case "Extensions", "Extension Rules", "Extension Scripts", "Extension State", "Local Extension Settings", "Managed Extension Settings", "Storage":
		return true
	default:
		return false
	}
}

func copyFile(src string, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return nil
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return nil
	}
	return closeErr
}
