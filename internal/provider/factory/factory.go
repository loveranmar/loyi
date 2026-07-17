// Package factory builds provider clients from stored config, refreshing
// OAuth tokens when they're about to expire.
package factory

import (
	"context"
	"fmt"

	"github.com/loveranmar/loyi/internal/auth"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/provider/anthropic"
	"github.com/loveranmar/loyi/internal/provider/codex"
	"github.com/loveranmar/loyi/internal/provider/openai"
	"github.com/loveranmar/loyi/internal/provider/openaicompat"
)

const (
	openrouterBaseURL      = "https://openrouter.ai/api/v1"
	openrouterDefaultModel = "openrouter/auto"
)

// Build returns a ready-to-use provider for the given id. OAuth tokens are
// refreshed and persisted if they're stale.
func Build(ctx context.Context, cfg *config.Config, id string) (provider.Provider, error) {
	pc := cfg.Providers[id]
	if pc == nil {
		return nil, fmt.Errorf("provider %q is not configured — run `loyi setup`", id)
	}

	if pc.Auth == "oauth" {
		if err := ensureFresh(ctx, cfg, id, pc); err != nil {
			return nil, err
		}
	}

	switch id {
	case "anthropic":
		return &anthropic.Client{
			APIKey:  pc.APIKey,
			Access:  pc.Access,
			BaseURL: pc.BaseURL,
			Model:   pc.Model,
		}, nil
	case "chatgpt":
		return &codex.Client{
			Access:    pc.Access,
			AccountID: pc.AccountID,
			BaseURL:   pc.BaseURL,
			Model:     pc.Model,
		}, nil
	case "openai":
		// OpenAI uses the Responses API (native web search); OpenRouter and
		// custom endpoints stay on chat completions.
		return &openai.Client{APIKey: pc.APIKey, BaseURL: pc.BaseURL, Model: pc.Model}, nil
	case "openrouter":
		return compat(id, pc, openrouterBaseURL, openrouterDefaultModel), nil
	default:
		// custom providers are OpenAI-compatible with their own base URL
		if pc.BaseURL == "" {
			return nil, fmt.Errorf("provider %q has no base url configured", id)
		}
		return compat(id, pc, pc.BaseURL, pc.Model), nil
	}
}

func compat(id string, pc *config.Provider, defaultBase, defaultModel string) *openaicompat.Client {
	base := pc.BaseURL
	if base == "" {
		base = defaultBase
	}
	model := pc.Model
	if model == "" {
		model = defaultModel
	}
	return &openaicompat.Client{ID: id, BaseURL: base, APIKey: pc.APIKey, Model: model}
}

func ensureFresh(ctx context.Context, cfg *config.Config, id string, pc *config.Provider) error {
	t := auth.Tokens{Access: pc.Access, Refresh: pc.Refresh, Expires: pc.Expires}
	if !t.Expired() {
		return nil
	}
	if t.Refresh == "" {
		return fmt.Errorf("%s login expired — run `loyi setup` to log in again", id)
	}
	var fresh auth.Tokens
	var err error
	switch id {
	case "anthropic":
		fresh, err = auth.RefreshAnthropic(ctx, t.Refresh)
	case "chatgpt":
		fresh, err = auth.RefreshOpenAI(ctx, t.Refresh)
	default:
		return fmt.Errorf("%s: unknown oauth provider", id)
	}
	if err != nil {
		return fmt.Errorf("refreshing %s login: %w", id, err)
	}
	pc.Access = fresh.Access
	if fresh.Refresh != "" {
		pc.Refresh = fresh.Refresh
	}
	pc.Expires = fresh.Expires
	if id == "chatgpt" {
		if acct := auth.ChatGPTAccountID(fresh.Access); acct != "" {
			pc.AccountID = acct
		}
	}
	return cfg.Save()
}
