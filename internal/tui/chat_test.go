package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/theme"
)

func testChat() *Chat {
	cfg := &config.Config{Theme: theme.Default.Name}
	sess := &agent.Session{Agent: agent.Agents[1]}
	c := NewChat(cfg, nil, sess, theme.Default)
	c.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	return c
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case inEsc:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// The input box must stay one content row tall — if the input is set wider
// than the box interior, lipgloss wraps it and the box grows an empty row.
func TestInputBoxSingleRow(t *testing.T) {
	c := testChat()
	box := stripANSI(c.inputBox())
	if n := strings.Count(box, "\n"); n != 2 {
		t.Fatalf("input box should be 3 lines (1 content row), got %d:\n%s", n+1, box)
	}
}

func TestTurnFormatting(t *testing.T) {
	c := testChat()
	if got := stripANSI(c.userLine("hi")); got != "   › hi " {
		t.Errorf("user turn = %q, want %q", got, "   › hi ")
	}
	if got := stripANSI(c.loyiLine("done.\nnext?")); got != "  ▸ done.\n    next?" {
		t.Errorf("loyi turn = %q, want aligned continuation, got %q", got, got)
	}
}

func TestToolResultLine(t *testing.T) {
	c := testChat()
	c.toolTarget = "index.html"
	got := stripANSI(c.toolResultLine(agent.ToolResultEvent{Name: "write", Output: "wrote index.html · 1 line"}))
	if got != "    ✓ index.html · 1 line" {
		t.Errorf("tool result = %q, want %q", got, "    ✓ index.html · 1 line")
	}
	got = stripANSI(c.toolResultLine(agent.ToolResultEvent{Name: "read", Output: "a\nb\nc"}))
	if got != "    ✓ index.html · 3 lines" {
		t.Errorf("tool result = %q, want %q", got, "    ✓ index.html · 3 lines")
	}
	got = stripANSI(c.toolResultLine(agent.ToolResultEvent{Name: "edit", Output: "boom", IsError: true}))
	if got != "    ✗ index.html · boom" {
		t.Errorf("tool error = %q, want %q", got, "    ✗ index.html · boom")
	}
}

func TestCountNoun(t *testing.T) {
	if got := countNoun(1, "line", "lines"); got != "1 line" {
		t.Errorf("countNoun(1) = %q, want %q", got, "1 line")
	}
	if got := countNoun(3, "line", "lines"); got != "3 lines" {
		t.Errorf("countNoun(3) = %q, want %q", got, "3 lines")
	}
}
