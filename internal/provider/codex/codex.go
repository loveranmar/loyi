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

	input := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		contentType := "input_text"
		if m.Role == provider.RoleAssistant {
			contentType = "output_text"
		}
		input = append(input, map[string]any{
			"type": "message",
			"role": string(m.Role),
			"content": []map[string]string{
				{"type": contentType, "text": m.Content},
			},
		})
	}

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
		err := provider.SSEData(res.Body, func(data string) bool {
			var ev struct {
				Type     string `json:"type"`
				Delta    string `json:"delta"`
				Response struct {
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
					out <- provider.Chunk{Text: ev.Delta}
				}
			case "response.failed":
				msg := "request failed"
				if ev.Response.Error != nil {
					msg = ev.Response.Error.Message
				}
				out <- provider.Chunk{Err: fmt.Errorf("chatgpt: %s", msg)}
				return false
			case "response.completed", "response.done":
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
