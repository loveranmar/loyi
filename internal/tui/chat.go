package tui

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/mascot"
	"github.com/loveranmar/loyi/internal/provider/factory"
	"github.com/loveranmar/loyi/internal/theme"
)

// eventMsg wraps an agent event for the bubbletea loop.
type eventMsg struct{ ev agent.Event }

// mascotRestMsg returns the mascot to idle after a success/error flash.
type mascotRestMsg struct{}

// wordTickMsg rotates the status word. gen guards against stale ticks.
type wordTickMsg struct{ gen int }

// catalogMsg carries the fetched model list into the picker.
type catalogMsg struct {
	entries []factory.ModelEntry
	err     string
}

// reloadedMsg fires after `/connect` returns from setup, with a fresh config.
type reloadedMsg struct{ cfg *config.Config }

// pad is the left margin — nothing is ever glued to the terminal edge.
const pad = 2

// Chat is loyi's interactive coding interface: a scrolling conversation with
// the agent, inline (not altscreen) so history stays in the terminal.
type Chat struct {
	cfg   *config.Config
	sess  *agent.Session
	th    theme.Theme
	s     theme.Styles
	input textinput.Model
	pup   mascot.Model
	width int

	// status word rotation
	cycler  *mascot.Cycler
	word    string
	wordGen int

	// active provider (for the /model picker and switching)
	providerID string

	// model picker state
	pickerLoading bool
	pickerActive  bool
	pickerErr     string
	pickerEntries []factory.ModelEntry
	pickerIdx     int

	events     chan agent.Event
	working    bool
	stream     strings.Builder // assistant text for the current segment
	toolLine   string          // live tool activity: the running call's summary
	toolTarget string          // what the running call is acting on (path, pattern…)
	toolBlock  bool            // a ✓/✗ line was printed since the last text block
	pending    *agent.PermissionEvent

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
		cfg:        cfg,
		sess:       sess,
		th:         th,
		s:          th.Styles(),
		input:      in,
		pup:        mascot.New(mascot.Mini, th),
		cycler:     mascot.NewCycler(rand.New(rand.NewSource(time.Now().UnixNano()))),
		word:       "ready",
		providerID: cfg.DefaultProvider,
		events:     make(chan agent.Event, 64),
	}
	st := textinput.DefaultDarkStyles()
	st.Focused.Prompt = c.s.Accent
	st.Focused.Text = c.s.Text       // typed text is full-bright primary
	st.Focused.Placeholder = c.s.Dim // placeholder is dim
	st.Cursor.Color = lipgloss.Color(th.Accent)
	c.input.SetStyles(st) // on the struct's copy — styling `in` would be lost
	c.input.Prompt = "› "
	return c
}

func (c *Chat) Init() tea.Cmd {
	return tea.Batch(c.input.Focus(), tea.Println(c.banner()), c.pup.Init())
}

// banner is the header + greeting, printed once so it stays at the top of
// scrollback. Lowercase, minimal, no provider name.
func (c *Chat) banner() string {
	who := c.cfg.Name
	greet := "hey. describe what you want to build, or /help for commands."
	if who != "" {
		greet = fmt.Sprintf("hey %s. describe what you want to build, or /help for commands.", who)
	}
	head := c.s.Accent.Render("loyi") + c.s.Dim.Render(" · "+c.sess.Agent.Label)
	return indent(head) + "\n\n" + indent(c.s.Dim.Render(greet)) + "\n"
}

// indent prefixes a (possibly multi-line) block with the standard left pad.
func indent(s string) string {
	p := strings.Repeat(" ", pad)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = p + ln
	}
	return strings.Join(lines, "\n")
}

// userLine formats a user turn: dim › caret, primary text.
func (c *Chat) userLine(text string) string {
	return indent(c.s.Dim.Render("›") + " " + c.s.Text.Render(text))
}

