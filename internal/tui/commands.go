package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/theme"
)

// runCommand handles a /slash command typed into the chat. Command output is
// appended to the conversation like any other line.
func (c *Chat) runCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	cmd := strings.TrimPrefix(fields[0], "/")
	args := fields[1:]
	return c.runCommand2(cmd, args)
}

func (c *Chat) runCommand2(cmd string, args []string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "help", "?":
		return c, c.push(c.helpText())
	case "clear":
		c.sess.Reset()
		c.items = nil
		c.focus = -1
		if c.showGreeting() {
			c.appendText(c.greeting())
		}
		c.stick = true
		return c, nil
	case "usage":
		return c, c.push(c.usageText())
	case "agent":
		return c.agentCommand(args)
	case "agents", "team", "tree":
		return c.teamCommand()
	case "effort":
		return c.effortCommand(args)
	case "permission", "permissions", "perm":
		return c.permissionCommand(args)
	case "theme", "themes":
		return c.themeCommand(args)
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
		return c, c.push(indent(c.s.Dim.Render("unknown command: /" + cmd + "  ·  try /help")))
	}
}

func (c *Chat) helpText() string {
	lines := []struct{ cmd, desc string }{
		{"/help", "show this list"},
		{"/agent [id]", "switch persona: plan · build · ship · construct · pm"},
		{"/agents", "live monitor of the sub-agent team (also ⌃t)"},
		{"/effort [low|medium|high]", "reasoning effort (no arg shows current)"},
		{"/permission [mode]", "how edits are gated: ask · accept-edits · auto · bypass"},
		{"/theme [name]", "change the accent color (no name opens the picker)"},
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
	if c.working {
		return c, c.push(indent(c.s.Dim.Render("wait for the current turn to finish")))
	}
	if len(args) == 0 {
		// open the interactive picker on the current agent
		c.agentPickerActive = true
		c.agentPickerIdx = 0
		for i, a := range agent.Agents {
			if a.ID == c.sess.Agent.ID {
				c.agentPickerIdx = i
			}
		}
		return c, nil
	}
	id := args[0]
	for _, a := range agent.Agents {
		if a.ID == id {
			c.sess.SwitchAgent(a)
			return c, c.push(c.s.Accent.Render("  → ") + c.s.Text.Render(a.Label) + c.s.Dim.Render(" · "+a.Tagline))
		}
	}
	return c, c.push(c.s.Dim.Render("  no agent called " + id + "  ·  try /agent to list them"))
}

// teamCommand opens the live team monitor (the pyramid of sub-agents).
func (c *Chat) teamCommand() (tea.Model, tea.Cmd) {
	return c, c.openMonitor()
}

// themeCommand opens the accent picker, or switches directly with an argument.
func (c *Chat) themeCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		c.themePickerActive = true
		c.themePickerOrig = c.th.Name
		c.themePickerIdx = 0
		for i, t := range theme.All() {
			if t.Name == c.th.Name {
				c.themePickerIdx = i
			}
		}
		return c, nil
	}
	name := strings.ToLower(args[0])
	for _, t := range theme.All() {
		if t.Name == name {
			c.applyTheme(t)
			return c, c.push(indent(c.s.Accent.Render("→ ") + c.s.Text.Render(t.Name) + c.s.Dim.Render(" theme")))
		}
	}
	var names []string
	for _, t := range theme.All() {
		names = append(names, t.Name)
	}
	return c, c.push(indent(c.s.Dim.Render("themes: " + strings.Join(names, " · "))))
}

func shorten(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func elapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

const loopMax = 25

func (c *Chat) loopCommand(args []string) (tea.Model, tea.Cmd) {
	if c.working {
		return c, c.push(c.s.Dim.Render("  already working — wait for the current turn to finish"))
	}
	// /loop <count> <task...>
	if len(args) < 2 {
		return c, c.push(c.s.Dim.Render("  usage: /loop <count> <task>   e.g. /loop 5 add tests until they all pass"))
	}
	count, err := strconv.Atoi(args[0])
	if err != nil || count < 1 {
		return c, c.push(c.s.Dim.Render("  first argument must be a number of iterations, e.g. /loop 5 <task>"))
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
		return c, c.push(c.s.Dim.Render("  effort: ") + c.s.Text.Render(cur))
	}
	switch args[0] {
	case "low", "medium", "high":
		c.sess.Effort = provider.Effort(args[0])
		return c, c.push(c.s.Accent.Render("  → ") + c.s.Text.Render("effort "+args[0]))
	default:
		return c, c.push(c.s.Dim.Render("  effort must be low, medium, or high"))
	}
}

var permModes = []struct {
	perm agent.Perm
	desc string
}{
	{agent.PermAsk, "ask before every edit and command (default)"},
	{agent.PermAcceptEdits, "auto-accept file edits, still ask before commands"},
	{agent.PermAuto, "auto-run what's clearly safe, ask when unsure"},
	{agent.PermBypass, "never ask — full autonomy"},
}

func (c *Chat) permissionCommand(args []string) (tea.Model, tea.Cmd) {
	cur := c.sess.Perm
	if cur == "" {
		cur = agent.PermAsk
	}
	if len(args) == 0 {
		var b strings.Builder
		b.WriteString("\n" + c.s.Text.Render("permission") + c.s.Dim.Render("  (on "+cur.Label()+")") + "\n")
		for _, m := range permModes {
			marker := "  "
			label := c.s.Dim.Render(padTo(string(m.perm), 14))
			if m.perm == cur {
				marker = c.s.Accent.Render("› ")
				label = c.s.Text.Render(padTo(string(m.perm), 14))
			}
			b.WriteString(marker + label + c.s.Dim.Render(m.desc) + "\n")
		}
		b.WriteString(c.s.Dim.Render("  set with /permission <mode>"))
		return c, c.push(strings.TrimRight(b.String(), "\n"))
	}
	mode, ok := parsePerm(args[0])
	if !ok {
		return c, c.push(indent(c.s.Dim.Render("modes: ask · accept-edits · auto · bypass")))
	}
	c.sess.Perm = mode
	return c, c.push(indent(c.s.Accent.Render("→ ") + c.s.Text.Render(mode.Label()) + c.s.Dim.Render(" mode")))
}

func parsePerm(s string) (agent.Perm, bool) {
	switch strings.ToLower(s) {
	case "ask", "default", "prompt":
		return agent.PermAsk, true
	case "accept-edits", "accept", "edits", "acceptedits":
		return agent.PermAcceptEdits, true
	case "auto":
		return agent.PermAuto, true
	case "bypass", "yolo", "full", "none":
		return agent.PermBypass, true
	}
	return "", false
}

func (c *Chat) modelCommand(args []string) (tea.Model, tea.Cmd) {
	if c.working {
		return c, c.push(indent(c.s.Dim.Render("wait for the current turn to finish")))
	}
	if len(args) == 0 {
		// no id: open the interactive picker across all connected providers
		return c, c.openPicker()
	}
	// explicit id: set it directly on the current provider
	c.sess.Model = args[0]
	return c, c.push(indent(c.s.Accent.Render("→ ") + c.s.Text.Render(args[0]) + c.s.Dim.Render(" · "+c.providerID)))
}

func padTo(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
