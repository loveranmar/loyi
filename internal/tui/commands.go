package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/provider"
)

// runCommand handles a /slash command typed into the chat.
func (c *Chat) runCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(fields[0], "/")
	args := fields[1:]

	switch cmd {
	case "help", "?":
		return c, tea.Println(c.helpText())
	case "clear":
		c.sess.Reset()
		return c, tea.Sequence(tea.ClearScreen, tea.Println(c.banner()))
	case "usage":
		return c, tea.Println(c.usageText())
	case "agent", "agents":
		return c.agentCommand(args)
	case "effort":
		return c.effortCommand(args)
	case "model":
		return c.modelCommand(args)
	case "loop":
		return c, tea.Println(c.s.Dim.Render("  /loop isn't wired yet — it'll let the agent run a task on repeat until a goal is met."))
	case "quit", "exit":
		c.quit = true
		return c, tea.Quit
	default:
		return c, tea.Println(c.s.Dim.Render("  unknown command: /" + cmd + "  ·  try /help"))
	}
}

func (c *Chat) helpText() string {
	lines := []struct{ cmd, desc string }{
		{"/help", "show this list"},
		{"/agent [id]", "switch persona: plan · build · ship (no id lists them)"},
		{"/effort [low|medium|high]", "reasoning effort (no arg shows current)"},
		{"/model [id]", "override the model for this session"},
		{"/usage", "tokens and tool calls this session (estimated)"},
		{"/clear", "clear the conversation and start fresh"},
		{"/loop", "run a task on repeat (coming soon)"},
		{"/quit", "leave loyi"},
	}
	var b strings.Builder
	b.WriteString("\n" + c.s.Text.Render("commands") + "\n")
	for _, l := range lines {
		b.WriteString("  " + c.s.Accent.Render(pad(l.cmd, 26)) + c.s.Dim.Render(l.desc) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *Chat) usageText() string {
	u := c.sess.Usage()
	var b strings.Builder
	b.WriteString("\n" + c.s.Text.Render("session usage") + "\n")
	rows := [][2]string{
		{"turns", fmt.Sprintf("%d", u.Turns)},
		{"tool calls", fmt.Sprintf("%d", u.ToolCalls)},
		{"tokens (est.)", fmt.Sprintf("~%d", u.EstTokens())},
	}
	for _, r := range rows {
		b.WriteString("  " + c.s.Dim.Render(pad(r[0], 16)) + c.s.Text.Render(r[1]) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *Chat) agentCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		var b strings.Builder
		b.WriteString("\n" + c.s.Text.Render("agents") + c.s.Dim.Render("  (you're on "+c.sess.Agent.Label+")") + "\n")
		for _, a := range agent.Agents {
			marker := "  "
			label := c.s.Dim.Render(pad(a.Label, 10))
			if a.ID == c.sess.Agent.ID {
				marker = c.s.Accent.Render("› ")
				label = c.s.Text.Render(pad(a.Label, 10))
			}
			b.WriteString(marker + label + c.s.Dim.Render(a.Tagline) + "\n")
		}
		b.WriteString(c.s.Dim.Render("  switch with /agent <id>"))
		return c, tea.Println(strings.TrimRight(b.String(), "\n"))
	}
	id := args[0]
	for _, a := range agent.Agents {
		if a.ID == id {
			c.sess.SwitchAgent(a)
			return c, tea.Println(c.s.Accent.Render("  → ") + c.s.Text.Render(a.Label) + c.s.Dim.Render(" · "+a.Tagline))
		}
	}
	return c, tea.Println(c.s.Dim.Render("  no agent called " + id + "  ·  try /agent to list them"))
}

func (c *Chat) effortCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		cur := string(c.sess.Effort)
		if cur == "" {
			cur = "default"
		}
		return c, tea.Println(c.s.Dim.Render("  effort: ") + c.s.Text.Render(cur))
	}
	switch args[0] {
	case "low", "medium", "high":
		c.sess.Effort = provider.Effort(args[0])
		return c, tea.Println(c.s.Accent.Render("  → ") + c.s.Text.Render("effort "+args[0]))
	default:
		return c, tea.Println(c.s.Dim.Render("  effort must be low, medium, or high"))
	}
}

func (c *Chat) modelCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		cur := c.sess.Model
		if cur == "" {
			cur = "provider default"
		}
		return c, tea.Println(c.s.Dim.Render("  model: ") + c.s.Text.Render(cur))
	}
	c.sess.Model = args[0]
	return c, tea.Println(c.s.Accent.Render("  → ") + c.s.Text.Render("model "+args[0]))
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
