package factory

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/loveranmar/loyi/internal/config"
)

// ModelEntry is one selectable model in the picker.
type ModelEntry struct {
	Provider string // provider id, e.g. "anthropic"
	Model    string // model id, e.g. "claude-opus-4-8"
}

// modelLister is implemented by providers that can list their models live.
type modelLister interface {
	Models(ctx context.Context) ([]string, error)
}

// curated is the fallback model list per provider id, used when a live listing
// isn't available (or returns an unwieldy number). Kept in one place so it's
// easy to update.
var curated = map[string][]string{
	"anthropic":  {"claude-opus-4-8", "claude-sonnet-5", "claude-haiku-4-5"},
	"chatgpt":    {"gpt-5.2-codex", "gpt-5.2"},
	"openai":     {"gpt-5.2", "gpt-4.1", "o3"},
	"openrouter": {"openrouter/auto"},
}

// Catalog returns the models available across every configured provider. For
// each it tries a live listing and falls back to the curated set plus whatever
// model the provider is already configured with.
func Catalog(ctx context.Context, cfg *config.Config) []ModelEntry {
	ids := make([]string, 0, len(cfg.Providers))
	for id := range cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var entries []ModelEntry
	for _, id := range ids {
		for _, m := range modelsFor(ctx, cfg, id) {
			entries = append(entries, ModelEntry{Provider: id, Model: m})
		}
	}
	return entries
}

func modelsFor(ctx context.Context, cfg *config.Config, id string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}

	// live listing first (short timeout, skip absurdly long lists)
	if p, err := Build(ctx, cfg, id); err == nil {
		if l, ok := p.(modelLister); ok {
			lctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			live, err := l.Models(lctx)
			cancel()
			if err == nil && len(live) > 0 && len(live) <= 60 {
				live = filterModels(id, live)
				sort.Strings(live)
				for _, m := range live {
					add(m)
				}
			}
		}
	}

	// curated fallback + the currently-configured model
	if len(out) == 0 {
		for _, m := range curated[id] {
			add(m)
		}
	}
	if pc := cfg.Providers[id]; pc != nil {
		add(pc.Model)
	}
	return out
}

// filterModels trims a live list to the models that make sense to code with.
func filterModels(id string, models []string) []string {
	var out []string
	for _, m := range models {
		lm := strings.ToLower(m)
		switch id {
		case "openai", "chatgpt":
			if strings.HasPrefix(lm, "gpt-") || strings.HasPrefix(lm, "o1") ||
				strings.HasPrefix(lm, "o3") || strings.HasPrefix(lm, "o4") {
				out = append(out, m)
			}
		default:
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return models
	}
	return out
}
