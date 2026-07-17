package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChatGPT subscription login uses the same OAuth client as OpenAI's Codex
// CLI: authorize on auth.openai.com with a localhost callback on port 1455.
// This is the personal-use provider — isolated, opt-in, never the default.
const (
	openaiClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	openaiAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	openaiRedirectURI  = "http://localhost:1455/auth/callback"
	openaiScope        = "openid profile email offline_access"
)

// OpenAITokenURL is a var so tests can point it at a mock server.
var OpenAITokenURL = "https://auth.openai.com/oauth/token"

// OpenAIFlow holds the state of one in-progress login.
type OpenAIFlow struct {
	URL      string
	Verifier string
	State    string
}

// NewOpenAIFlow builds the authorize URL for the ChatGPT login.
func NewOpenAIFlow() (*OpenAIFlow, error) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		return nil, err
	}
	state, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {openaiClientID},
		"redirect_uri":               {openaiRedirectURI},
		"scope":                      {openaiScope},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"codex_cli_rs"},
	}
	return &OpenAIFlow{
		URL:      openaiAuthorizeURL + "?" + q.Encode(),
		Verifier: verifier,
		State:    state,
	}, nil
}

// ParseOpenAICode extracts the authorization code from whatever the user
// pasted: a full redirect URL, "code#state", a query fragment, or a bare code.
func ParseOpenAICode(input string) string {
	v := strings.TrimSpace(input)
	if v == "" {
		return ""
	}
	if u, err := url.Parse(v); err == nil && u.Query().Get("code") != "" {
		return u.Query().Get("code")
	}
	if i := strings.IndexByte(v, '#'); i >= 0 {
		return v[:i]
	}
	if strings.Contains(v, "code=") {
		if q, err := url.ParseQuery(v); err == nil && q.Get("code") != "" {
			return q.Get("code")
		}
	}
	return v
}

// Exchange trades an authorization code for tokens.
func (f *OpenAIFlow) Exchange(ctx context.Context, code string) (Tokens, error) {
	return openaiTokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openaiClientID},
		"code":          {code},
		"code_verifier": {f.Verifier},
		"redirect_uri":  {openaiRedirectURI},
	})
}

// RefreshOpenAI gets a fresh access token from a refresh token.
func RefreshOpenAI(ctx context.Context, refreshToken string) (Tokens, error) {
	return openaiTokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {openaiClientID},
	})
}

func openaiTokenRequest(ctx context.Context, form url.Values) (Tokens, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OpenAITokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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

// ChatGPTAccountID pulls the account id out of the access token JWT.
// Requests to the Codex backend need it as a header.
func ChatGPTAccountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// some JWTs pad; try standard encoding too
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var claims struct {
		Auth struct {
			ChatGPTAccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Auth.ChatGPTAccountID
}

// CallbackResult is what the local server catches from the browser redirect.
type CallbackResult struct {
	Code string
	Err  error
}

// CallbackServer listens on localhost:1455 for the OAuth redirect.
type CallbackServer struct {
	Result   chan CallbackResult
	listener net.Listener
	server   *http.Server
}

// StartCallbackServer starts the local listener. Fails if the port is taken
// (e.g. the official Codex CLI is mid-login) — the paste fallback still works.
func StartCallbackServer(expectedState string) (*CallbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, err
	}
	cs := &CallbackServer{
		Result:   make(chan CallbackResult, 1),
		listener: ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if expectedState != "" && q.Get("state") != expectedState {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			cs.deliver(CallbackResult{Err: fmt.Errorf("oauth state mismatch")})
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			cs.deliver(CallbackResult{Err: fmt.Errorf("callback missing code")})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body style=\"font-family:monospace;background:#1A1815;color:#EDE8E0\"><p style=\"padding:2rem\">logged in — you can close this tab and go back to loyi.</p></body></html>")
		cs.deliver(CallbackResult{Code: code})
	})
	cs.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go cs.server.Serve(ln)
	return cs, nil
}

func (cs *CallbackServer) deliver(r CallbackResult) {
	select {
	case cs.Result <- r:
	default:
	}
}

// Close shuts the listener down.
func (cs *CallbackServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = cs.server.Shutdown(ctx)
}
