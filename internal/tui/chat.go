package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/theme"
)

// eventMsg wraps an agent event for the bubbletea loop.
type eventMsg struct{ ev agent.Event }
type tickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Chat is loyi's interactive coding interface: a scrolling conversation with
// the agent, inline (not altscreen) so history stays in the terminal.
type Chat struct {
	cfg     *config.Config
	sess    *agent.Session
	th      theme.Theme
	s       theme.Styles
	input   textinput.Model
	width   int
	spinner int

	events   chan agent.Event
	working  bool
	stream   strings.Builder // assistant text for the current segment
	toolLine string          // live tool activity line
	pending  *agent.PermissionEvent

	// loop state (/loop)
	loopActive bool
	loopLeft   int
	loopTotal  int

	cancel context.CancelFunc
	quit   bool
}

const loopContinue = "Continue with the task. When it is fully complete, reply with only the word: DONE"

func NewChat(cfg *config.Config, sess *agent.Session, th theme.Theme) *Chat {
	in := textinput.New()
	in.Placeholder = "what are we building?"
	in.SetVirtualCursor(true)
	c := &Chat{
		cfg:    cfg,
		sess:   sess,
		th:     th,
		s:      th.Styles(),
		input:  in,
		events: make(chan agent.Event, 64),
	}
	st := textinput.DefaultDarkStyles()
	st.Focused.Prompt = c.s.Accent
	st.Focused.Text = c.s.Text
	st.Focused.Placeholder = c.s.Dim
	st.Cursor.Color = lipgloss.Color(th.Accent)
	in.SetStyles(st)
	c.input.Prompt = "› "
	return c
}

func (c *Chat) Init() tea.Cmd {
	return tea.Batch(c.input.Focus(), tea.Println(c.banner()))
}

func (c *Chat) banner() string {
	who := c.cfg.Name
	if who == "" {
		who = "there"
	}
	return "\n" + c.s.Accent.Render("loyi") + c.s.Dim.Render(" · "+c.sess.Agent.Label+" · "+c.cfg.DefaultProvider) +
		"\n" + c.s.Dim.Render(fmt.Sprintf("hey %s. describe what you want to build, or /help for commands.", who))
}

func (c *Chat) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.input.SetWidth(msg.Width - 4)
		return c, nil

	case tickMsg:
		if c.working {
			c.spinner++
			return c, c.tick()
		}
		return c, nil

	case eventMsg:
		return c.handleEvent(msg.ev)

	case tea.KeyPressMsg:
		return c.handleKey(msg)
	}
	return c, nil
}

func (c *Chat) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Permission prompt takes over the keyboard.
	if c.pending != nil {
		switch key {
		case "y", "enter":
			c.pending.Reply <- true
			c.pending = nil
			return c, c.waitEvent()
		case "a":
			c.sess.AutoApprove = true
			c.pending.Reply <- true
			c.pending = nil
			return c, tea.Sequence(tea.Println(c.s.Dim.Render("  auto-approving tool calls for this session")), c.waitEvent())
		case "n", "esc":
			c.pending.Reply <- false
			c.pending = nil
			return c, c.waitEvent()
		case "ctrl+c":
			c.pending.Reply <- false
			c.pending = nil
			if c.cancel != nil {
				c.cancel()
			}
			return c, nil
		}
		return c, nil
	}

	switch key {
	case "ctrl+c":
		if c.working && c.cancel != nil {
			c.stopLoop()
			c.cancel()
			return c, tea.Println(c.s.Dim.Render("  interrupted"))
		}
		c.quit = true
		return c, tea.Quit
	case "ctrl+d":
		c.quit = true
		return c, tea.Quit
	case "enter":
		if c.working {
			return c, nil
		}
		text := strings.TrimSpace(c.input.Value())
		if text == "" {
			return c, nil
		}
		c.input.SetValue("")
		if strings.HasPrefix(text, "/") {
			return c.runCommand(text)
		}
		return c.startTurn(text)
	}

	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

func (c *Chat) startTurn(text string) (tea.Model, tea.Cmd) {
	echo := c.s.Accent.Render("›") + " " + c.s.Text.Render(text)
	return c, c.beginTurn(text, echo)
}

// beginTurn kicks off one agent turn, printing echo above the live view and
// starting the event pump. echo may be empty for a silent (loop) step.
func (c *Chat) beginTurn(text, echo string) tea.Cmd {
	c.working = true
	c.spinner = 0
	c.stream.Reset()
	c.toolLine = ""

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	sess := c.sess
	events := c.events
	go func() {
		sess.Run(ctx, text, func(e agent.Event) { events <- e })
	}()

	cmds := []tea.Cmd{c.waitEvent(), c.tick()}
	if echo != "" {
		cmds = append([]tea.Cmd{tea.Println(echo)}, cmds...)
	}
	return tea.Batch(cmds...)
}

