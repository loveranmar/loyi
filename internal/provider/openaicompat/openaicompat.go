// Package openaicompat implements the OpenAI chat-completions wire format,
// shared by the openai, openrouter, and custom (bring-your-own-base-URL)
// backends.
package openaicompat

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

type Client struct {
	ID      string // provider id shown to the user: "openai", "openrouter", "custom"
	BaseURL string // e.g. https://api.openai.com/v1
	APIKey  string
	Model   string
}

func (c *Client) Name() string { return c.ID }

func (c *Client) model(req provider.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return c.Model
}

// buildMessages converts loyi messages into OpenAI chat format.
func buildMessages(system string, msgs []provider.Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs)+1)
	if system != "" {
		out = append(out, map[string]any{"role": "system", "content": system})
	}
	for _, m := range msgs {
		switch {
		case m.ToolCallID != "":
			out = append(out, map[string]any{
				"role": "tool", "tool_call_id": m.ToolCallID, "content": m.ToolResult,
			})
		case m.Role == provider.RoleAssistant && len(m.ToolCalls) > 0:
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, map[string]any{
					"id": tc.ID, "type": "function",
					"function": map[string]any{"name": tc.Name, "arguments": string(tc.Input)},
				})
			}
			msg := map[string]any{"role": "assistant", "tool_calls": calls}
			if m.Content != "" {
				msg["content"] = m.Content
			}
			out = append(out, msg)
		default:
			out = append(out, map[string]any{"role": string(m.Role), "content": m.Content})
		}
	}
	return out
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	model := c.model(req)
	if model == "" {
		return nil, fmt.Errorf("%s: no model configured", c.ID)
	}

	body := map[string]any{
		"model":          model,
		"messages":       buildMessages(req.System, req.Messages),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": t.Name, "description": t.Description, "parameters": t.Schema,
				},
			})
		}
		body["tools"] = tools
	}
	if req.Effort != "" {
		body["reasoning_effort"] = string(req.Effort)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)

	res, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		text, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("%s returned %d: %s", c.ID, res.StatusCode, strings.TrimSpace(string(text)))
	}

	out := make(chan provider.Chunk)
	go func() {
		defer close(out)
		defer res.Body.Close()
		acc := map[int]*provider.ToolCall{}
		var order []int
		var usage *provider.Usage
		err := provider.SSEData(res.Body, func(data string) bool {
			if data == "[DONE]" {
				return false
			}
			var ev struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				return true
			}
			if ev.Error != nil {
				out <- provider.Chunk{Err: fmt.Errorf("%s: %s", c.ID, ev.Error.Message)}
				return false
			}
			if ev.Usage != nil {
				usage = &provider.Usage{
					InputTokens:  ev.Usage.PromptTokens,
					OutputTokens: ev.Usage.CompletionTokens,
				}
			}
			if len(ev.Choices) == 0 {
				return true
			}
			d := ev.Choices[0].Delta
			if d.Content != "" {
				out <- provider.Chunk{Text: d.Content}
			}
			for _, tc := range d.ToolCalls {
				call := acc[tc.Index]
				if call == nil {
					call = &provider.ToolCall{}
					acc[tc.Index] = call
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Name = tc.Function.Name
				}
				call.Input = append(call.Input, tc.Function.Arguments...)
			}
			return true
		})
		if err != nil {
			out <- provider.Chunk{Err: err}
			return
		}
		calls := make([]provider.ToolCall, 0, len(order))
		for _, i := range order {
			c := acc[i]
			if len(c.Input) == 0 {
				c.Input = json.RawMessage("{}")
			}
			calls = append(calls, *c)
		}
		out <- provider.Chunk{ToolCalls: calls, Usage: usage, Done: true}
	}()
	return out, nil
}
