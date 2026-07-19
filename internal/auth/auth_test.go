package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPKCEPair(t *testing.T) {
	v, c, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 43 {
		t.Errorf("verifier length = %d, want 43", len(v))
	}
	if strings.ContainsAny(v+c, "+/=") {
		t.Errorf("expected base64url without padding, got %q %q", v, c)
	}
	if v2, _, _ := pkcePair(); v2 == v {
		t.Error("two verifiers should not match")
	}
}

func TestAnthropicFlowURL(t *testing.T) {
	f, err := NewAnthropicFlow()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "claude.ai" || u.Path != "/oauth/authorize" {
		t.Errorf("unexpected authorize endpoint: %s", f.URL)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id":             anthropicClientID,
		"response_type":         "code",
		"code":                  "true",
		"redirect_uri":          anthropicRedirectURI,
		"scope":                 anthropicScope,
		"code_challenge_method": "S256",
		"state":                 f.Verifier,
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if q.Get("code_challenge") == "" {
		t.Error("missing code_challenge")
	}
}

func TestAnthropicExchangeAndRefresh(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "acc-123",
			"refresh_token": "ref-456",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()
	old := AnthropicTokenURL
	AnthropicTokenURL = srv.URL
	defer func() { AnthropicTokenURL = old }()

	f := &AnthropicFlow{Verifier: "verifier-x"}
	tokens, err := f.Exchange(context.Background(), "the-code#the-state")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Access != "acc-123" || tokens.Refresh != "ref-456" {
		t.Errorf("unexpected tokens: %+v", tokens)
	}
	if tokens.Expired() {
		t.Error("fresh token reported expired")
	}
	if gotBody["code"] != "the-code" || gotBody["state"] != "the-state" {
		t.Errorf("code#state not split: %v", gotBody)
	}
	if gotBody["code_verifier"] != "verifier-x" || gotBody["grant_type"] != "authorization_code" {
		t.Errorf("bad exchange body: %v", gotBody)
	}

	if _, err := RefreshAnthropic(context.Background(), "ref-456"); err != nil {
		t.Fatal(err)
	}
	if gotBody["grant_type"] != "refresh_token" || gotBody["refresh_token"] != "ref-456" {
		t.Errorf("bad refresh body: %v", gotBody)
	}
}

func TestAnthropicExchangeRetriesRateLimit(t *testing.T) {
	oldDelays := anthropicRetryDelays
	anthropicRetryDelays = []time.Duration{0, 0, 0}
	defer func() { anthropicRetryDelays = oldDelays }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "acc-retry", "refresh_token": "ref", "expires_in": 3600,
		})
	}))
	defer srv.Close()
	old := AnthropicTokenURL
	AnthropicTokenURL = srv.URL
	defer func() { AnthropicTokenURL = old }()

	f := &AnthropicFlow{Verifier: "v"}
	tokens, err := f.Exchange(context.Background(), "code#state")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Access != "acc-retry" || calls != 2 {
		t.Errorf("tokens = %+v after %d calls, want retry then success", tokens, calls)
	}
}

func TestAnthropicExchangeNoRetryOn400(t *testing.T) {
	oldDelays := anthropicRetryDelays
	anthropicRetryDelays = []time.Duration{0, 0, 0}
	defer func() { anthropicRetryDelays = oldDelays }()

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	old := AnthropicTokenURL
	AnthropicTokenURL = srv.URL
	defer func() { AnthropicTokenURL = old }()

	f := &AnthropicFlow{Verifier: "v"}
	if _, err := f.Exchange(context.Background(), "code"); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("400 was retried %d times; single-use codes must not be re-sent on client errors", calls-1)
	}
}

func TestAnthropicExchangeFallsBackOn404(t *testing.T) {
	oldDelays := anthropicRetryDelays
	anthropicRetryDelays = []time.Duration{0, 0, 0}
	defer func() { anthropicRetryDelays = oldDelays }()

	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "acc-legacy", "refresh_token": "ref", "expires_in": 3600,
		})
	}))
	defer legacy.Close()
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primary.Close()

	oldP, oldL := AnthropicTokenURL, anthropicTokenURLLegacy
	AnthropicTokenURL, anthropicTokenURLLegacy = primary.URL, legacy.URL
	defer func() { AnthropicTokenURL, anthropicTokenURLLegacy = oldP, oldL }()

	f := &AnthropicFlow{Verifier: "v"}
	tokens, err := f.Exchange(context.Background(), "code#state")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Access != "acc-legacy" {
		t.Errorf("expected fallback to legacy endpoint, got %+v", tokens)
	}
}

