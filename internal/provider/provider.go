// Package provider defines the backend-agnostic model interface.
// Every model backend (anthropic, openai, openrouter, ...) implements
// Provider; the rest of loyi never talks to a vendor SDK directly.
package provider

import (
	"context"
	"encoding/json"
)

// Effort is the unified reasoning-effort control. Each backend maps it to
// its own knob (e.g. an explicit reasoning_effort parameter, or a thinking
// token budget).
type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ToolDef describes a tool the model may call. Schema is a JSON Schema object
// for the tool's input.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ToolCall is a single tool invocation the model asked for.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// Message is one turn in the conversation. It carries plain text, and/or
// tool calls (assistant) or a tool result (a result turn).
type Message struct {
	Role    Role
	Content string

	// Assistant tool calls, when the model decided to use tools.
	ToolCalls []ToolCall

	// A tool result. When ToolCallID is set, this message reports the output
	// of an earlier tool call back to the model.
	ToolCallID string
	ToolResult string
	IsError    bool
}

// UserText builds a plain user message.
func UserText(s string) Message { return Message{Role: RoleUser, Content: s} }

// ToolResultMsg builds a message reporting a tool's output.
func ToolResultMsg(id, output string, isErr bool) Message {
	return Message{Role: RoleUser, ToolCallID: id, ToolResult: output, IsError: isErr}
}

// Request is a single completion request, expressed in loyi's own terms.
type Request struct {
	System   string // optional system prompt
	Messages []Message
	Tools    []ToolDef
	Effort   Effort
	Model    string // backend-specific model id; empty means the provider's default
}

// Usage reports token counts for a single model call, when the backend
// provides them.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
}

// Chunk is one piece of a streamed response. Text arrives incrementally;
// ToolCalls and Usage arrive once, on the final Done chunk.
type Chunk struct {
	Text      string
	ToolCalls []ToolCall
	Usage     *Usage
	Done      bool
	Err       error
}

// Provider is a model backend. Implementations live in subpackages of
// internal/provider and register themselves via Register.
type Provider interface {
	// Name is the stable identifier used in config, e.g. "anthropic".
	Name() string
	// Stream runs a completion and streams chunks until Done or Err.
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}

var registry = map[string]Provider{}

// Register makes a provider available by name. Called from each backend's
// package init or explicit setup.
func Register(p Provider) {
	registry[p.Name()] = p
}

// Get returns the provider registered under name, or nil.
func Get(name string) Provider {
	return registry[name]
}
