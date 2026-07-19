package openai

import (
	"encoding/json"
	"testing"

	"github.com/loveranmar/loyi/internal/provider"
)

func TestIsReasoningModel(t *testing.T) {
	yes := []string{"gpt-5.2", "gpt-5", "o1", "o3-mini", "o4-mini"}
	for _, m := range yes {
		if !isReasoningModel(m) {
			t.Errorf("%s should be a reasoning model", m)
		}
	}
	no := []string{"gpt-4.1", "gpt-4o", "gpt-3.5-turbo"}
	for _, m := range no {
		if isReasoningModel(m) {
			t.Errorf("%s should not be a reasoning model", m)
		}
	}
}

func TestBuildInputRoundTrip(t *testing.T) {
	msgs := []provider.Message{
		provider.UserText("hi"),
		{Role: provider.RoleAssistant, ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"x"}`)},
		}},
		provider.ToolResultMsg("c1", "contents", false),
	}
	in := buildInput(msgs)
	if len(in) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(in))
	}
	if in[0]["type"] != "message" {
		t.Errorf("first item = %v, want message", in[0]["type"])
	}
	if in[1]["type"] != "function_call" || in[1]["call_id"] != "c1" {
		t.Errorf("second item = %v, want function_call c1", in[1])
	}
	if in[2]["type"] != "function_call_output" || in[2]["output"] != "contents" {
		t.Errorf("third item = %v, want function_call_output", in[2])
	}
}
