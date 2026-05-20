package tripcdp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type runtimeState struct {
	ResultsRoot     string
	StateDir        string
	SessionFile     string
	StateProfileDir string
	ProfileSeedDir  string
	SessionExplicit bool
	Migrated        bool
	Warnings        []string
}

func resolveRuntimeState(request TaskRequest) runtimeState {
	state := runtimeState{
		ResultsRoot:     defaultVendorResultsRoot(),
		StateDir:        defaultStateDir(),
		SessionFile:     defaultStateSessionPath(),
		StateProfileDir: defaultStateProfileDir(),
		ProfileSeedDir:  defaultStateProfileDir(),
	}
	if request.SessionFileExplicit && request.BrowserOptions.SessionFile != "" {
		state.SessionFile = expandPath(request.BrowserOptions.SessionFile)
		state.SessionExplicit = true
	}
	_ = os.MkdirAll(state.StateDir, 0o755)

	sessionMissing := !fileExists(state.SessionFile)
	profileMissing := !dirExists(state.StateProfileDir)
	if sessionMissing || profileMissing {
		if migrated, err := migrateLatestWorkerState(state.ResultsRoot, state.StateDir); err != nil {
			state.Warnings = append(state.Warnings, "state migration failed: "+err.Error())
		} else if migrated {
			state.Migrated = true
		}
	}
	if !request.SessionFileExplicit && !fileExists(state.SessionFile) {
		if legacy := legacyDefaultSessionPath(); fileExists(legacy) {
			_ = ensureParent(state.SessionFile)
			if raw, err := os.ReadFile(legacy); err == nil {
				_ = os.WriteFile(state.SessionFile, raw, 0o644)
			}
		}
	}
	if !request.SessionFileExplicit && !fileExists(state.SessionFile) {
		_ = writeEmptySession(state.SessionFile)
	}
	if !dirExists(state.StateProfileDir) {
		source := defaultChromeUserDataDir()
		if err := replaceProfileDir(source, state.StateProfileDir); err != nil {
			state.Warnings = append(state.Warnings, "state profile seed unavailable: "+err.Error())
			state.ProfileSeedDir = source
		}
	}
	return state
}

func migrateLatestWorkerState(resultsRoot string, stateDir string) (bool, error) {
	runDirs, err := recentRunDirs(resultsRoot)
	if err != nil {
		return false, err
	}
	for _, runDir := range runDirs {
		workerRoot := filepath.Join(runDir, "_workers")
		entries, err := os.ReadDir(workerRoot)
		if err != nil {
			continue
		}
		sessionFiles := []string{}
		profileDirs := []string{}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			workerDir := filepath.Join(workerRoot, entry.Name())
			sessionPath := filepath.Join(workerDir, "session.json")
			if fileExists(sessionPath) {
				sessionFiles = append(sessionFiles, sessionPath)
			}
			profilePath := filepath.Join(workerDir, "profile")
			if usableProfileDir(profilePath) {
				profileDirs = append(profileDirs, profilePath)
			}
		}
		migrated := false
		if len(sessionFiles) > 0 {
			if err := mergeSessionFiles(sessionFiles, filepath.Join(stateDir, "session.json")); err != nil {
				return migrated, err
			}
			migrated = true
		}
		if len(profileDirs) > 0 {
			if err := replaceProfileDir(profileDirs[0], filepath.Join(stateDir, "profile")); err != nil {
				return migrated, err
			}
			migrated = true
		}
		if migrated {
			return true, nil
		}
	}
	return false, nil
}