// loyiLine formats a loyi turn: accent ▸ caret on the first line, continuation
// lines aligned under the text.
func (c *Chat) loyiLine(text string) string {
	p := strings.Repeat(" ", pad)
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		if i == 0 {
			lines[i] = p + c.s.Accent.Render("▸") + " " + c.s.Text.Render(ln)
		} else {
			lines[i] = p + "  " + c.s.Text.Render(ln)
		}
	}
	return strings.Join(lines, "\n")
}

// toolIndent is the left margin for tool action lines, tucked under loyi's
// message.
func toolIndent() string { return strings.Repeat(" ", pad+2) }

// setActivity moves the mascot face and status word to a new activity and
// returns the commands to animate them.
func (c *Chat) setActivity(a mascot.Activity) tea.Cmd {
	c.word = c.cycler.Set(a)
	cmds := []tea.Cmd{c.pup.SetState(a.Face())}
	if a.Working() {
		c.wordGen++
		cmds = append(cmds, c.wordTick())
	}
	return tea.Batch(cmds...)
}

func (c *Chat) wordTick() tea.Cmd {
	gen := c.wordGen
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return wordTickMsg{gen: gen} })
}

// openPicker fetches the model catalog across all providers and opens the
// picker when it arrives.
func (c *Chat) openPicker() tea.Cmd {
	c.pickerLoading = true
	cfg := c.cfg
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return catalogMsg{entries: factory.Catalog(ctx, cfg)}
	}
}

func (c *Chat) currentModelIndex() int {
	for i, e := range c.pickerEntries {
		if e.Provider == c.providerID && e.Model == c.currentModel() {
			return i
		}
	}
	return 0
}

func (c *Chat) currentModel() string {
	if c.sess.Model != "" {
		return c.sess.Model
	}
	if pc := c.cfg.Providers[c.providerID]; pc != nil && pc.Model != "" {
		return pc.Model
	}
	return ""
}

// pickModel switches the session to the chosen model, rebuilding the provider
// if it belongs to a different backend than the current one.
func (c *Chat) pickModel(e factory.ModelEntry) (tea.Model, tea.Cmd) {
	c.pickerActive = false
	if e.Provider != c.providerID {
		p, err := factory.Build(context.Background(), c.cfg, e.Provider)
		if err != nil {
			return c, tea.Println(indent(c.s.Danger.Render("✗ ") + c.s.Dim.Render(err.Error())))
		}
		c.sess.Provider = p
		c.providerID = e.Provider
	}
	c.sess.Model = e.Model
	line := c.s.Accent.Render("→ ") + c.s.Text.Render(e.Model) + c.s.Dim.Render(" · "+e.Provider)
	return c, tea.Println(indent(line))
}

// connect pauses the chat, runs `loyi setup`, then reloads the config so newly
// connected providers show up in the picker.
func (c *Chat) connect() tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return tea.Println(indent(c.s.Dim.Render("run `loyi setup` to connect a provider")))
	}
	cmd := exec.Command(exe, "setup")
	return tea.ExecProcess(cmd, func(error) tea.Msg {
		cfg, err := config.Load()
		if err != nil {
			return reloadedMsg{cfg: c.cfg}
		}
		return reloadedMsg{cfg: cfg}
	})
}

func (c *Chat) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.input.SetWidth(c.boxContentWidth() - 5)
		return c, nil

	case mascot.TickMsg:
		var cmd tea.Cmd
		c.pup, cmd = c.pup.Update(msg)
		return c, cmd

	case wordTickMsg:
		if msg.gen == c.wordGen && c.working {
			c.word = c.cycler.Next()
			return c, c.wordTick()
		}
		return c, nil

	case mascotRestMsg:
		if !c.working && (c.pup.State() == mascot.Success || c.pup.State() == mascot.Error) {
			return c, c.setActivity(mascot.ActReady)
		}
		return c, nil

	case catalogMsg:
		c.pickerLoading = false
		if msg.err != "" {
			return c, tea.Println(indent(c.s.Dim.Render("couldn't list models: " + msg.err)))
		}
		if len(msg.entries) == 0 {
			return c, tea.Println(indent(c.s.Dim.Render("no models found — run /connect to add a provider")))
		}
		c.pickerEntries = msg.entries
		c.pickerActive = true
		c.pickerIdx = c.currentModelIndex()
		return c, nil

	case reloadedMsg:
		c.cfg = msg.cfg
		return c, tea.Println(indent(c.s.Dim.Render("providers refreshed — /model to pick from them")))

	case eventMsg:
		return c.handleEvent(msg.ev)

	case tea.KeyPressMsg:
		return c.handleKey(msg)
	}
	return c, nil
}

