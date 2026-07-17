// Package anthropic implements the Anthropic Messages API backend.
// Two auth modes: a plain API key, or an OAuth access token from a Claude
// subscription login. The OAuth mode presents as Claude Code — that's what
// the token is scoped to accept.
package anthropic

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
	DefaultBaseURL = "https://api.anthropic.com"
	DefaultModel   = "claude-opus-4-8"

	// claudeCodeSystem must be the first system block on OAuth requests.
	claudeCodeSystem = "You are Claude Code, Anthropic's official CLI for Claude."
)

type Client struct {
	APIKey  string // api-key mode
	Access  string // oauth mode (used when APIKey is empty)
	BaseURL string
	Model   string
}

func (c *Client) Name() string { return "anthropic" }

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

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// buildMessages converts loyi messages into Anthropic's block format,
// coalescing consecutive tool results into a single user turn.
func buildMessages(msgs []provider.Message) []map[string]any {
	var out []map[string]any
	for _, m := range msgs {
		switch {
		case m.ToolCallID != "":
			block := map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.ToolResult,
			}
			if m.IsError {
				block["is_error"] = true
			}
			if n := len(out); n > 0 && out[n-1]["role"] == "user" {
				if blocks, ok := out[n-1]["content"].([]any); ok {
					out[n-1]["content"] = append(blocks, block)
					continue
				}
			}
			out = append(out, map[string]any{"role": "user", "content": []any{block}})
		case m.Role == provider.RoleAssistant:
			var blocks []any
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				_ = json.Unmarshal(tc.Input, &input)
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input,
				})
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		default:
			out = append(out, map[string]any{"role": "user", "content": m.Content})
		}
	}
	return out
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	oauth := c.APIKey == ""

	var system []textBlock
	if oauth {
		system = append(system, textBlock{Type: "text", Text: claudeCodeSystem})
	}
	if req.System != "" {
		system = append(system, textBlock{Type: "text", Text: req.System})
	}

	body := map[string]any{
		"model":      c.model(req),
		"max_tokens": 32000,
		"stream":     true,
		"messages":   buildMessages(req.Messages),
	}
	if len(system) > 0 {
		body["system"] = system
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name": t.Name, "description": t.Description, "input_schema": t.Schema,
			})
		}
		body["tools"] = tools
	}
	if req.Effort != "" {
		body["output_config"] = map[string]string{"effort": string(req.Effort)}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("anthropic-version", "2023-06-01")
	if oauth {
		hreq.Header.Set("Authorization", "Bearer "+c.Access)
		hreq.Header.Set("anthropic-beta", "oauth-2025-04-20,claude-code-20250219")
	} else {
		hreq.Header.Set("x-api-key", c.APIKey)
	}

	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		text, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("anthropic returned %d: %s", res.StatusCode, strings.TrimSpace(string(text)))
	}

	out := make(chan provider.Chunk)
	go func() {
		defer close(out)
		defer res.Body.Close()
		stream := &anthropicStream{out: out}
		err := provider.SSEData(res.Body, stream.event)
		if err != nil {
			out <- provider.Chunk{Err: err}
			return
		}
		out <- provider.Chunk{ToolCalls: stream.finishedCalls(), Done: true}
	}()
	return out, nil
}

// anthropicStream accumulates tool_use input JSON across streamed deltas.
type anthropicStream struct {
	out   chan<- provider.Chunk
	calls []provider.ToolCall
	cur   int // index into calls of the block currently streaming (-1 = none)
	buf   strings.Builder
}

func (s *anthropicStream) event(data string) bool {
	var ev struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(data), &ev) != nil {
		return true
	}
	switch ev.Type {
	case "content_block_start":
		if ev.ContentBlock.Type == "tool_use" {
			s.calls = append(s.calls, provider.ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name})
			s.cur = len(s.calls) - 1
			s.buf.Reset()
		} else {
			s.cur = -1
		}
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text != "" {
				s.out <- provider.Chunk{Text: ev.Delta.Text}
			}
		case "input_json_delta":
			s.buf.WriteString(ev.Delta.PartialJSON)
		}
	case "content_block_stop":
		if s.cur >= 0 && s.cur < len(s.calls) {
			s.calls[s.cur].Input = json.RawMessage(s.buf.String())
			s.cur = -1
		}
	case "error":
		s.out <- provider.Chunk{Err: fmt.Errorf("anthropic: %s", ev.Error.Message)}
		return false
	case "message_stop":
		return false
	}
	return true
}

func (s *anthropicStream) finishedCalls() []provider.ToolCall {
	for i := range s.calls {
		if len(s.calls[i].Input) == 0 {
			s.calls[i].Input = json.RawMessage("{}")
		}
	}
	return s.calls
}
