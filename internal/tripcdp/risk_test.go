package tripcdp

import (
	"testing"
	"time"
)

func TestClassifyRiskScriptsWithSKUIsDiagnosticOnly(t *testing.T) {
	result := classifyRiskChallenge(
		"套餐與價格 1天 每日1GB",
		"https://hk.trip.com/things-to-do/detail/92237157/",
		[]string{"https://static.tripcdn.com/risk/ubtrms/dist/rms.js", "https://static.tripcdn.com/infosec/jigsaw-captcha.js"},
		true,
		0,
	)
	if result.Challenge {
		t.Fatalf("risk scripts on normal SKU page should not be challenge: %+v", result)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts not preserved: %+v", result.Scripts)
	}
}

func TestClassifyRiskChallengeWithoutSKU(t *testing.T) {
	result := classifyRiskChallenge(
		"Security verification. Please verify you are human.",
		"https://hk.trip.com/things-to-do/detail/92237157/",
		nil,
		false,
		0,
	)
	if !result.Challenge {
		t.Fatalf("verification page should be a challenge: %+v", result)
	}
}

func TestClassifyCloudflareChallengeKind(t *testing.T) {
	result := classifyRiskChallenge(
		"Cloudflare Just a moment Checking if the site connection is secure",
		"https://hk.trip.com/things-to-do/detail/92237157/",
		nil,
		false,
		0,
	)
	if !result.Challenge {
		t.Fatalf("cloudflare page should be a challenge diagnostic: %+v", result)
	}
	if result.Kind != "cloudflare" {
		t.Fatalf("cloudflare kind = %q", result.Kind)
	}
}

func TestClassifyCloudflareTurnstileTraditionalChineseKind(t *testing.T) {
	result := classifyRiskChallenge(
		`<label><input type="checkbox"><span>驗證您是人類</span></label><span id="LLoRg3">正在驗證...</span><a href="/cdn-cgi/challenge-platform/help">說明</a>`,
		"https://hk.trip.com/things-to-do/detail/92237157/",
		nil,
		false,
		0,
	)
	if !result.Challenge {
		t.Fatalf("cloudflare turnstile page should be a challenge diagnostic: %+v", result)
	}
	if result.Kind != "cloudflare" {
		t.Fatalf("cloudflare turnstile kind = %q", result.Kind)
	}
}

func TestClassifyWhaleguardAlwaysChallenge(t *testing.T) {
	result := classifyRiskChallenge(
		"套餐與價格",
		"https://hk.trip.com/whaleguard/block",
		nil,
		true,
		0,
	)
	if !result.Challenge {
		t.Fatalf("whaleguard URL should be a challenge: %+v", result)
	}
	if result.Kind != "whaleguard" {
		t.Fatalf("whaleguard kind = %q", result.Kind)
	}
}

func TestClassifyWhaleguardStatus432(t *testing.T) {
	result := classifyRiskChallenge(
		"whaleguard block",
		"https://hk.trip.com/things-to-do/detail/67897052/?curr=HKD",
		nil,
		false,
		432,
	)
	if !result.Challenge {
		t.Fatalf("432 whaleguard body should be a challenge: %+v", result)
	}
	if result.Kind != "whaleguard" {
		t.Fatalf("risk kind = %q, want whaleguard", result.Kind)
	}
	if result.HTTPStatus != 432 {
		t.Fatalf("http status = %d, want 432", result.HTTPStatus)
	}
	if result.BodyPreview != "whaleguard block" {
		t.Fatalf("body preview = %q", result.BodyPreview)
	}
}

func TestRiskCooldownDelayRanges(t *testing.T) {
	tests := []struct {
		profile string
		min     time.Duration
		max     time.Duration
	}{
		{profile: "fast", min: 30 * time.Second, max: 60 * time.Second},
		{profile: "normal", min: 30 * time.Second, max: 60 * time.Second},
		{profile: "safe", min: 60 * time.Second, max: 120 * time.Second},
		{profile: "slow", min: 120 * time.Second, max: 240 * time.Second},
	}
	for _, tt := range tests {
		for i := 0; i < 20; i++ {
			delay := riskCooldownDelay(tt.profile)
			if delay < tt.min || delay > tt.max {
				t.Fatalf("%s cooldown out of range: %s", tt.profile, delay)
			}
		}
	}
}

func TestGlobalRiskPauseAfterMultipleWhaleguardEvents(t *testing.T) {
	pause := newGlobalRiskPause("fast", nil)
	pause.observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard"})
	if !pause.pauseUntil.IsZero() {
		t.Fatal("single whaleguard event should not trigger global cooldown")
	}
	pause.observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard"})
	if !pause.pauseUntil.After(time.Now()) {
		t.Fatal("multiple whaleguard events should trigger global cooldown")
	}
}