func consolidateRuntimeState(results []workerResult, state runtimeState, reporter Reporter) error {
	sessionFiles := []string{}
	profileDirs := []string{}
	for _, result := range results {
		if !result.Success && result.Rows == 0 {
			continue
		}
		if result.SessionFile != "" && fileExists(result.SessionFile) {
			sessionFiles = append(sessionFiles, result.SessionFile)
		}
		if result.ProfileDir != "" && usableProfileDir(result.ProfileDir) {
			profileDirs = append(profileDirs, result.ProfileDir)
		}
	}
	if len(sessionFiles) > 0 && !state.SessionExplicit {
		if err := mergeSessionFiles(sessionFiles, state.SessionFile); err != nil {
			return err
		}
		if reporter != nil {
			reporter(Event{"event": "state_saved", "stage": "session", "session_file": state.SessionFile, "session_sources": len(sessionFiles)})
		}
	}
	if len(profileDirs) > 0 {
		if err := replaceProfileDir(profileDirs[0], state.StateProfileDir); err != nil {
			return err
		}
		if reporter != nil {
			reporter(Event{"event": "state_saved", "stage": "profile", "profile_dir": state.StateProfileDir, "profile_sources": len(profileDirs)})
		}
	}
	return nil
}

func mergeSessionFiles(paths []string, output string) error {
	merged := sessionData{
		Cookies:        []map[string]any{},
		LocalStorage:   map[string]string{},
		SessionStorage: map[string]string{},
	}
	cookieIndex := map[string]int{}
	for _, path := range paths {
		data, err := readSessionFile(path)
		if err != nil {
			continue
		}
		for _, cookie := range data.Cookies {
			key := cookieMergeKey(cookie)
			if key == "" {
				continue
			}
			if index, ok := cookieIndex[key]; ok {
				merged.Cookies[index] = cookie
				continue
			}
			cookieIndex[key] = len(merged.Cookies)
			merged.Cookies = append(merged.Cookies, cookie)
		}
		if merged.LocalStorage == nil {
			merged.LocalStorage = map[string]string{}
		}
		for key, value := range data.LocalStorage {
			merged.LocalStorage[key] = value
		}
		if merged.SessionStorage == nil {
			merged.SessionStorage = map[string]string{}
		}
		for key, value := range data.SessionStorage {
			merged.SessionStorage[key] = value
		}
	}
	if output == "" {
		return fmt.Errorf("session output path is required")
	}
	if err := ensureParent(output); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(expandPath(output), raw, 0o644)
}

func readSessionFile(path string) (sessionData, error) {
	var data sessionData
	raw, err := os.ReadFile(expandPath(path))
	if err != nil {
		return data, err
	}
	err = json.Unmarshal(raw, &data)
	if data.LocalStorage == nil {
		data.LocalStorage = map[string]string{}
	}
	if data.SessionStorage == nil {
		data.SessionStorage = map[string]string{}
	}
	return data, err
}

func writeEmptySession(path string) error {
	if path == "" {
		return nil
	}
	return mergeSessionFiles(nil, path)
}

func cookieMergeKey(cookie map[string]any) string {
	name, _ := cookie["name"].(string)
	if name == "" {
		return ""
	}
	domain, _ := cookie["domain"].(string)
	path, _ := cookie["path"].(string)
	if path == "" {
		path = "/"
	}
	return domain + "\x00" + path + "\x00" + name
}

var runDirNameRe = regexp.MustCompile(`^\d{6}-\d{4}$`)

func recentRunDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(expandPath(root))
	if err != nil {
		return nil, err
	}
	dirs := []string{}
	for _, entry := range entries {
		if entry.IsDir() && runDirNameRe.MatchString(entry.Name()) {
			dirs = append(dirs, filepath.Join(expandPath(root), entry.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	return dirs, nil
}

func replaceProfileDir(src string, dst string) error {
	src = expandPath(src)
	dst = expandPath(dst)
	if !usableProfileDir(src) {
		return fmt.Errorf("profile source is not usable: %s", src)
	}
	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".profile-new-*")
	if err != nil {
		return err
	}
	_ = os.RemoveAll(tmp)
	if err := copyChromeProfile(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	_ = fixChromeExitType(tmp)
	backup := dst + ".old"
	_ = os.RemoveAll(backup)
	if dirExists(dst) {
		if err := os.Rename(dst, backup); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		if dirExists(backup) {
			_ = os.Rename(backup, dst)
		}
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

func usableProfileDir(path string) bool {
	path = expandPath(path)
	if !dirExists(path) {
		return false
	}
	return fileExists(filepath.Join(path, "Local State")) || dirExists(filepath.Join(path, "Default"))
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(expandPath(path))
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(expandPath(path))
	return err == nil && info.IsDir()
}
