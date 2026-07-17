package config

import "testing"

func TestMigrateAPIKeys(t *testing.T) {
	c := &Config{APIKeys: map[string]string{"openai": "sk-1"}}
	c.migrate()
	if c.APIKeys != nil {
		t.Error("api_keys should be dropped after migration")
	}
	p := c.Providers["openai"]
	if p == nil || p.Auth != "api_key" || p.APIKey != "sk-1" {
		t.Errorf("migrated provider = %+v", p)
	}
	if c.DefaultProvider != "openai" {
		t.Errorf("default provider = %q", c.DefaultProvider)
	}
}

func TestSetProviderKeepsDefault(t *testing.T) {
	c := &Config{}
	c.SetProvider("anthropic", &Provider{Auth: "oauth"})
	c.SetProvider("openai", &Provider{Auth: "api_key"})
	if c.DefaultProvider != "anthropic" {
		t.Errorf("default should stay anthropic, got %q", c.DefaultProvider)
	}
}
