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

func TestStreamToolUseAccumulates(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		// text, then a tool_use block whose input JSON streams in pieces
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Writing it.\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_9\",\"name\":\"write\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"a.txt\\\"}\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	c := &Client{APIKey: "k", BaseURL: srv.URL}
	ch, err := c.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "write a.txt"}},
		Tools:    []provider.ToolDef{{Name: "write", Description: "d", Schema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var calls []provider.ToolCall
	for chunk := range ch {
		text += chunk.Text
		if chunk.Done {
			calls = chunk.ToolCalls
		}
	}
	if text != "Writing it." {
		t.Errorf("text = %q", text)
	}
	if len(calls) != 1 || calls[0].Name != "write" || calls[0].ID != "toolu_9" {
		t.Fatalf("calls = %+v", calls)
	}
	if string(calls[0].Input) != `{"path":"a.txt"}` {
		t.Errorf("accumulated input = %s", calls[0].Input)
	}
	if body["tools"] == nil {
		t.Error("tools not sent in request body")
	}
}

func TestBuildMessagesToolRoundTrip(t *testing.T) {
	msgs := []provider.Message{
		provider.UserText("hi"),
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "t1", Name: "read", Input: json.RawMessage(`{"path":"x"}`)},
		}},
		provider.ToolResultMsg("t1", "file contents", false),
	}
	out := buildMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("got %d messages: %+v", len(out), out)
	}
	if out[1]["role"] != "assistant" {
		t.Errorf("second message role = %v", out[1]["role"])
	}
	blocks := out[2]["content"].([]any)
	tr := blocks[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "t1" {
		t.Errorf("tool result block = %+v", tr)
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
