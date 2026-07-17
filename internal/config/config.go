// Package config loads and saves loyi's user configuration.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Provider is the stored configuration for one model backend.
type Provider struct {
	Auth    string `json:"auth"` // "api_key" or "oauth"
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"` // custom endpoints and tests
	Model   string `json:"model,omitempty"`

	// oauth state
	Access    string `json:"access,omitempty"`
	Refresh   string `json:"refresh,omitempty"`
	Expires   int64  `json:"expires,omitempty"` // unix milliseconds
	AccountID string `json:"account_id,omitempty"`
}

type Config struct {
	Name      string `json:"name"`
	Theme     string `json:"theme"`
	Onboarded bool   `json:"onboarded"`

	DefaultProvider string               `json:"default_provider,omitempty"`
	Providers       map[string]*Provider `json:"providers,omitempty"`

	// APIKeys is the pre-provider-rework format; migrated into Providers
	// on load and dropped on the next save.
	APIKeys map[string]string `json:"api_keys,omitempty"`
}

// SetProvider stores a provider config, creating the map if needed.
func (c *Config) SetProvider(id string, p *Provider) {
	if c.Providers == nil {
		c.Providers = map[string]*Provider{}
	}
	c.Providers[id] = p
	if c.DefaultProvider == "" {
		c.DefaultProvider = id
	}
}

// Path returns the config file location, e.g. ~/.config/loyi/config.json.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "loyi", "config.json"), nil
}

// Load reads the config file. A missing file returns os.ErrNotExist.
func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	c.migrate()
	return &c, nil
}

// migrate converts the old flat api_keys map into provider entries.
func (c *Config) migrate() {
	for id, key := range c.APIKeys {
		if c.Providers[id] == nil {
			c.SetProvider(id, &Provider{Auth: "api_key", APIKey: key})
		}
	}
	c.APIKeys = nil
}

// Save writes the config file, creating the directory if needed. The file is
// 0600 since it holds keys and tokens.
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}
