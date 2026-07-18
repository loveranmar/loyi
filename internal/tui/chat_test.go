package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/theme"
	"github.com/loveranmar/loyi/internal/tool"
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

func TestBlockCollapsedLines(t *testing.T) {
	c := testChat()
	c.toolTarget = "index.html"
	b := c.newBlock(agent.ToolResultEvent{Name: "write",
		Display: &tool.DisplayInfo{Content: "+ hi", Detail: "1 line", OK: true}})
	if got := stripANSI(c.blockView(b, false)); got != "    ✓ index.html · 1 line" {
		t.Errorf("write block = %q", got)
	}
	be := c.newBlock(agent.ToolResultEvent{Name: "edit", Output: "boom", IsError: true})
	if got := stripANSI(c.blockView(be, false)); got != "    ✗ index.html · boom" {
		t.Errorf("error block = %q", got)
	}
	c.toolTarget = "npm test"
	br := c.newBlock(agent.ToolResultEvent{Name: "run",
		Display: &tool.DisplayInfo{Content: "out", Detail: "exit 1", OK: false}})
	if got := stripANSI(c.blockView(br, false)); got != "    ✗ ran npm test · exit 1" {
		t.Errorf("run block = %q", got)
	}
}

func TestBlockCycleAndPeek(t *testing.T) {
	c := testChat()
	c.toolTarget = "main.go"
	content := "+ l1\n+ l2\n+ l3\n+ l4\n+ l5\n+ l6\n+ l7\n+ l8"
	b := c.newBlock(agent.ToolResultEvent{Name: "write",
		Display: &tool.DisplayInfo{Content: content, Detail: "8 lines", OK: true}})

	if b.state != blockCollapsed {
		t.Fatal("blocks default to collapsed")
	}
	b.cycle(c)
	if b.state != blockPeek {
		t.Fatal("large content should peek first")
	}
	view := stripANSI(c.blockView(b, true))
	if !strings.Contains(view, "l5") || strings.Contains(view, "l6") {
		t.Errorf("peek should show exactly %d lines:\n%s", peekLines, view)
	}
	if !strings.Contains(view, "+3 more lines") {
		t.Errorf("peek should hint remaining lines:\n%s", view)
	}
	if !strings.Contains(view, "↵ full") {
		t.Errorf("focused peek should hint the next action:\n%s", view)
	}
	b.cycle(c)
	if b.state != blockFull {
		t.Fatal("peek should cycle to full")
	}
	if v := stripANSI(c.blockView(b, true)); !strings.Contains(v, "l8") || !strings.Contains(v, "↵ collapse") {
		t.Errorf("full should show everything + collapse hint:\n%s", v)
	}
	b.cycle(c)
	if b.state != blockCollapsed {
		t.Fatal("full should cycle back to collapsed")
	}

	// small content skips peek
	small := c.newBlock(agent.ToolResultEvent{Name: "write",
		Display: &tool.DisplayInfo{Content: "+ a\n+ b", Detail: "2 lines", OK: true}})
	small.cycle(c)
	if small.state != blockFull {
		t.Error("content within a peek should expand straight to full")
	}
}

func TestFocusNavigation(t *testing.T) {
	c := testChat()
	c.toolTarget = "a.html"
	c.appendText("text")
	b1 := c.newBlock(agent.ToolResultEvent{Name: "write",
		Display: &tool.DisplayInfo{Content: "+ a", Detail: "1 line", OK: true}})
	c.appendBlock(b1)
	c.toolTarget = "b.html"
	b2 := c.newBlock(agent.ToolResultEvent{Name: "write",
		Display: &tool.DisplayInfo{Content: "+ b", Detail: "1 line", OK: true}})
	c.appendBlock(b2)

	c.focusLastBlock()
	if c.focusedBlock() != b2 {
		t.Fatal("up from input should focus the last block")
	}
	c.handleBlockKey("k")
	if c.focusedBlock() != b1 {
		t.Fatal("k should move focus up")
	}
	c.handleBlockKey("up") // already first — stays
	if c.focusedBlock() != b1 {
		t.Fatal("focus should stay on the first block")
	}
	c.handleBlockKey("j")
	c.handleBlockKey("down")
	if c.focus != -1 {
		t.Fatal("down past the last block should return to the input")
	}

	c.focusLastBlock()
	c.handleBlockKey("enter")
	if b2.state != blockFull {
		t.Error("enter should cycle the focused block")
	}
	c.handleBlockKey("esc")
	if b2.state != blockCollapsed || c.focus < 0 {
		t.Error("esc should collapse first, keeping focus")
	}
	c.handleBlockKey("esc")
	if c.focus != -1 {
		t.Error("esc on a collapsed block should return to the input")
	}
}

func TestFlushTurnCollapsesBlocks(t *testing.T) {
	c := testChat()
	c.toolTarget = "a.html"
	b := c.newBlock(agent.ToolResultEvent{Name: "write",
		Display: &tool.DisplayInfo{Content: "+ a\n+ b", Detail: "2 lines", OK: true}})
	b.state = blockFull
	c.appendBlock(b)
	c.focusLastBlock()

	cmds := c.flushTurn()
	if len(cmds) != 1 || len(c.items) != 0 || c.focus != -1 {
		t.Fatal("flush should clear the round and reset focus")
	}
	if b.state != blockCollapsed {
		t.Error("flushed blocks must collapse")
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
