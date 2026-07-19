package factory

import (
	"context"
	"testing"

	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/provider/anthropic"
)

func TestBuildAnthropicSetupToken(t *testing.T) {
	cfg := &config.Config{Providers: map[string]*config.Provider{
		"anthropic": {Auth: "api_key", APIKey: "sk-ant-oat01-xyz"},
	}}
	p, err := Build(context.Background(), cfg, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := p.(*anthropic.Client)
	if !ok {
		t.Fatalf("unexpected provider type %T", p)
	}
	if c.APIKey != "" || c.Access != "sk-ant-oat01-xyz" {
		t.Errorf("sk-ant-oat token not routed to bearer auth: %+v", c)
	}
}

func TestBuildAnthropicAPIKey(t *testing.T) {
	cfg := &config.Config{Providers: map[string]*config.Provider{
		"anthropic": {Auth: "api_key", APIKey: "sk-ant-api03-xyz"},
	}}
	p, err := Build(context.Background(), cfg, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	c := p.(*anthropic.Client)
	if c.APIKey != "sk-ant-api03-xyz" || c.Access != "" {
		t.Errorf("plain api key mangled: %+v", c)
	}
}
