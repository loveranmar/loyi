package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/tool"
)

// scriptProvider returns a pre-baked sequence of turns, one per Stream call.
type scriptProvider struct {
	turns [][]provider.Chunk
	n     int
	last  provider.Request
}

func (p *scriptProvider) Name() string { return "script" }
func (p *scriptProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.last = req
	ch := make(chan provider.Chunk, 8)
	turn := p.turns[p.n]
	p.n++
	go func() {
		for _, c := range turn {
			ch <- c
		}
		close(ch)
	}()
	return ch, nil
}

func newTestSession(t *testing.T, root string, turns [][]provider.Chunk) *Session {
	t.Helper()
	ws, err := tool.NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry(
		&tool.ReadTool{WS: ws}, &tool.WriteTool{WS: ws}, &tool.EditTool{WS: ws},
		&tool.TreeTool{WS: ws}, &tool.LsTool{WS: ws}, &tool.GlobTool{WS: ws},
		&tool.GrepTool{WS: ws}, &tool.RunTool{WS: ws},
	)
	return &Session{
		Provider:  &scriptProvider{turns: turns},
		Tools:     reg,
		Agent:     AgentByID("build"),
		Workspace: root,
	}
}

func collect(s *Session, input string, approve bool) []Event {
	reply := ReplyDeny
	if approve {
		reply = ReplyAllow
	}
	return collectReply(s, input, reply)
}

func collectReply(s *Session, input string, reply PermissionReply) []Event {
	var events []Event
	s.Run(context.Background(), input, func(e Event) {
		if pe, ok := e.(PermissionEvent); ok {
			pe.Reply <- reply
			events = append(events, e)
			return
		}
		events = append(events, e)
	})
	return events
}

func TestWriteToolFlowApproved(t *testing.T) {
	dir := t.TempDir()
	call := provider.ToolCall{
		ID: "t1", Name: "write",
		Input: json.RawMessage(`{"path":"hello.txt","content":"hi there\n"}`),
	}
	turns := [][]provider.Chunk{
		{{Text: "Creating the file."}, {Done: true, ToolCalls: []provider.ToolCall{call}}},
		{{Text: "Done — wrote hello.txt."}, {Done: true}},
	}
	s := newTestSession(t, dir, turns)
	events := collect(s, "make a hello file", true)

	// permission was requested
	if !hasEvent[PermissionEvent](events) {
		t.Fatal("expected a permission prompt for write")
	}
	// file actually got written
	got, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil || string(got) != "hi there\n" {
		t.Fatalf("file not written correctly: %q err=%v", got, err)
	}
	if !hasEvent[DoneEvent](events) {
		t.Error("expected a done event")
	}
	if s.Usage().ToolCalls != 1 {
		t.Errorf("tool calls = %d, want 1", s.Usage().ToolCalls)
	}
}

func TestWriteToolFlowDenied(t *testing.T) {
	dir := t.TempDir()
	call := provider.ToolCall{
		ID: "t1", Name: "write",
		Input: json.RawMessage(`{"path":"nope.txt","content":"x"}`),
	}
	turns := [][]provider.Chunk{
		{{Done: true, ToolCalls: []provider.ToolCall{call}}},
		{{Text: "Okay, I won't."}, {Done: true}},
	}
	s := newTestSession(t, dir, turns)
	collect(s, "write a file", false)

	if _, err := os.Stat(filepath.Join(dir, "nope.txt")); !os.IsNotExist(err) {
		t.Error("file should not exist after denial")
	}
	// the model should have been told it was declined
	if !strings.Contains(lastToolResult(s), "declined") {
		t.Error("expected a 'declined' tool result fed back to the model")
	}
}

func writeCall(path string) provider.ToolCall {
	return provider.ToolCall{
		ID: "t1", Name: "write",
		Input: json.RawMessage(`{"path":"` + path + `","content":"x"}`),
	}
}

func writeTurns(path string) [][]provider.Chunk {
	return [][]provider.Chunk{
		{{Done: true, ToolCalls: []provider.ToolCall{writeCall(path)}}},
		{{Text: "done."}, {Done: true}},
	}
}

func TestAllowRuleSkipsPrompt(t *testing.T) {
	dir := t.TempDir()
	s := newTestSession(t, dir, writeTurns("index.html"))
	s.Settings = config.DefaultSettings()
	s.Settings.Permissions.Allow = []string{"write:*.html"}

	events := collect(s, "write it", false) // any prompt would be denied
	if hasEvent[PermissionEvent](events) {
		t.Error("allow rule should skip the prompt")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Error("file should have been written without asking")
	}
}

