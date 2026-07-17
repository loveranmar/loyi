// Package agent is loyi's agent loop: plan → act → observe. It drives a
// provider with a set of tools, executing tool calls (gating the destructive
// ones behind a permission prompt) until the model stops asking for tools.
package agent

import (
	"context"
	"fmt"

	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/tool"
)

// Event is something that happened during a turn, streamed to the UI.
type Event interface{ isEvent() }

type TextEvent struct{ Text string }
type ToolStartEvent struct {
	Name    string
	Summary string
}
type ToolResultEvent struct {
	Name    string
	Output  string
	IsError bool
}

// PermissionEvent asks the UI to approve a mutating tool call. The UI must
// send the decision on Reply exactly once.
type PermissionEvent struct {
	Summary string
	Reply   chan bool
}
type DoneEvent struct{}
type ErrorEvent struct{ Err error }

func (TextEvent) isEvent()       {}
func (ToolStartEvent) isEvent()  {}
func (ToolResultEvent) isEvent() {}
func (PermissionEvent) isEvent() {}
func (DoneEvent) isEvent()       {}
func (ErrorEvent) isEvent()      {}

// Usage accounts for a session's model traffic. Token counts come from the
// provider when it reports them; otherwise a chars/4 estimate is used.
type Usage struct {
	Turns     int
	ToolCalls int

	InputTokens  int
	OutputTokens int
	CacheRead    int
	Reported     bool // true once a provider reported real token counts

	charsIn  int
	charsOut int
}

// Tokens returns the input and output token totals and whether they are real
// (provider-reported) or estimated.
func (u Usage) Tokens() (in, out int, estimated bool) {
	if u.Reported {
		return u.InputTokens, u.OutputTokens, false
	}
	return u.charsIn / 4, u.charsOut / 4, true
}

// AutoApprove, when true, skips the permission prompt for mutating tools.
type Session struct {
	Provider    provider.Provider
	Tools       *tool.Registry
	Agent       Agent
	Effort      provider.Effort
	Model       string
	AutoApprove bool

	Workspace string
	history   []provider.Message
	usage     Usage
}

func (s *Session) Usage() Usage { return s.usage }

// SwitchAgent changes the active persona for subsequent turns.
func (s *Session) SwitchAgent(a Agent) { s.Agent = a }

// Reset clears the conversation history (keeps config).
func (s *Session) Reset() {
	s.history = nil
	s.usage = Usage{}
}

func (s *Session) system() string {
	return BuildSystem(s.Agent, s.Workspace, s.Tools.Names())
}

func (s *Session) toolDefs() []provider.ToolDef {
	defs := make([]provider.ToolDef, 0, len(s.Tools.List()))
	for _, t := range s.Tools.List() {
		defs = append(defs, provider.ToolDef{
			Name: t.Name(), Description: t.Description(), Schema: t.Schema(),
		})
	}
	return defs
}

// Run drives one user turn to completion, emitting events. It blocks until the
// model stops requesting tools, an error occurs, or ctx is cancelled.
func (s *Session) Run(ctx context.Context, input string, emit func(Event)) {
	s.history = append(s.history, provider.UserText(input))
	s.usage.Turns++
	s.usage.charsIn += len(input)

	for {
		if ctx.Err() != nil {
			emit(ErrorEvent{ctx.Err()})
			return
		}
		req := provider.Request{
			System:   s.system(),
			Messages: s.history,
			Tools:    s.toolDefs(),
			Effort:   s.Effort,
			Model:    s.Model,
		}
		ch, err := s.Provider.Stream(ctx, req)
		if err != nil {
			emit(ErrorEvent{err})
			return
		}

		var text string
		var calls []provider.ToolCall
		for chunk := range ch {
			if chunk.Err != nil {
				emit(ErrorEvent{chunk.Err})
				return
			}
			if chunk.Text != "" {
				text += chunk.Text
				emit(TextEvent{chunk.Text})
			}
			if chunk.ServerTool != nil {
				s.usage.ToolCalls++
				emit(ToolStartEvent{Name: chunk.ServerTool.Name, Summary: serverToolSummary(chunk.ServerTool)})
			}
			if chunk.Done {
				calls = chunk.ToolCalls
				if chunk.Usage != nil {
					s.usage.Reported = true
					s.usage.InputTokens += chunk.Usage.InputTokens
					s.usage.OutputTokens += chunk.Usage.OutputTokens
					s.usage.CacheRead += chunk.Usage.CacheRead
				}
			}
		}
		s.usage.charsOut += len(text)

		// Record the assistant turn (text + any tool calls).
		s.history = append(s.history, provider.Message{
			Role: provider.RoleAssistant, Content: text, ToolCalls: calls,
		})

		if len(calls) == 0 {
			emit(DoneEvent{})
			return
		}

		// Execute each tool call, gating mutating ones.
		for _, tc := range calls {
			s.usage.ToolCalls++
			t, ok := s.Tools.Get(tc.Name)
			if !ok {
				s.appendToolResult(tc.ID, fmt.Sprintf("unknown tool %q", tc.Name), true, emit)
				continue
			}
			summary := t.Summary(tc.Input)
			emit(ToolStartEvent{Name: tc.Name, Summary: summary})

			if t.Mutating(tc.Input) && !s.AutoApprove {
				reply := make(chan bool, 1)
				emit(PermissionEvent{Summary: summary, Reply: reply})
				var approved bool
				select {
				case approved = <-reply:
				case <-ctx.Done():
					emit(ErrorEvent{ctx.Err()})
					return
				}
				if !approved {
					s.appendToolResult(tc.ID, "the user declined this action. stop and ask what they'd like to do instead.", true, emit)
					continue
				}
			}

			out, err := t.Run(ctx, tc.Input)
			if err != nil {
				s.appendToolResult(tc.ID, "error: "+err.Error(), true, emit)
				continue
			}
			s.appendToolResult(tc.ID, out, false, emit)
		}
	}
}

func serverToolSummary(st *provider.ServerTool) string {
	verb := "search"
	if st.Name == "web_fetch" {
		verb = "fetch"
	}
	if st.Query == "" {
		return verb
	}
	return verb + " " + st.Query
}

func (s *Session) appendToolResult(id, output string, isErr bool, emit func(Event)) {
	s.history = append(s.history, provider.ToolResultMsg(id, output, isErr))
	s.usage.charsIn += len(output)
	// find the tool name for the event by matching the last assistant call
	name := ""
	for i := len(s.history) - 1; i >= 0; i-- {
		for _, c := range s.history[i].ToolCalls {
			if c.ID == id {
				name = c.Name
			}
		}
		if name != "" {
			break
		}
	}
	emit(ToolResultEvent{Name: name, Output: output, IsError: isErr})
}
