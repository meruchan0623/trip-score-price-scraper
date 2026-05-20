package tripcdp

import (
	"net/url"
	"testing"
)

func TestNavigationTargetNormalizesHKURL(t *testing.T) {
	target := newNavigationTarget("https://hk.trip.com/things-to-do/detail/92237157/?foo=bar")
	if target.OriginalURL != "https://hk.trip.com/things-to-do/detail/92237157/?foo=bar" {
		t.Fatalf("original URL was changed: %s", target.OriginalURL)
	}
	if !target.IsHK {
		t.Fatal("target should be marked HK")
	}
	parsed, err := url.Parse(target.NavigateURL)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	if values.Get("locale") != "zh-hk" {
		t.Fatalf("locale = %q, want zh-hk", values.Get("locale"))
	}
	if values.Get("curr") != "HKD" {
		t.Fatalf("curr = %q, want HKD", values.Get("curr"))
	}
	if values.Get("foo") != "bar" {
		t.Fatalf("existing query was not preserved: %s", target.NavigateURL)
	}
	if target.AcceptLanguage != hkAcceptLanguage {
		t.Fatalf("accept language = %q, want HK language", target.AcceptLanguage)
	}
}

func TestNavigationTargetPreservesNonHKURL(t *testing.T) {
	raw := "https://sg.trip.com/things-to-do/detail/92237157/?locale=en-sg"
	target := newNavigationTarget(raw)
	if target.NavigateURL != raw {
		t.Fatalf("non-HK URL changed: %s", target.NavigateURL)
	}
	if target.IsHK {
		t.Fatal("non-HK target should not be marked HK")
	}
}

func TestPreferredLanguageForItemsUsesHKWhenPresent(t *testing.T) {
	language := preferredLanguageForItems([]ScrapeItem{
		{URL: "https://sg.trip.com/things-to-do/detail/1/"},
		{URL: "https://hk.trip.com/things-to-do/detail/2/"},
	})
	if language != hkAcceptLanguage {
		t.Fatalf("language = %q, want HK accept language", language)
	}
}
