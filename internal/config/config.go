// Package config loads and saves loyi's user configuration.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Name      string            `json:"name"`
	Theme     string            `json:"theme"`
	Onboarded bool              `json:"onboarded"`
	APIKeys   map[string]string `json:"api_keys,omitempty"`
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
	return &c, nil
}

// Save writes the config file, creating the directory if needed. The file is
// 0600 since it can hold API keys.
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