func (c *Chat) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Model picker takes over the keyboard.
	if c.pickerActive {
		switch key {
		case "up", "k":
			c.pickerIdx = (c.pickerIdx + len(c.pickerEntries) - 1) % len(c.pickerEntries)
		case "down", "j":
			c.pickerIdx = (c.pickerIdx + 1) % len(c.pickerEntries)
		case "enter":
			return c.pickModel(c.pickerEntries[c.pickerIdx])
		case "esc", "ctrl+c", "q":
			c.pickerActive = false
		}
		return c, nil
	}

	// Permission prompt takes over the keyboard.
	if c.pending != nil {
		resume := func(extra ...tea.Cmd) tea.Cmd {
			cmds := append(extra, c.setActivity(mascot.ActThinking), c.waitEvent())
			return tea.Sequence(cmds...)
		}
		switch key {
		case "y", "enter":
			c.pending.Reply <- true
			c.pending = nil
			return c, resume()
		case "a":
			c.sess.AutoApprove = true
			c.pending.Reply <- true
			c.pending = nil
			return c, resume(tea.Println(indent(c.s.Dim.Render("auto-approving tool calls for this session"))))
		case "n", "esc":
			c.pending.Reply <- false
			c.pending = nil
			return c, resume()
		case "ctrl+c":
			c.pending.Reply <- false
			c.pending = nil
			c.stopLoop()
			c.working = false
			if c.cancel != nil {
				c.cancel()
			}
			return c, c.setActivity(mascot.ActReady)
		}
		return c, nil
	}

	switch key {
	case "ctrl+c":
		if c.working && c.cancel != nil {
			c.stopLoop()
			c.working = false
			c.cancel()
			return c, tea.Sequence(tea.Println(indent(c.s.Dim.Render("interrupted"))), c.setActivity(mascot.ActReady))
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
	return c, c.beginTurn(text, "\n"+c.userLine(text))
}

// beginTurn kicks off one agent turn, printing echo above the live view and
// starting the event pump. echo may be empty for a silent (loop) step.
func (c *Chat) beginTurn(text, echo string) tea.Cmd {
	c.working = true
	c.stream.Reset()
	c.toolLine = ""
	c.toolTarget = ""
	c.toolBlock = false

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	sess := c.sess
	events := c.events
	go func() {
		sess.Run(ctx, text, func(e agent.Event) { events <- e })
	}()

	cmds := []tea.Cmd{c.waitEvent(), c.setActivity(mascot.ActThinking)}
	if echo != "" {
		cmds = append([]tea.Cmd{tea.Println(echo)}, cmds...)
	}
	return tea.Batch(cmds...)
}

// activityForTool maps a tool name to the status activity: wolf verbs for
// reading/exploring, plain verbs for concrete steps.
func activityForTool(name string) mascot.Activity {
	switch name {
	case "write", "edit":
		return mascot.ActWriting
	case "run":
		return mascot.ActRunning
	default: // read, tree, ls, glob, grep
		return mascot.ActReading
	}
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

// restMascot flashes success/error, then returns the pup to idle after a beat.
func restMascot() tea.Cmd {
	return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return mascotRestMsg{} })
}

