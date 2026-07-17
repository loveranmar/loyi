// Package codex is the personal-use provider that routes through a ChatGPT
// subscription's Codex backend, using the same wire protocol as OpenAI's
// Codex CLI. This sits in TOS gray territory — it is opt-in, clearly
// labeled, and never the default.
package codex

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/loveranmar/loyi/internal/provider"
)

const (
	DefaultBaseURL = "https://chatgpt.com/backend-api"
	DefaultModel   = "gpt-5.2-codex"

	instructions = "You are Codex, a coding agent running in a CLI. Be precise, direct, and helpful."
)

type Client struct {
	Access    string
	AccountID string
	BaseURL   string
	Model     string
}

func (c *Client) Name() string { return "chatgpt" }

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) model(req provider.Request) string {
	if req.Model != "" {
		return req.Model
	}
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	instr := instructions
	if req.System != "" {
		instr += "\n\n" + req.System
	}

	input := buildInput(req.Messages)

	effort := "medium"
	if req.Effort != "" {
		effort = string(req.Effort)
	}
	body := map[string]any{
		"model":        c.model(req),
		"instructions": instr,
		"input":        input,
		"stream":       true,
		"store":        false,
		"reasoning":    map[string]string{"effort": effort, "summary": "auto"},
		"include":      []string{"reasoning.encrypted_content"},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name,
				"description": t.Description, "parameters": t.Schema,
			})
		}
		body["tools"] = tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/codex/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	session := make([]byte, 16)
	_, _ = rand.Read(session)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	hreq.Header.Set("Authorization", "Bearer "+c.Access)
	hreq.Header.Set("chatgpt-account-id", c.AccountID)
	hreq.Header.Set("OpenAI-Beta", "responses=experimental")
	hreq.Header.Set("originator", "codex_cli_rs")
	hreq.Header.Set("session_id", hex.EncodeToString(session))

	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		text, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("chatgpt backend returned %d: %s", res.StatusCode, strings.TrimSpace(string(text)))
	}

	out := make(chan provider.Chunk)
	go func() {
		defer close(out)
		defer res.Body.Close()
		st := &codexStream{out: out}
		err := provider.SSEData(res.Body, st.event)
		if err != nil {
			out <- provider.Chunk{Err: err}
			return
		}
		out <- provider.Chunk{ToolCalls: st.calls, Usage: st.usage, Done: true}
	}()
	return out, nil
}

// buildInput converts loyi messages into Responses-API input items,
// including function_call and function_call_output for tool round-trips.
func buildInput(msgs []provider.Message) []map[string]any {
	input := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.ToolCallID != "":
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": m.ToolCallID, "output": m.ToolResult,
			})
		case m.Role == provider.RoleAssistant && len(m.ToolCalls) > 0:
			if m.Content != "" {
				input = append(input, textItem("assistant", "output_text", m.Content))
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{
					"type": "function_call", "call_id": tc.ID,
					"name": tc.Name, "arguments": string(tc.Input),
				})
			}
		case m.Content != "":
			ct := "input_text"
			if m.Role == provider.RoleAssistant {
				ct = "output_text"
			}
			input = append(input, textItem(string(m.Role), ct, m.Content))
		}
	}
	return input
}

func textItem(role, contentType, text string) map[string]any {
	return map[string]any{
		"type": "message", "role": role,
		"content": []map[string]string{{"type": contentType, "text": text}},
	}
}

// codexStream accumulates streamed text, function calls, and usage.
type codexStream struct {
	out   chan<- provider.Chunk
	calls []provider.ToolCall
	byID  map[string]int // response item id -> index into calls
	usage *provider.Usage
}

func (s *codexStream) event(data string) bool {
	var ev struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
		Item  struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"item"`
		Response struct {
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(data), &ev) != nil {
		return true
	}
	switch ev.Type {
	case "response.output_text.delta":
		if ev.Delta != "" {
			s.out <- provider.Chunk{Text: ev.Delta}
		}
	case "response.output_item.added":
		if ev.Item.Type == "function_call" {
			if s.byID == nil {
				s.byID = map[string]int{}
			}
			s.byID[ev.Item.ID] = len(s.calls)
			s.calls = append(s.calls, provider.ToolCall{ID: ev.Item.CallID, Name: ev.Item.Name})
		}
	case "response.output_item.done":
		if ev.Item.Type == "function_call" {
			if i, ok := s.byID[ev.Item.ID]; ok {
				if ev.Item.CallID != "" {
					s.calls[i].ID = ev.Item.CallID
				}
				s.calls[i].Input = json.RawMessage(nonEmptyJSON(ev.Item.Arguments))
			}
		}
	case "response.completed", "response.done":
		if ev.Response.Usage != nil {
			s.usage = &provider.Usage{
				InputTokens:  ev.Response.Usage.InputTokens,
				OutputTokens: ev.Response.Usage.OutputTokens,
			}
		}
		return false
	case "response.failed":
		msg := "request failed"
		if ev.Response.Error != nil {
			msg = ev.Response.Error.Message
		}
		s.out <- provider.Chunk{Err: fmt.Errorf("chatgpt: %s", msg)}
		return false
	}
	return true
}

func nonEmptyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}
