package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSettingsCreatesGlobalDefaults(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, ".loyi", "loyi.json")
	s, err := LoadSettingsFiles(gp, filepath.Join(dir, "proj", "loyi.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.CreatedNow() {
		t.Error("first load should create the global file")
	}
	b, err := os.ReadFile(gp)
	if err != nil {
		t.Fatalf("global defaults not written: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"theme", "model", "providers", "permissions", "context", "ui"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("default file missing %q", k)
		}
	}
	if s.Permissions.Mode != "ask" || s.BannerMode() != "first-run" || !s.MascotEnabled() {
		t.Error("defaults should be ask / first-run / mascot on")
	}
}

func TestProjectOverridesGlobal(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "global.json")
	pp := filepath.Join(dir, "loyi.json")
	writeJSON(t, gp, `{
		"theme": "sage",
		"model": {"default": "m-global", "provider": "anthropic"},
		"permissions": {"mode": "ask", "allow": ["read:*.md"]},
		"context": {"ignore": ["dist"], "maxFiles": 100},
		"ui": {"mascot": true, "banner": "always"}
	}`)
	writeJSON(t, pp, `{
		"theme": "honey",
		"model": {"default": "m-proj"},
		"permissions": {"mode": "auto", "allow": ["write:*.html"]},
		"context": {"ignore": ["vendor"]},
		"ui": {"mascot": false}
	}`)
	s, err := LoadSettingsFiles(gp, pp)
	if err != nil {
		t.Fatal(err)
	}
	if s.Theme != "honey" || s.Model.Default != "m-proj" {
		t.Errorf("project scalars should win: theme=%q model=%q", s.Theme, s.Model.Default)
	}
	if s.Model.Provider != "anthropic" {
		t.Error("unset project fields should keep the global value")
	}
	if s.Permissions.Mode != "auto" || s.MascotEnabled() || s.BannerMode() != "always" {
		t.Error("mode/mascot override, banner kept")
	}
	// lists merge
	if len(s.Permissions.Allow) != 2 || len(s.Context.Ignore) != 2 {
		t.Errorf("allow/ignore should merge: %v %v", s.Permissions.Allow, s.Context.Ignore)
	}
	if s.Context.MaxFiles != 100 {
		t.Error("maxFiles from global should survive")
	}
	if s.RuleFile() != pp {
		t.Error("rules should persist to the project file when it exists")
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"permissions": {"mode": "yolo"}}`, "permissions.mode"},
		{`{"ui": {"banner": "sometimes"}}`, "ui.banner"},
		{`{"model": {"effort": "max"}}`, "model.effort"},
		{`{"context": {"maxFiles": -1}}`, "maxFiles"},
		{`{"providers": {"openrouter": {"apiKey": "sk-or-raw-key"}}}`, "env reference"},
		{`{"permissions": {"allow": ["no-colon"]}}`, "tool:pattern"},
		{`{not json`, "not valid json"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		gp := filepath.Join(dir, "loyi.json")
		writeJSON(t, gp, c.body)
		_, err := LoadSettingsFiles(gp, filepath.Join(dir, "none", "loyi.json"))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("body %s: err = %v, want mention of %q", c.body, err, c.want)
		}
		if err != nil && !strings.Contains(err.Error(), gp) {
			t.Errorf("error should name the file: %v", err)
		}
	}
}

func TestDecide(t *testing.T) {
	s := DefaultSettings()
	s.Permissions.Allow = []string{"write:*.html", "run:go *"}
	s.Permissions.Deny = []string{"run:rm *", "write:*.env"}

	cases := []struct {
		tool, target string
		want         Decision
	}{
		{"write", "index.html", Allow},
		{"write", "src/pages/about.html", Allow}, // extension rule crosses dirs
		{"write", "main.go", Ask},
		{"write", "prod.env", Deny},
		{"run", "go test ./...", Allow},
		{"run", "rm -rf /", Deny},
		{"run", "npm install", Ask},
	}
	for _, c := range cases {
		if got := s.Decide(c.tool, c.target); got != c.want {
			t.Errorf("Decide(%s, %s) = %v, want %v", c.tool, c.target, got, c.want)
		}
	}

	s.Permissions.Mode = "auto"
	if s.Decide("write", "main.go") != Allow {
		t.Error("auto mode should allow unmatched calls")
	}
	if s.Decide("run", "rm -rf /") != Deny {
		t.Error("deny rules must beat auto mode")
	}
	s.Permissions.Mode = "readonly"
	if s.Decide("write", "index.html") != Deny {
		t.Error("readonly must beat allow rules")
	}
}

func TestRuleFor(t *testing.T) {
	cases := []struct{ tool, target, want string }{
		{"write", "index.html", "write:*.html"},
		{"edit", "src/app.go", "edit:*.go"},
		{"write", "Makefile", "write:Makefile"},
		{"run", "npm install", "run:npm *"},
		{"run", "make", "run:make"},
	}
	for _, c := range cases {
		if got := RuleFor(c.tool, c.target); got != c.want {
			t.Errorf("RuleFor(%s, %s) = %q, want %q", c.tool, c.target, got, c.want)
		}
	}
}

func TestRememberAllowPersists(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "global.json")
	pp := filepath.Join(dir, "loyi.json")
	writeJSON(t, pp, `{"permissions": {"mode": "ask", "allow": []}}`)
	s, err := LoadSettingsFiles(gp, pp)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RememberAllow("write:*.html"); err != nil {
		t.Fatal(err)
	}
	if s.Decide("write", "a.html") != Allow {
		t.Error("rule should apply in-session immediately")
	}
	// and survive a reload — written to the project file, not the global one
	s2, err := LoadSettingsFiles(gp, pp)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Decide("write", "b.html") != Allow {
		t.Error("rule should persist across loads")
	}
	b, _ := os.ReadFile(gp)
	if strings.Contains(string(b), "*.html") {
		t.Error("rule belongs in the project file, not the global one")
	}
	// appending twice keeps one copy
	if err := s.RememberAllow("write:*.html"); err != nil {
		t.Fatal(err)
	}
	pb, _ := os.ReadFile(pp)
	if strings.Count(string(pb), "write:*.html") != 1 {
		t.Errorf("rule duplicated in file:\n%s", pb)
	}
}

func TestProviderKeyEnvRef(t *testing.T) {
	t.Setenv("LOYI_TEST_KEY", "sk-123")
	p := ProviderRef{APIKey: "env:LOYI_TEST_KEY"}
	if p.Key() != "sk-123" {
		t.Errorf("Key() = %q", p.Key())
	}
	if (ProviderRef{APIKey: "env:LOYI_TEST_UNSET_KEY"}).Key() != "" {
		t.Error("unset env should resolve to empty")
	}
	if (ProviderRef{APIKey: "sk-raw"}).Key() != "" {
		t.Error("non-env refs never resolve")
	}
}
