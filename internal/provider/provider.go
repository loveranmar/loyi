// Package provider defines the backend-agnostic model interface.
// Every model backend (anthropic, openai, openrouter, ...) implements
// Provider; the rest of loyi never talks to a vendor SDK directly.
package provider

import "context"

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

type Message struct {
	Role    Role
	Content string
}

// Request is a single completion request, expressed in loyi's own terms.
type Request struct {
	System   string // optional system prompt
	Messages []Message
	Effort   Effort
	Model    string // backend-specific model id; empty means the provider's default
}

// Chunk is one piece of a streamed response.
type Chunk struct {
	Text string
	Done bool
	Err  error
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
