package tripcdp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultOutputPathsUseVendorResultsRunFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path := defaultSingleOutputPath(TaskRequest{}, "260520-1234")
	want := filepath.Join(home, "Documents", "trip_vendor_results", "260520-1234", "trip_scoreandprice_results_260520-1234.xlsx")
	if path != want {
		t.Fatalf("default single output = %q, want %q", path, want)
	}

	ratingPath := defaultSingleOutputPath(TaskRequest{RatingOnly: true}, "260520-1234")
	if !strings.HasSuffix(ratingPath, filepath.Join("260520-1234", "trip_score_results_260520-1234.xlsx")) {
		t.Fatalf("rating output path = %q", ratingPath)
	}

	dir := defaultVendorOutputDir("260520-1234")
	wantDir := filepath.Join(home, "Documents", "trip_vendor_results", "260520-1234")
	if dir != wantDir {
		t.Fatalf("vendor output dir = %q, want %q", dir, wantDir)
	}
}

func TestDefaultSessionPathUsesStateFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, "Documents", "trip_vendor_results", "_state", "session.json")
	if got := defaultSessionPath(); got != want {
		t.Fatalf("default session path = %q, want %q", got, want)
	}
}

func TestMergeSessionFilesDedupesCookiesAndMergesStorage(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	out := filepath.Join(dir, "merged.json")

	mustWriteSession(t, first, sessionData{
		Cookies: []map[string]any{{"name": "sid", "value": "old", "domain": ".trip.com", "path": "/"}},
		LocalStorage: map[string]string{
			"locale": "zh-hk",
		},
		SessionStorage: map[string]string{
			"step": "one",
		},
	})
	mustWriteSession(t, second, sessionData{
		Cookies: []map[string]any{{"name": "sid", "value": "new", "domain": ".trip.com", "path": "/"}},
		LocalStorage: map[string]string{
			"currency": "HKD",
		},
		SessionStorage: map[string]string{
			"step": "two",
		},
	})

	if err := mergeSessionFiles([]string{first, second}, out); err != nil {
		t.Fatalf("mergeSessionFiles returned error: %v", err)
	}
	merged, err := readSessionFile(out)
	if err != nil {
		t.Fatalf("read merged session: %v", err)
	}
	if len(merged.Cookies) != 1 || merged.Cookies[0]["value"] != "new" {
		t.Fatalf("merged cookies = %#v", merged.Cookies)
	}
	if merged.LocalStorage["locale"] != "zh-hk" || merged.LocalStorage["currency"] != "HKD" {
		t.Fatalf("merged local storage = %#v", merged.LocalStorage)
	}
	if merged.SessionStorage["step"] != "two" {
		t.Fatalf("merged session storage = %#v", merged.SessionStorage)
	}
}

func TestMigrateLatestWorkerState(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "_state")
	oldWorker := filepath.Join(root, "260520-0900", "_workers", "01_old")
	newWorker := filepath.Join(root, "260520-1000", "_workers", "01_new")
	mustWriteSession(t, filepath.Join(oldWorker, "session.json"), sessionData{
		Cookies: []map[string]any{{"name": "sid", "value": "old", "domain": ".trip.com", "path": "/"}},
	})
	mustWriteSession(t, filepath.Join(newWorker, "session.json"), sessionData{
		Cookies: []map[string]any{{"name": "sid", "value": "new", "domain": ".trip.com", "path": "/"}},
	})
	mustWrite(t, filepath.Join(newWorker, "profile", "Local State"), []byte(`{}`))
	mustWrite(t, filepath.Join(newWorker, "profile", "SingletonLock"), []byte("lock"))
	mustWrite(t, filepath.Join(newWorker, "profile", "Default", "Preferences"), []byte(`{"profile":{"exit_type":"Crashed"}}`))

	migrated, err := migrateLatestWorkerState(root, stateDir)
	if err != nil {
		t.Fatalf("migrateLatestWorkerState returned error: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration")
	}
	merged, err := readSessionFile(filepath.Join(stateDir, "session.json"))
	if err != nil {
		t.Fatalf("read migrated session: %v", err)
	}
	if len(merged.Cookies) != 1 || merged.Cookies[0]["value"] != "new" {
		t.Fatalf("migrated session = %#v", merged.Cookies)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "profile", "SingletonLock")); !os.IsNotExist(err) {
		t.Fatalf("lock file should not be migrated, stat err=%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(stateDir, "profile", "Default", "Preferences"))
	if err != nil {
		t.Fatalf("read migrated preferences: %v", err)
	}
	var preferences map[string]any
	if err := json.Unmarshal(raw, &preferences); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	profile := preferences["profile"].(map[string]any)
	if profile["exit_type"] != nil {
		t.Fatalf("exit_type = %#v, want nil", profile["exit_type"])
	}
}

func TestConsolidateRuntimeStateSkipsExplicitSession(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit-session.json")
	state := runtimeState{
		StateDir:        filepath.Join(dir, "_state"),
		SessionFile:     explicit,
		StateProfileDir: filepath.Join(dir, "_state", "profile"),
		SessionExplicit: true,
	}
	workerDir := filepath.Join(dir, "run", "_workers", "01_vendor")
	workerSession := filepath.Join(workerDir, "session.json")
	mustWriteSession(t, workerSession, sessionData{
		Cookies: []map[string]any{{"name": "sid", "value": "worker", "domain": ".trip.com", "path": "/"}},
	})
	mustWrite(t, filepath.Join(workerDir, "profile", "Local State"), []byte(`{}`))

	if err := consolidateRuntimeState([]workerResult{{
		Success:     true,
		Rows:        1,
		SessionFile: workerSession,
		ProfileDir:  filepath.Join(workerDir, "profile"),
	}}, state, nil); err != nil {
		t.Fatalf("consolidateRuntimeState returned error: %v", err)
	}
	if fileExists(explicit) {
		t.Fatal("explicit session should not be overwritten by state consolidation")
	}
	if !dirExists(state.StateProfileDir) {
		t.Fatal("state profile should be updated")
	}
}

func mustWriteSession(t *testing.T, path string, data sessionData) {
	t.Helper()
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	mustWrite(t, path, raw)
}
