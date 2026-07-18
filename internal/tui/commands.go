package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/provider"
)

// runCommand handles a /slash command typed into the chat. The live round
// flushes to scrollback first so command output lands below it.
func (c *Chat) runCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(fields[0], "/")
	args := fields[1:]

	if flush := c.flushTurn(); len(flush) > 0 {
		model, next := c.runCommand2(cmd, args)
		return model, tea.Sequence(append(flush, next)...)
	}
	return c.runCommand2(cmd, args)
}

func (c *Chat) runCommand2(cmd string, args []string) (tea.Model, tea.Cmd) {
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
	case "model", "models":
		return c.modelCommand(args)
	case "connect", "login", "provider":
		return c, c.connect()
	case "loop":
		return c.loopCommand(args)
	case "quit", "exit":
		c.quit = true
		return c, tea.Quit
	default:
		return c, tea.Println(indent(c.s.Dim.Render("unknown command: /" + cmd + "  ·  try /help")))
	}
}

func (c *Chat) helpText() string {
	lines := []struct{ cmd, desc string }{
		{"/help", "show this list"},
		{"/agent [id]", "switch persona: plan · build · ship (no id lists them)"},
		{"/effort [low|medium|high]", "reasoning effort (no arg shows current)"},
		{"/model [id]", "pick a model (no id opens the picker across all providers)"},
		{"/connect", "connect another provider (claude, chatgpt, api key, custom)"},
		{"/usage", "tokens and tool calls this session (estimated)"},
		{"/clear", "clear the conversation and start fresh"},
		{"/loop <n> <task>", "run a task, repeating up to n× until the agent says DONE"},
		{"/quit", "leave loyi"},
	}
	var b strings.Builder
	b.WriteString("\n" + c.s.Text.Render("commands") + "\n")
	for _, l := range lines {
		b.WriteString("  " + c.s.Accent.Render(padTo(l.cmd, 26)) + c.s.Dim.Render(l.desc) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *Chat) usageText() string {
	u := c.sess.Usage()
	in, out, est := u.Tokens()
	inLabel, outLabel := "input tokens", "output tokens"
	if est {
		inLabel, outLabel = "input tokens (est.)", "output tokens (est.)"
	}
	var b strings.Builder
	b.WriteString("\n" + c.s.Text.Render("session usage") + "\n")
	rows := [][2]string{
		{"turns", fmt.Sprintf("%d", u.Turns)},
		{"tool calls", fmt.Sprintf("%d", u.ToolCalls)},
		{inLabel, fmt.Sprintf("%d", in)},
		{outLabel, fmt.Sprintf("%d", out)},
	}
	if u.CacheRead > 0 {
		rows = append(rows, [2]string{"cache reads", fmt.Sprintf("%d", u.CacheRead)})
	}
	for _, r := range rows {
		b.WriteString("  " + c.s.Dim.Render(padTo(r[0], 22)) + c.s.Text.Render(r[1]) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (c *Chat) agentCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		var b strings.Builder
		b.WriteString("\n" + c.s.Text.Render("agents") + c.s.Dim.Render("  (you're on "+c.sess.Agent.Label+")") + "\n")
		for _, a := range agent.Agents {
			marker := "  "
			label := c.s.Dim.Render(padTo(a.Label, 10))
			if a.ID == c.sess.Agent.ID {
				marker = c.s.Accent.Render("› ")
				label = c.s.Text.Render(padTo(a.Label, 10))
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

const loopMax = 25

func (c *Chat) loopCommand(args []string) (tea.Model, tea.Cmd) {
	if c.working {
		return c, tea.Println(c.s.Dim.Render("  already working — wait for the current turn to finish"))
	}
	// /loop <count> <task...>
	if len(args) < 2 {
		return c, tea.Println(c.s.Dim.Render("  usage: /loop <count> <task>   e.g. /loop 5 add tests until they all pass"))
	}
	count, err := strconv.Atoi(args[0])
	if err != nil || count < 1 {
		return c, tea.Println(c.s.Dim.Render("  first argument must be a number of iterations, e.g. /loop 5 <task>"))
	}
	if count > loopMax {
		count = loopMax
	}
	task := strings.Join(args[1:], " ")
	c.loopActive = true
	c.loopTotal = count
	c.loopLeft = count

	echo := c.s.Accent.Render("↻") + " " + c.s.Text.Render(task) +
		c.s.Dim.Render(fmt.Sprintf("  (looping up to %d×, stops when the agent says DONE)", count))
	return c, c.beginTurn(task, echo)
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
	if c.working {
		return c, tea.Println(indent(c.s.Dim.Render("wait for the current turn to finish")))
	}
	if len(args) == 0 {
		// no id: open the interactive picker across all connected providers
		return c, c.openPicker()
	}
	// explicit id: set it directly on the current provider
	c.sess.Model = args[0]
	return c, tea.Println(indent(c.s.Accent.Render("→ ") + c.s.Text.Render(args[0]) + c.s.Dim.Render(" · "+c.providerID)))
}

func padTo(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
