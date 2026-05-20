package tripcdp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestSessionDataJSONShapeRoundTrips(t *testing.T) {
	input := sessionData{
		Cookies: []map[string]any{{"name": "sid", "value": "abc", "domain": ".trip.com", "path": "/", "secure": true, "httpOnly": true}},
		LocalStorage: map[string]string{
			"local": "value",
		},
		SessionStorage: map[string]string{
			"session": "value",
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output sessionData
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.Cookies[0]["name"] != "sid" || output.LocalStorage["local"] != "value" || output.SessionStorage["session"] != "value" {
		t.Fatalf("unexpected session round-trip: %+v", output)
	}
}

func TestLoadSessionEmptyFileSkipsBrowserPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(`{"cookies":[],"local_storage":{},"session_storage":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := loadSession(context.Background(), nil, path, "https://hk.trip.com/things-to-do/detail/92237157/?locale=zh-hk&curr=HKD")
	if err != nil {
		t.Fatalf("loadSession error = %v, want nil", err)
	}
	if result.Loaded {
		t.Fatal("empty session should not be loaded")
	}
}

func TestLoadSessionNonEmptyNilPageReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte(`{"cookies":[{"name":"sid","value":"abc"}],"local_storage":{},"session_storage":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := loadSession(context.Background(), nil, path, "https://hk.trip.com/things-to-do/detail/92237157/?locale=zh-hk&curr=HKD")
	if err == nil {
		t.Fatal("loadSession error = nil, want error")
	}
	if result.Loaded {
		t.Fatal("session should not load without a browser page")
	}
}

func TestSessionCookieRequestAcceptsPythonCookieFields(t *testing.T) {
	request, ok := sessionCookieRequest(map[string]any{
		"name":     "sid",
		"value":    "abc",
		"domain":   ".trip.com",
		"path":     "/",
		"secure":   true,
		"httpOnly": true,
		"sameSite": "Lax",
		"expires":  1893456000.0,
		"extra":    "ignored",
	}, "https://hk.trip.com/things-to-do/detail/92237157/?locale=zh-hk&curr=HKD")
	if !ok {
		t.Fatal("cookie request was not created")
	}
	if request.Name != "sid" || request.Value != "abc" || request.Domain != ".trip.com" || request.Path != "/" {
		t.Fatalf("unexpected cookie request: %+v", request)
	}
	if !request.Secure || !request.HTTPOnly {
		t.Fatalf("secure/httpOnly were not preserved: %+v", request)
	}
	if request.SameSite != proto.NetworkCookieSameSiteLax {
		t.Fatalf("sameSite = %q, want Lax", request.SameSite)
	}
	if request.Expires != proto.TimeSinceEpoch(1893456000) {
		t.Fatalf("expires = %v, want 1893456000", request.Expires)
	}
}

func TestSessionCookieRequestDefaultsToTargetOriginWithoutDomain(t *testing.T) {
	request, ok := sessionCookieRequest(map[string]any{
		"name":  "sid",
		"value": "abc",
	}, "https://hk.trip.com/things-to-do/detail/92237157/?locale=zh-hk&curr=HKD")
	if !ok {
		t.Fatal("cookie request was not created")
	}
	if request.URL != "https://hk.trip.com" {
		t.Fatalf("URL = %q, want target origin", request.URL)
	}
	if request.Domain != "" {
		t.Fatalf("domain = %q, want empty", request.Domain)
	}
	if request.Path != "/" {
		t.Fatalf("path = %q, want /", request.Path)
	}
}

func TestSaveSessionNilPageDoesNotPanicOrWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if saveSession(nil, path) {
		t.Fatal("saveSession nil page = true, want false")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file should not be written for nil page, stat err=%v", err)
	}
}
