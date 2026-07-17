package anthropic

import "testing"

func TestWebToolTypes(t *testing.T) {
	dynamic := []string{"claude-opus-4-8", "claude-sonnet-5", "claude-sonnet-4-6", "claude-fable-5"}
	for _, m := range dynamic {
		s, f := webToolTypes(m)
		if s != "web_search_20260209" || f != "web_fetch_20260209" {
			t.Errorf("%s: got (%s,%s), want 2026 versions", m, s, f)
		}
	}
	legacy := []string{"claude-haiku-4-5", "claude-3-5-sonnet"}
	for _, m := range legacy {
		s, f := webToolTypes(m)
		if s != "web_search_20250305" || f != "web_fetch_20250910" {
			t.Errorf("%s: got (%s,%s), want legacy versions", m, s, f)
		}
	}
}

func TestParseServerTool(t *testing.T) {
	if st := parseServerTool("web_search", `{"query":"golang generics"}`); st.Query != "golang generics" {
		t.Errorf("query = %q", st.Query)
	}
	if st := parseServerTool("web_fetch", `{"url":"https://go.dev"}`); st.Query != "https://go.dev" {
		t.Errorf("url = %q", st.Query)
	}
	if st := parseServerTool("web_search", `not json`); st.Query != "" {
		t.Errorf("expected empty query on bad json, got %q", st.Query)
	}
}