func TestDenyRuleBlocksWithoutPrompt(t *testing.T) {
	dir := t.TempDir()
	s := newTestSession(t, dir, writeTurns("prod.env"))
	s.Settings = config.DefaultSettings()
	s.Settings.Permissions.Deny = []string{"write:*.env"}

	events := collect(s, "write it", true) // any prompt would be approved
	if hasEvent[PermissionEvent](events) {
		t.Error("deny rule should skip the prompt")
	}
	if _, err := os.Stat(filepath.Join(dir, "prod.env")); !os.IsNotExist(err) {
		t.Error("file should not exist")
	}
	if !strings.Contains(lastToolResult(s), "blocked") {
		t.Error("the model should be told the call was blocked")
	}
}

func TestReadonlyModeBlocksMutations(t *testing.T) {
	dir := t.TempDir()
	s := newTestSession(t, dir, writeTurns("a.txt"))
	s.Settings = config.DefaultSettings()
	s.Settings.Permissions.Mode = "readonly"

	events := collect(s, "write it", true)
	if hasEvent[PermissionEvent](events) {
		t.Error("readonly mode should not prompt")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Error("file should not exist in readonly mode")
	}
}

func TestAlwaysReplyRecordsRule(t *testing.T) {
	dir := t.TempDir()
	s := newTestSession(t, dir, writeTurns("index.html"))
	s.Settings = config.DefaultSettings()

	collectReply(s, "write it", ReplyAlways)
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Error("always should approve the call")
	}
	if s.Settings.Decide("write", "other.html") != config.Allow {
		t.Errorf("always should record write:*.html, allow = %v", s.Settings.Permissions.Allow)
	}
}

func TestReadToolNoPermission(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line one\nline two\n"), 0o644)
	call := provider.ToolCall{ID: "r1", Name: "read", Input: json.RawMessage(`{"path":"a.txt"}`)}
	turns := [][]provider.Chunk{
		{{Done: true, ToolCalls: []provider.ToolCall{call}}},
		{{Text: "It has two lines."}, {Done: true}},
	}
	s := newTestSession(t, dir, turns)
	events := collect(s, "read a.txt", false)

	if hasEvent[PermissionEvent](events) {
		t.Error("read should not require permission")
	}
	if !strings.Contains(lastToolResult(s), "line one") {
		t.Error("read output not fed back")
	}
}

func TestUsageReported(t *testing.T) {
	dir := t.TempDir()
	turns := [][]provider.Chunk{{
		{Text: "hi"},
		{Done: true, Usage: &provider.Usage{InputTokens: 120, OutputTokens: 8, CacheRead: 40}},
	}}
	s := newTestSession(t, dir, turns)
	collect(s, "hello", false)
	in, out, est := s.Usage().Tokens()
	if est {
		t.Error("usage should be reported, not estimated")
	}
	if in != 120 || out != 8 || s.Usage().CacheRead != 40 {
		t.Errorf("usage = in:%d out:%d cache:%d", in, out, s.Usage().CacheRead)
	}
}

func TestUsageEstimatedFallback(t *testing.T) {
	dir := t.TempDir()
	turns := [][]provider.Chunk{{{Text: "12345678"}, {Done: true}}} // no Usage
	s := newTestSession(t, dir, turns)
	collect(s, "abcd", false)
	_, out, est := s.Usage().Tokens()
	if !est {
		t.Error("usage should fall back to estimate")
	}
	if out != 2 { // 8 chars / 4
		t.Errorf("estimated out = %d, want 2", out)
	}
}

func TestToolsAdvertisedToProvider(t *testing.T) {
	dir := t.TempDir()
	turns := [][]provider.Chunk{{{Text: "hi"}, {Done: true}}}
	s := newTestSession(t, dir, turns)
	collect(s, "hello", false)
	sp := s.Provider.(*scriptProvider)
	if len(sp.last.Tools) != 8 {
		t.Errorf("advertised %d tools, want 8", len(sp.last.Tools))
	}
	if !strings.Contains(sp.last.System, "loyi") {
		t.Error("system prompt should mention loyi")
	}
}

// helpers

func hasEvent[T Event](events []Event) bool {
	for _, e := range events {
		if _, ok := e.(T); ok {
			return true
		}
	}
	return false
}

func lastToolResult(s *Session) string {
	for i := len(s.history) - 1; i >= 0; i-- {
		if s.history[i].ToolCallID != "" {
			return s.history[i].ToolResult
		}
	}
	return ""
}

func TestCapToolOutput(t *testing.T) {
	small := "just a little output"
	if got := capToolOutput(small); got != small {
		t.Errorf("small output should pass through unchanged")
	}
	big := strings.Repeat("x", maxToolOutputBytes+5000)
	got := capToolOutput(big)
	if len(got) >= len(big) {
		t.Errorf("oversized output should be truncated: len %d", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncated output should carry a note, got tail: %q", got[len(got)-60:])
	}
}