func TestImportClaudeCode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".credentials.json")
	blob, _ := json.Marshal(map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "sk-ant-oat01-xyz",
			"refreshToken": "ref-xyz",
			"expiresAt":    int64(1784509603939),
		},
	})
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	old := ClaudeCodeCredsPath
	ClaudeCodeCredsPath = path
	defer func() { ClaudeCodeCredsPath = old }()

	if !ClaudeCodeAvailable() {
		t.Fatal("expected a login to be available")
	}
	toks, err := ImportClaudeCode()
	if err != nil {
		t.Fatal(err)
	}
	if toks.Access != "sk-ant-oat01-xyz" || toks.Refresh != "ref-xyz" || toks.Expires != 1784509603939 {
		t.Errorf("imported tokens wrong: %+v", toks)
	}
}

func TestImportClaudeCodeMissing(t *testing.T) {
	old := ClaudeCodeCredsPath
	ClaudeCodeCredsPath = filepath.Join(t.TempDir(), "does-not-exist.json")
	defer func() { ClaudeCodeCredsPath = old }()

	if ClaudeCodeAvailable() {
		t.Error("expected no login available")
	}
	if _, err := ImportClaudeCode(); err == nil {
		t.Error("expected an error for a missing file")
	}
}

func TestOpenAIFlowURL(t *testing.T) {
	f, err := NewOpenAIFlow()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "auth.openai.com" {
		t.Errorf("unexpected host %s", u.Host)
	}
	q := u.Query()
	if q.Get("client_id") != openaiClientID || q.Get("redirect_uri") != openaiRedirectURI {
		t.Errorf("bad client/redirect: %s", f.URL)
	}
	if q.Get("codex_cli_simplified_flow") != "true" || q.Get("originator") != "codex_cli_rs" {
		t.Errorf("missing codex params: %s", f.URL)
	}
}

func TestOpenAIExchange(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "acc-oai",
			"refresh_token": "ref-oai",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()
	old := OpenAITokenURL
	OpenAITokenURL = srv.URL
	defer func() { OpenAITokenURL = old }()

	f := &OpenAIFlow{Verifier: "ver"}
	tokens, err := f.Exchange(context.Background(), "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Access != "acc-oai" {
		t.Errorf("unexpected tokens: %+v", tokens)
	}
	if gotForm.Get("code") != "code-1" || gotForm.Get("code_verifier") != "ver" {
		t.Errorf("bad form: %v", gotForm)
	}
}

func TestParseOpenAICode(t *testing.T) {
	cases := map[string]string{
		"http://localhost:1455/auth/callback?code=abc&state=s": "abc",
		"abc#state":        "abc",
		"code=abc&state=s": "abc",
		"  plaincode  ":    "plaincode",
		"":                 "",
	}
	for in, want := range cases {
		if got := ParseOpenAICode(in); got != want {
			t.Errorf("ParseOpenAICode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatGPTAccountID(t *testing.T) {
	claims := map[string]any{
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct-42"},
	}
	payload, _ := json.Marshal(claims)
	jwt := "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	if got := ChatGPTAccountID(jwt); got != "acct-42" {
		t.Errorf("ChatGPTAccountID = %q, want acct-42", got)
	}
	if got := ChatGPTAccountID("not-a-jwt"); got != "" {
		t.Errorf("expected empty for garbage, got %q", got)
	}
}

func TestCallbackServer(t *testing.T) {
	srv, err := StartCallbackServer("st-1")
	if err != nil {
		t.Skipf("port 1455 unavailable: %v", err)
	}
	defer srv.Close()

	res, err := http.Get("http://127.0.0.1:1455/auth/callback?code=cb-code&state=st-1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	got := <-srv.Result
	if got.Err != nil || got.Code != "cb-code" {
		t.Errorf("callback result = %+v", got)
	}
}
