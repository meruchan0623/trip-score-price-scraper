package tripcdp

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func absPath(path string) string {
	if path == "" {
		return ""
	}
	expanded := expandPath(path)
	if abs, err := filepath.Abs(expanded); err == nil {
		return abs
	}
	return expanded
}

func defaultOutputPath(request TaskRequest) string {
	stamp := time.Now().Format("060102-1504")
	return defaultSingleOutputPath(request, stamp)
}

func defaultSingleOutputPath(request TaskRequest, stamp string) string {
	if stamp == "" {
		stamp = time.Now().Format("060102-1504")
	}
	name := "trip_scoreandprice_results_" + stamp + ".xlsx"
	if request.RatingOnly {
		name = "trip_score_results_" + stamp + ".xlsx"
	}
	if outputDir := strings.TrimSpace(request.OutputDir); outputDir != "" {
		return filepath.Join(expandPath(outputDir), name)
	}
	return filepath.Join(defaultRunOutputDir(stamp), name)
}

func defaultVendorResultsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents", "trip_vendor_results")
}

func defaultRunOutputDir(stamp string) string {
	if stamp == "" {
		stamp = time.Now().Format("060102-1504")
	}
	return filepath.Join(defaultVendorResultsRoot(), stamp)
}

func defaultVendorOutputDir(stamp string) string {
	return defaultRunOutputDir(stamp)
}

func defaultSingleLogFile(outputPath string, stamp string) string {
	if stamp == "" {
		stamp = time.Now().Format("060102-1504")
	}
	return filepath.Join(filepath.Dir(expandPath(outputPath)), "trip_cli_"+stamp+".log")
}

func defaultSessionPath() string {
	return defaultStateSessionPath()
}

func legacyDefaultSessionPath() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("PROGRAMDATA")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "TripDetailFilling", "session_data.json")
	case "darwin":
		return filepath.Join(home, "Library", "Preferences", "TripDetailFilling", "session_data.json")
	default:
		return filepath.Join(home, ".config", "TripDetailFilling", "session_data.json")
	}
}

func defaultStateDir() string {
	return filepath.Join(defaultVendorResultsRoot(), "_state")
}

func defaultStateSessionPath() string {
	return filepath.Join(defaultStateDir(), "session.json")
}

func defaultStateProfileDir() string {
	return filepath.Join(defaultStateDir(), "profile")
}

var unsafeFilenameRe = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]+`)

func safeFilename(value string) string {
	cleaned := strings.Trim(unsafeFilenameRe.ReplaceAllString(value, "_"), " ._")
	if cleaned == "" {
		return "未命名厂商"
	}
	return cleaned
}
