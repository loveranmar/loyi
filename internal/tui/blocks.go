package tui

// Tool blocks: each executed tool action in the current round renders as a
// result line that can expand — collapsed → peek (first lines) → full
// (everything, scrolling in a viewport when it overflows). Blocks live in the
// managed view until the next turn starts, then flush to scrollback collapsed
// so the terminal history stays clean.

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/loveranmar/loyi/internal/agent"
)

type blockState int

const (
	blockCollapsed blockState = iota
	blockPeek
	blockFull
)

// peekLines is how much content the peek state shows.
const peekLines = 5

type toolBlock struct {
	name    string   // tool name: write, edit, run, …
	label   string   // result-line label: "index.html", "ran npm test"
	detail  string   // dim note: "1 line", "exit 0"
	ok      bool     // ✓ accent vs ✗ terracotta
	content []string // expandable lines; empty = plain line, not focusable
	diff    bool     // color content as a diff (+/- lines)
	state   blockState
	vp      viewport.Model
}

func (b *toolBlock) focusable() bool { return len(b.content) > 0 }

// cycle advances collapsed → peek → full → collapsed, skipping peek when the
// content already fits in a peek.
func (b *toolBlock) cycle(c *Chat) {
	switch b.state {
	case blockCollapsed:
		if len(b.content) <= peekLines {
			b.state = blockFull
		} else {
			b.state = blockPeek
		}
	case blockPeek:
		b.state = blockFull
	default:
		b.state = blockCollapsed
	}
	if b.state == blockFull {
		c.fitViewport(b)
	}
}

// hint names the next action in the cycle.
func (b *toolBlock) hint() string {
	switch b.state {
	case blockCollapsed:
		return "↵ expand"
	case blockPeek:
		return "↵ full"
	default:
		return "↵ collapse"
	}
}

// overflows reports whether full content needs viewport scrolling.
func (b *toolBlock) overflows() bool {
	return len(b.content) > b.vp.Height()
}

// newBlock turns a tool result event into a block. Which tools expand is
// decided by the agent side: events carrying a Display payload do.
func (c *Chat) newBlock(e agent.ToolResultEvent) *toolBlock {
	b := &toolBlock{name: e.Name, ok: !e.IsError}
	label := c.toolTarget
	if label == "" {
		label = e.Name
	}
	if e.Name == "run" {
		label = "ran " + label
	}
	if len(label) > 48 {
		label = label[:48] + "…"
	}
	b.label = label

	switch {
	case e.IsError:
		b.detail = firstLine(e.Output)
	case e.Display != nil:
		b.detail = e.Display.Detail
		b.ok = e.Display.OK
		if e.Display.Content != "" {
			b.content = strings.Split(strings.TrimRight(e.Display.Content, "\n"), "\n")
		}
		b.diff = e.Name == "write" || e.Name == "edit"
	default:
		b.detail = toolDetail(e.Name, e.Output)
	}
	return b
}

// blockView renders a block. The focused block gets an accent left marker and
// a right-aligned dim hint for the next action.
func (c *Chat) blockView(b *toolBlock, focused bool) string {
	mark, markStyle := "✓", c.s.Accent
	if !b.ok {
		mark, markStyle = "✗", c.s.Danger
	}
	lead := toolIndent()
	if focused {
		lead = strings.Repeat(" ", pad) + c.s.Accent.Render("▎") + " "
	}
	line := lead + markStyle.Render(mark) + " " + c.s.Text.Render(b.label) + c.s.Dim.Render(" · "+b.detail)
	if focused && b.focusable() {
		hint := c.s.Dim.Render(b.hint())
		gap := pad + 1 + c.boxContentWidth() - lipgloss.Width(line) - lipgloss.Width(hint)
		if gap < 2 {
			gap = 2
		}
		line += strings.Repeat(" ", gap) + hint
	}
	if b.state == blockCollapsed || !b.focusable() {
		return line
	}

	var body string
	switch b.state {
	case blockPeek:
		lines := c.gutter(b.content[:peekLines], b.diff)
		more := len(b.content) - peekLines
		lines = append(lines, c.gutterRaw(c.s.Dim.Render(fmt.Sprintf("… +%d more lines", more))))
		body = strings.Join(lines, "\n")
	default: // full
		if b.overflows() {
			body = b.vp.View()
		} else {
			body = strings.Join(c.gutter(b.content, b.diff), "\n")
		}
	}
	return line + "\n" + body
}

// gutter prefixes content lines with the dim │ gutter and styles them: diff
// lines get accent/terracotta, everything else primary text.
func (c *Chat) gutter(lines []string, diff bool) []string {
	out := make([]string, len(lines))
	for i, ln := range lines {
		style := c.s.Text
		if diff {
			switch {
			case strings.HasPrefix(ln, "+"):
				style = c.s.Accent
			case strings.HasPrefix(ln, "-"):
				style = c.s.Danger
			default:
				style = c.s.Dim
			}
		}
		out[i] = c.gutterRaw(style.Render(ln))
	}
	return out
}

// gutterRaw wraps one already-styled line in the gutter.
func (c *Chat) gutterRaw(styled string) string {
	return toolIndent() + "  " + c.s.Dim.Render("│") + " " + styled
}

// fitViewport prepares a block's viewport for the full state, sized to the
// terminal with room for the input area.
func (c *Chat) fitViewport(b *toolBlock) {
	h := c.height - 14
	if h < 5 {
		h = 5
	}
	if h > len(b.content) {
		h = len(b.content)
	}
	b.vp = viewport.New()
	b.vp.SetWidth(pad + 1 + c.boxContentWidth())
	b.vp.SetHeight(h)
	b.vp.SetContent(strings.Join(c.gutter(b.content, b.diff), "\n"))
}

// --- live turn transcript ---

// turnItem is one element of the current round: either pre-rendered text
// (a loyi message, an error line) or a tool block.
type turnItem struct {
	text  string
	block *toolBlock
}

func (c *Chat) appendText(rendered string) {
	c.items = append(c.items, turnItem{text: rendered})
}

func (c *Chat) appendBlock(b *toolBlock) {
	c.items = append(c.items, turnItem{block: b})
}

// focusableItems lists item indices holding expandable blocks.
func (c *Chat) focusableItems() []int {
	var out []int
	for i, it := range c.items {
		if it.block != nil && it.block.focusable() {
			out = append(out, i)
		}
	}
	return out
}

func (c *Chat) focusedBlock() *toolBlock {
	if c.focus < 0 || c.focus >= len(c.items) || c.items[c.focus].block == nil {
		return nil
	}
	return c.items[c.focus].block
}
