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

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	oauth := c.APIKey == ""

	var system []textBlock
	if oauth {
		system = append(system, textBlock{Type: "text", Text: claudeCodeSystem})
	}
	if req.System != "" {
		system = append(system, textBlock{Type: "text", Text: req.System})
	}

	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}

	body := map[string]any{
		"model":      c.model(req),
		"max_tokens": 32000,
		"stream":     true,
		"messages":   msgs,
	}
	if len(system) > 0 {
		body["system"] = system
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
		err := provider.SSEData(res.Body, func(data string) bool {
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				return true
			}
			switch ev.Type {
			case "content_block_delta":
				if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
					out <- provider.Chunk{Text: ev.Delta.Text}
				}
			case "error":
				out <- provider.Chunk{Err: fmt.Errorf("anthropic: %s", ev.Error.Message)}
				return false
			case "message_stop":
				return false
			}
			return true
		})
		if err != nil {
			out <- provider.Chunk{Err: err}
			return
		}
		out <- provider.Chunk{Done: true}
	}()
	return out, nil
}
