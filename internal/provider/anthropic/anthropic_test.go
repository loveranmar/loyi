package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loveranmar/loyi/internal/provider"
)

func TestStreamAPIKey(t *testing.T) {
	var gotAuth, gotBeta, gotKey string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotKey = r.Header.Get("x-api-key")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	c := &Client{APIKey: "sk-test", BaseURL: srv.URL}
	got := collect(t, c, provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if got != "Hello" {
		t.Errorf("streamed text = %q, want Hello", got)
	}
	if gotKey != "sk-test" || gotAuth != "" {
		t.Errorf("api-key mode should set x-api-key only: key=%q auth=%q", gotKey, gotAuth)
	}
	if body["system"] != nil {
		t.Error("api-key mode should not inject the claude code system block")
	}
	_ = gotBeta
}

func TestStreamOAuthInjectsClaudeCode(t *testing.T) {
	var gotAuth, gotBeta string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	c := &Client{Access: "oauth-tok", BaseURL: srv.URL}
	collect(t, c, provider.Request{
		System:   "be terse",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if gotAuth != "Bearer oauth-tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBeta, "oauth-2025-04-20") {
		t.Errorf("anthropic-beta = %q", gotBeta)
	}
	sys, ok := body["system"].([]any)
	if !ok || len(sys) != 2 {
		t.Fatalf("expected 2 system blocks (claude code + user), got %v", body["system"])
	}
	first := sys[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(first, "You are Claude Code") {
		t.Errorf("first system block = %q", first)
	}
}

func TestStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()
	c := &Client{APIKey: "x", BaseURL: srv.URL}
	_, err := c.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}
}

func collect(t *testing.T, c *Client, req provider.Request) string {
	t.Helper()
	ch, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("chunk error: %v", chunk.Err)
		}
		sb.WriteString(chunk.Text)
	}
	return sb.String()
}
