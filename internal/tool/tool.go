// Package tool holds the tools the agent executes: read, write, edit, tree,
// grep, and run. Each tool declares whether it mutates state so the agent can
// gate destructive actions behind a permission prompt.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool is one capability the agent can invoke.
type Tool interface {
	Name() string
	Description() string
	// Schema is the JSON Schema for the tool's input object.
	Schema() map[string]any
	// Mutating reports whether a call with these args changes the workspace
	// or runs a command — i.e. whether it needs permission.
	Mutating(input json.RawMessage) bool
	// Summary is a one-line, human-readable description of what this specific
	// call will do, shown in the permission prompt and the transcript.
	Summary(input json.RawMessage) string
	// Run executes the tool and returns output for the model.
	Run(ctx context.Context, input json.RawMessage) (string, error)
}

// AutoSafe is an optional interface for mutating tools. In "auto" permission
// mode, a call is run without asking only when AutoSafe reports true; tools
// that don't implement it are treated as safe to auto-run.
type AutoSafe interface {
	AutoSafe(input json.RawMessage) bool
}

// Registry is an ordered set of tools.
type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range tools {
		r.Add(t)
	}
	return r
}

func (r *Registry) Add(t Tool) {
	if _, ok := r.tools[t.Name()]; !ok {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names returns tool names in registration order.
func (r *Registry) Names() []string {
	out := append([]string(nil), r.order...)
	return out
}

// List returns tools in registration order.
func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.tools[n])
	}
	return out
}

// helper: parse input JSON into v, tolerating empty input.
func parseInput(input json.RawMessage, v any) error {
	if len(input) == 0 {
		return nil
	}
	if err := json.Unmarshal(input, v); err != nil {
		return fmt.Errorf("bad tool input: %w", err)
	}
	return nil
}

// stringField pulls a top-level string field out of raw input for summaries
// without a full unmarshal.
func stringField(input json.RawMessage, key string) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
