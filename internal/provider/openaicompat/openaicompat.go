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

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	model := c.model(req)
	if model == "" {
		return nil, fmt.Errorf("%s: no model configured", c.ID)
	}

	msgs := make([]map[string]string, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}

	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
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
		err := provider.SSEData(res.Body, func(data string) bool {
			if data == "[DONE]" {
				return false
			}
			var ev struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
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
			if len(ev.Choices) > 0 && ev.Choices[0].Delta.Content != "" {
				out <- provider.Chunk{Text: ev.Choices[0].Delta.Content}
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
