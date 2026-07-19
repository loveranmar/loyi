package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeCodeCredsPath is the file Claude Code stores its logins in. A var so
// tests can point it elsewhere.
var ClaudeCodeCredsPath = defaultClaudeCodeCredsPath()

func defaultClaudeCodeCredsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
}

// ClaudeCodeAvailable reports whether a Claude Code subscription login exists
// on disk that we could import.
func ClaudeCodeAvailable() bool {
	_, err := readClaudeCodeTokens()
	return err == nil
}

// ImportClaudeCode reads the subscription tokens Claude Code already stored,
// so loyi can reuse them without running the browser OAuth flow (which avoids
// the rate-limited token endpoint entirely).
func ImportClaudeCode() (Tokens, error) {
	return readClaudeCodeTokens()
}

func readClaudeCodeTokens() (Tokens, error) {
	if ClaudeCodeCredsPath == "" {
		return Tokens{}, fmt.Errorf("can't locate your home directory")
	}
	data, err := os.ReadFile(ClaudeCodeCredsPath)
	if err != nil {
		return Tokens{}, fmt.Errorf("no claude code login found (%s)", ClaudeCodeCredsPath)
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
			ExpiresAt    int64  `json:"expiresAt"` // unix milliseconds
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return Tokens{}, fmt.Errorf("couldn't parse the claude code login file")
	}
	o := creds.ClaudeAiOauth
	if o.AccessToken == "" {
		return Tokens{}, fmt.Errorf("claude code has no subscription login to import — run `claude` and log in first")
	}
	return Tokens{Access: o.AccessToken, Refresh: o.RefreshToken, Expires: o.ExpiresAt}, nil
}