func (c *Chat) handleEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case agent.TextEvent:
		c.stream.WriteString(e.Text)
		return c, c.waitEvent()

	case agent.ToolStartEvent:
		var cmds []tea.Cmd
		if s := strings.TrimSpace(c.stream.String()); s != "" {
			cmds = append(cmds, tea.Println("\n"+c.loyiLine(s)))
			c.stream.Reset()
			c.toolBlock = false
		}
		c.toolLine = e.Summary
		c.toolTarget = targetOf(e.Summary)
		cmds = append(cmds, c.setActivity(activityForTool(e.Name)), c.waitEvent())
		return c, tea.Batch(cmds...)

	case agent.ToolResultEvent:
		c.toolLine = ""
		line := c.toolResultLine(e)
		if !c.toolBlock {
			line = "\n" + line // first action under a message gets its own block
			c.toolBlock = true
		}
		// after a tool, the model is thinking again
		return c, tea.Batch(tea.Println(line), c.setActivity(mascot.ActThinking), c.waitEvent())

	case agent.PermissionEvent:
		c.pending = &e
		c.toolLine = ""
		c.word = "waiting on you"
		return c, c.pup.SetState(mascot.Listening) // your turn

	case agent.DoneEvent:
		c.working = false
		c.cancel = nil
		final := strings.TrimSpace(c.stream.String())
		c.stream.Reset()
		var cmds []tea.Cmd
		if final != "" {
			cmds = append(cmds, tea.Println("\n"+c.loyiLine(final)))
			c.toolBlock = false
		}
		if cont := c.loopNext(final); cont != nil {
			cmds = append(cmds, cont)
		} else {
			// no more loop work — flash success, then settle back to ready
			cmds = append(cmds, c.setActivity(mascot.ActSuccess), restMascot())
		}
		return c, tea.Sequence(cmds...)

	case agent.ErrorEvent:
		c.working = false
		c.cancel = nil
		c.stopLoop()
		var cmds []tea.Cmd
		if s := strings.TrimSpace(c.stream.String()); s != "" {
			cmds = append(cmds, tea.Println("\n"+c.loyiLine(s)))
			c.stream.Reset()
		}
		cmds = append(cmds, tea.Println("\n"+indent(c.s.Danger.Render("✗ ")+c.s.Dim.Render(e.Err.Error()))))
		c.toolBlock = false
		cmds = append(cmds, c.setActivity(mascot.ActError), restMascot())
		return c, tea.Sequence(cmds...)
	}
	return c, c.waitEvent()
}

// toolResultLine resolves a finished tool call into its transcript line:
// "✓ index.html · 1 line" — mark in accent (✗ in terracotta on error), target
// primary, detail dim.
func (c *Chat) toolResultLine(e agent.ToolResultEvent) string {
	target := c.toolTarget
	if target == "" {
		target = e.Name
	}
	if e.IsError {
		return toolIndent() + c.s.Danger.Render("✗") + " " +
			c.s.Text.Render(target) + c.s.Dim.Render(" · "+firstLine(e.Output))
	}
	return toolIndent() + c.s.Accent.Render("✓") + " " +
		c.s.Text.Render(target) + c.s.Dim.Render(" · "+toolDetail(e.Name, e.Output))
}

// targetOf pulls what a tool acts on from its summary ("write index.html" →
// "index.html").
func targetOf(summary string) string {
	if _, arg, ok := strings.Cut(summary, " "); ok {
		return arg
	}
	return summary
}

