package codex

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

func TestStreamFunctionCall(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Reading it.\"}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":\"{\\\"path\\\":\\\"a.txt\\\"}\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":50,\"output_tokens\":6}}}\n\n")
	}))
	defer srv.Close()

	c := &Client{Access: "tok", AccountID: "acct", BaseURL: srv.URL}
	ch, err := c.Stream(context.Background(), provider.Request{
		Messages: []provider.Message{provider.UserText("read a.txt")},
		Tools:    []provider.ToolDef{{Name: "read", Description: "d", Schema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var final provider.Chunk
	for chunk := range ch {
		text += chunk.Text
		if chunk.Done {
			final = chunk
		}
	}
	if text != "Reading it." {
		t.Errorf("text = %q", text)
	}
	if len(final.ToolCalls) != 1 || final.ToolCalls[0].Name != "read" || final.ToolCalls[0].ID != "call_1" {
		t.Fatalf("tool calls = %+v", final.ToolCalls)
	}
	if string(final.ToolCalls[0].Input) != `{"path":"a.txt"}` {
		t.Errorf("input = %s", final.ToolCalls[0].Input)
	}
	if final.Usage == nil || final.Usage.InputTokens != 50 {
		t.Errorf("usage = %+v", final.Usage)
	}
	if body["tools"] == nil {
		t.Error("tools not sent")
	}
}

func TestBuildInputRoundTrip(t *testing.T) {
	msgs := []provider.Message{
		provider.UserText("hi"),
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "call_9", Name: "write", Input: json.RawMessage(`{"path":"x"}`)},
		}},
		provider.ToolResultMsg("call_9", "wrote x", false),
	}
	in := buildInput(msgs)
	if len(in) != 3 {
		t.Fatalf("got %d items: %+v", len(in), in)
	}
	if in[1]["type"] != "function_call" || in[1]["call_id"] != "call_9" {
		t.Errorf("call item = %+v", in[1])
	}
	if in[2]["type"] != "function_call_output" || in[2]["output"] != "wrote x" {
		t.Errorf("output item = %+v", in[2])
	}
}

func TestStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"nope"}`)
	}))
	defer srv.Close()
	c := &Client{Access: "t", AccountID: "a", BaseURL: srv.URL}
	_, err := c.Stream(context.Background(), provider.Request{Messages: []provider.Message{provider.UserText("hi")}})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403, got %v", err)
	}
}
