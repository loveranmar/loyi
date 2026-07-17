// Package openai talks to OpenAI's Responses API. It's used for OpenAI API
// keys (not OpenRouter or custom endpoints, which stay on the chat-completions
// path) because native web search lives in the Responses API, not chat
// completions. The wire shape mirrors the codex backend.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/loveranmar/loyi/internal/provider"
)

const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-5.2"
)

type Client struct {
	APIKey  string
	BaseURL string
	Model   string
}

func (c *Client) Name() string { return "openai" }

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

// Models lists chat-capable model ids (GET /models).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("openai models returned %d: %s", res.StatusCode, strings.TrimSpace(string(text)))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	model := c.model(req)
	body := map[string]any{
		"model":  model,
		"input":  buildInput(req.Messages),
		"stream": true,
		"store":  false,
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	// reasoning.effort is only valid for reasoning models (gpt-5, o-series).
	if req.Effort != "" && isReasoningModel(model) {
		body["reasoning"] = map[string]string{"effort": string(req.Effort)}
	}
	if len(req.Tools) > 0 {
		// loyi's client-side web_search/web_fetch collapse into the Responses
		// API's one native web_search tool (it both searches and reads pages).
		tools := make([]map[string]any, 0, len(req.Tools))
		web := false
		for _, t := range req.Tools {
			if t.Name == "web_search" || t.Name == "web_fetch" {
				web = true
				continue
			}
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name,
				"description": t.Description, "parameters": t.Schema,
			})
		}
		if web {
			tools = append(tools, map[string]any{"type": "web_search"})
		}
		body["tools"] = tools
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)

	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		text, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("openai returned %d: %s", res.StatusCode, strings.TrimSpace(string(text)))
	}

	out := make(chan provider.Chunk)
	go func() {
		defer close(out)
		defer res.Body.Close()
		st := &respStream{out: out}
		if err := provider.SSEData(res.Body, st.event); err != nil {
			out <- provider.Chunk{Err: err}
			return
		}
		out <- provider.Chunk{ToolCalls: st.calls, Usage: st.usage, Done: true}
	}()
	return out, nil
}

func isReasoningModel(m string) bool {
	m = strings.ToLower(m)
	return strings.HasPrefix(m, "gpt-5") || strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
}

// buildInput converts loyi messages into Responses-API input items.
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

// respStream accumulates streamed text, function calls, web-search calls, and usage.
type respStream struct {
	out   chan<- provider.Chunk
	calls []provider.ToolCall
	byID  map[string]int
	usage *provider.Usage
}

func (s *respStream) event(data string) bool {
	var ev struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
		Item  struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Action    struct {
				Query string `json:"query"`
				URL   string `json:"url"`
			} `json:"action"`
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
		switch ev.Item.Type {
		case "function_call":
			if i, ok := s.byID[ev.Item.ID]; ok {
				if ev.Item.CallID != "" {
					s.calls[i].ID = ev.Item.CallID
				}
				s.calls[i].Input = json.RawMessage(nonEmptyJSON(ev.Item.Arguments))
			}
		case "web_search_call":
			q := ev.Item.Action.Query
			if q == "" {
				q = ev.Item.Action.URL
			}
			s.out <- provider.Chunk{ServerTool: &provider.ServerTool{Name: "web_search", Query: q}}
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
		s.out <- provider.Chunk{Err: fmt.Errorf("openai: %s", msg)}
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