// loopNext decides whether to run another loop iteration after a turn ended
// with the given final assistant text. Returns nil when the loop is inactive
// or should stop.
func (c *Chat) loopNext(final string) tea.Cmd {
	if !c.loopActive {
		return nil
	}
	if isDone(final) {
		c.loopActive = false
		return tea.Println(c.s.Accent.Render("  ✓ ") + c.s.Dim.Render("loop done — agent reported the task complete"))
	}
	c.loopLeft--
	if c.loopLeft <= 0 {
		c.loopActive = false
		return tea.Println(c.s.Dim.Render(fmt.Sprintf("  loop stopped after %d iterations", c.loopTotal)))
	}
	label := c.s.Accent.Render("  ↻ ") + c.s.Dim.Render(fmt.Sprintf("loop %d/%d", c.loopTotal-c.loopLeft+1, c.loopTotal))
	return c.beginTurn(loopContinue, label)
}

func isDone(s string) bool {
	s = strings.TrimSpace(s)
	return s == "DONE" || strings.HasPrefix(s, "DONE") || strings.HasSuffix(s, "DONE")
}

func (c *Chat) stopLoop() {
	c.loopActive = false
	c.loopLeft = 0
}

func (c *Chat) waitEvent() tea.Cmd {
	return func() tea.Msg { return eventMsg{<-c.events} }
}

func (c *Chat) tick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (c *Chat) handleEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case agent.TextEvent:
		c.stream.WriteString(e.Text)
		return c, c.waitEvent()

	case agent.ToolStartEvent:
		var cmds []tea.Cmd
		if s := strings.TrimSpace(c.stream.String()); s != "" {
			cmds = append(cmds, tea.Println(c.s.Text.Render(s)))
			c.stream.Reset()
		}
		c.toolLine = e.Summary
		cmds = append(cmds, c.waitEvent())
		return c, tea.Sequence(cmds...)

	case agent.ToolResultEvent:
		c.toolLine = ""
		line := c.s.Accent.Render("  ·") + " " + c.s.Dim.Render(toolLineText(e))
		return c, tea.Sequence(tea.Println(line), c.waitEvent())

	case agent.PermissionEvent:
		c.pending = &e
		c.toolLine = ""
		return c, nil // wait for keypress, not another event

	case agent.DoneEvent:
		c.working = false
		c.cancel = nil
		final := strings.TrimSpace(c.stream.String())
		c.stream.Reset()
		var cmds []tea.Cmd
		if final != "" {
			cmds = append(cmds, tea.Println(c.s.Text.Render(final)))
		}
		if cont := c.loopNext(final); cont != nil {
			cmds = append(cmds, cont)
		}
		if len(cmds) == 0 {
			return c, nil
		}
		return c, tea.Sequence(cmds...)

	case agent.ErrorEvent:
		c.working = false
		c.cancel = nil
		c.stopLoop()
		var cmds []tea.Cmd
		if s := strings.TrimSpace(c.stream.String()); s != "" {
			cmds = append(cmds, tea.Println(c.s.Text.Render(s)))
			c.stream.Reset()
		}
		cmds = append(cmds, tea.Println(c.s.Accent.Render("  ✗ ")+c.s.Dim.Render(e.Err.Error())))
		return c, tea.Sequence(cmds...)
	}
	return c, c.waitEvent()
}

func toolLineText(e agent.ToolResultEvent) string {
	label := e.Name
	if e.IsError {
		return label + " · " + firstLine(e.Output)
	}
	return label + " · " + firstLine(e.Output)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 76 {
		s = s[:76] + " …"
	}
	return s
}

func (c *Chat) View() tea.View {
	if c.quit {
		return tea.NewView("")
	}
	var b strings.Builder

	// live assistant text (before it's committed to scrollback)
	if s := strings.TrimSpace(c.stream.String()); s != "" {
		b.WriteString(c.s.Text.Render(s) + "\n")
	}

	switch {
	case c.pending != nil:
		b.WriteString("\n" + c.permissionPrompt())
	case c.toolLine != "":
		frame := spinnerFrames[c.spinner%len(spinnerFrames)]
		b.WriteString(c.s.Accent.Render("  "+frame) + " " + c.s.Dim.Render(c.toolLine))
	case c.working:
		frame := spinnerFrames[c.spinner%len(spinnerFrames)]
		b.WriteString(c.s.Accent.Render("  "+frame) + " " + c.s.Dim.Render("thinking"))
	default:
		b.WriteString(c.input.View())
	}

	v := tea.NewView(b.String())
	return v
}

func (c *Chat) permissionPrompt() string {
	q := c.s.Text.Render("allow ") + c.s.Accent.Render(c.pending.Summary) + c.s.Text.Render(" ?")
	opts := c.s.Dim.Render("  [y] yes   [n] no   [a] always")
	return q + "\n" + opts
}