// toolDetail is the short dim note after the target: what came of the call, in
// a couple of words.
func toolDetail(name, out string) string {
	out = strings.TrimSpace(out)
	switch name {
	case "write":
		// the tool reports "wrote index.html · 1 line" — keep just the tail
		if _, d, ok := strings.Cut(firstLine(out), " · "); ok {
			return d
		}
	case "edit":
		return "edited"
	case "read":
		return countNoun(lineCount(out), "line", "lines")
	case "grep", "glob":
		if strings.HasPrefix(out, "no ") {
			return firstLine(out)
		}
		return countNoun(lineCount(out), "match", "matches")
	case "ls", "tree":
		return countNoun(lineCount(out), "entry", "entries")
	case "run":
		if out == "(no output)" {
			return "done"
		}
		return countNoun(lineCount(out), "line", "lines")
	}
	return firstLine(out)
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// countNoun formats a count with the right noun form: "1 line", "3 lines".
func countNoun(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
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

// boxContentWidth is the inner width of the input box (inside the border).
func (c *Chat) boxContentWidth() int {
	w := c.width - 2*pad - 2 // left pad, right margin, two border cells
	if w < 24 {
		w = 24
	}
	return w
}

func (c *Chat) View() tea.View {
	if c.quit {
		return tea.NewView("")
	}
	if c.pickerActive {
		return tea.NewView(c.pickerView())
	}

	var b strings.Builder

	// live assistant text streams above the box until it's committed
	if s := strings.TrimSpace(c.stream.String()); s != "" {
		b.WriteString("\n" + c.loyiLine(s) + "\n")
	}
	// running tool action, tucked under the message; resolves to a ✓ line
	if c.toolLine != "" {
		b.WriteString("\n" + toolIndent() + c.s.Dim.Render(c.word+"  ") + c.s.Text.Render(c.toolLine) + "\n")
	}

	b.WriteString("\n\n" + c.inputBox() + "\n")
	b.WriteString(c.statusLine())
	if c.pickerLoading {
		b.WriteString("\n" + indent(c.s.Dim.Render("listing models…")))
	}

	return tea.NewView(b.String())
}

// pickerView renders the model chooser: models grouped by provider, the
// current one marked, cursor on the highlighted row.
func (c *Chat) pickerView() string {
	var b strings.Builder
	b.WriteString(indent(c.s.Text.Render("pick a model")) + "\n\n")

	lastProvider := ""
	for i, e := range c.pickerEntries {
		if e.Provider != lastProvider {
			if lastProvider != "" {
				b.WriteString("\n")
			}
			b.WriteString(indent(c.s.Dim.Render(e.Provider)) + "\n")
			lastProvider = e.Provider
		}
		cursor := "  "
		label := c.s.Dim.Render(e.Model)
		if i == c.pickerIdx {
			cursor = c.s.Accent.Render("› ")
			label = c.s.Text.Render(e.Model)
		}
		mark := "  "
		if e.Provider == c.providerID && e.Model == c.currentModel() {
			mark = c.s.Accent.Render("• ")
		}
		b.WriteString(strings.Repeat(" ", pad) + cursor + mark + label + "\n")
	}
	b.WriteString("\n" + indent(c.s.Dim.Render("↑↓ move   ⏎ select   esc cancel   ·   /connect to add a provider")))
	return b.String()
}

// inputBox renders the caret + input inside a rounded border, one line tall.
// The border is dim at rest and the theme accent while loyi is working.
func (c *Chat) inputBox() string {
	cw := c.boxContentWidth()
	// the leading spaces (2) + prompt (2) + input field must fit inside cw,
	// with a cell of slack for the cursor — anything wider wraps and the box
	// grows a phantom empty row
	c.input.SetWidth(cw - 5)

	border := lipgloss.Color(theme.Neutrals.Border)
	if c.working && c.pending == nil {
		border = lipgloss.Color(c.th.Accent)
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Width(cw).
		MarginLeft(pad).
		Render("  " + c.input.View())
}

// statusLine is the line under the box: mini mascot + state word on the left,
// keybind hints (or the permission choices) right-aligned to the box width.
func (c *Chat) statusLine() string {
	cw := c.boxContentWidth()
	lead := strings.Repeat(" ", pad+1) // align under the box's inner content

	var left, right string
	if c.pending != nil {
		left = c.pup.View() + "  " + c.s.Text.Render("allow ") + c.s.Accent.Render(c.pending.Summary) + c.s.Text.Render("?")
		right = c.s.Dim.Render("[y] yes   [n] no   [a] always")
	} else {
		wordStyle := c.s.Dim
		switch c.pup.State() {
		case mascot.Success:
			wordStyle = c.s.Accent
		case mascot.Error:
			wordStyle = c.s.Danger
		}
		left = c.pup.View() + "  " + wordStyle.Render(c.word)
		if c.working {
			right = c.s.Dim.Render("⌃c stop")
		} else {
			right = c.s.Dim.Render("⏎ send   ⌃c quit")
		}
	}

	gap := cw - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return lead + left + strings.Repeat(" ", gap) + right
}
