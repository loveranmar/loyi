package factory

import (
	"context"
	"testing"

	"github.com/loveranmar/loyi/internal/config"
)

func TestFilterModels(t *testing.T) {
	in := []string{"gpt-5.2", "o3-mini", "text-embedding-3", "dall-e-3", "whisper-1"}
	got := filterModels("openai", in)
	want := map[string]bool{"gpt-5.2": true, "o3-mini": true}
	if len(got) != 2 {
		t.Fatalf("filtered = %v, want 2 chat models", got)
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unexpected model %q kept", m)
		}
	}
	// non-openai providers pass everything through
	if len(filterModels("anthropic", in)) != len(in) {
		t.Error("anthropic should not filter")
	}
}

func TestCatalogFallsBackToCurated(t *testing.T) {
	// a custom provider pointing at a dead endpoint → live listing fails →
	// catalog should fall back to the configured model.
	cfg := &config.Config{
		DefaultProvider: "custom",
		Providers: map[string]*config.Provider{
			"custom": {Auth: "api_key", APIKey: "k", BaseURL: "http://127.0.0.1:9", Model: "my-model"},
		},
	}
	entries := Catalog(context.Background(), cfg)
	found := false
	for _, e := range entries {
		if e.Provider == "custom" && e.Model == "my-model" {
			found = true
		}
	}
	if !found {
		t.Errorf("catalog should include the configured model as a fallback, got %+v", entries)
	}
}

func TestCatalogCuratedForAnthropicWhenOffline(t *testing.T) {
	cfg := &config.Config{
		DefaultProvider: "anthropic",
		Providers: map[string]*config.Provider{
			"anthropic": {Auth: "api_key", APIKey: "k", BaseURL: "http://127.0.0.1:9"},
		},
	}
	entries := Catalog(context.Background(), cfg)
	if len(entries) == 0 {
		t.Fatal("expected curated anthropic models when offline")
	}
	if entries[0].Model != "claude-opus-4-8" {
		t.Errorf("first curated anthropic model = %q, want claude-opus-4-8", entries[0].Model)
	}
}
