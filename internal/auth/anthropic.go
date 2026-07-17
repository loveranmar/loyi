package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Claude subscription login uses the same OAuth client as Claude Code:
// authorize on claude.ai, paste the code back, exchange on
// console.anthropic.com. PKCE, no client secret.
const (
	anthropicClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicAuthorizeURL = "https://claude.ai/oauth/authorize"
	anthropicRedirectURI  = "https://console.anthropic.com/oauth/code/callback"
	anthropicScope        = "org:create_api_key user:profile user:inference"
)

// AnthropicTokenURL is a var so tests can point it at a mock server.
var AnthropicTokenURL = "https://console.anthropic.com/v1/oauth/token"

// AnthropicFlow holds the state of one in-progress login.
type AnthropicFlow struct {
	URL      string
	Verifier string
}

// NewAnthropicFlow builds the authorize URL the user opens in a browser.
// After logging in, the callback page shows a code (format "code#state")
// which the user pastes back into loyi.
func NewAnthropicFlow() (*AnthropicFlow, error) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"code":                  {"true"},
		"response_type":         {"code"},
		"client_id":             {anthropicClientID},
		"redirect_uri":          {anthropicRedirectURI},
		"scope":                 {anthropicScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {verifier},
	}
	return &AnthropicFlow{
		URL:      anthropicAuthorizeURL + "?" + q.Encode(),
		Verifier: verifier,
	}, nil
}

// Exchange trades the pasted code for tokens. Accepts "code#state" or a
// bare code.
func (f *AnthropicFlow) Exchange(ctx context.Context, pasted string) (Tokens, error) {
	code := strings.TrimSpace(pasted)
	state := ""
	if i := strings.IndexByte(code, '#'); i >= 0 {
		code, state = code[:i], code[i+1:]
	}
	body := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"client_id":     anthropicClientID,
		"redirect_uri":  anthropicRedirectURI,
		"code_verifier": f.Verifier,
	}
	if state != "" {
		body["state"] = state
	}
	return anthropicTokenRequest(ctx, body)
}

// RefreshAnthropic gets a fresh access token from a refresh token.
func RefreshAnthropic(ctx context.Context, refreshToken string) (Tokens, error) {
	return anthropicTokenRequest(ctx, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     anthropicClientID,
	})
}

func anthropicTokenRequest(ctx context.Context, body map[string]string) (Tokens, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, AnthropicTokenURL, bytes.NewReader(payload))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "anthropic")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return Tokens{}, fmt.Errorf("token endpoint returned %d: %s", res.StatusCode, strings.TrimSpace(string(text)))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return Tokens{}, err
	}
	if out.AccessToken == "" {
		return Tokens{}, fmt.Errorf("token response missing access_token")
	}
	return Tokens{
		Access:  out.AccessToken,
		Refresh: out.RefreshToken,
		Expires: time.Now().UnixMilli() + out.ExpiresIn*1000,
	}, nil
}
