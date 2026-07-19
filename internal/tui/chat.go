package tui

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
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

// monitorTickMsg re-renders the live team monitor while it's open.
type monitorTickMsg struct{}

// catalogMsg carries the fetched model list into the picker.
type catalogMsg struct {
	entries []factory.ModelEntry
	err     string
}

// reloadedMsg fires after `/connect` returns from setup, with a fresh config.
type reloadedMsg struct{ cfg *config.Config }

// pad is the left margin — nothing is ever glued to the terminal edge.
const pad = 2

// Chat is loyi's interactive coding interface: a full-screen alt-screen app
// with a branding header, a scrolling conversation viewport, and a pinned
// input + status footer.
type Chat struct {
	cfg    *config.Config
	set    *config.Settings // loyi.json; nil falls back to defaults
	sess   *agent.Session
	orch   *agent.Orchestrator
	th     theme.Theme
	s      theme.Styles
	input  textinput.Model
	pup    mascot.Model
	width  int
	height int

	// full-screen alt-screen layout: a branding header, a scrolling viewport
	// holding the whole conversation, and a pinned input + status footer.
	vp          viewport.Model
	stick       bool   // keep the viewport pinned to the bottom as content grows
	lastContent string // last transcript set on the viewport, to avoid re-wrapping

	// the whole conversation: loyi's messages and expandable tool blocks. Kept
	// for the life of the session so blocks stay expandable and scroll back.
	items []turnItem
	focus int // index into items of the focused block; -1 = input

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

	// agent picker state
	agentPickerActive bool
	agentPickerIdx    int

	// theme picker state
	themePickerActive bool
	themePickerIdx    int
	themePickerOrig   string // theme to restore if the picker is cancelled

	// / command autocomplete state
	slashIdx int

	// long pastes collapsed to [Pasted text #n] placeholders; expanded on send
	pastes []string

	// live team monitor
	monitorActive bool

	events     chan agent.Event
	working    bool
	stream     strings.Builder // assistant text for the current segment
	toolLine   string          // live tool activity: the running call's summary
	toolTarget string          // what the running call is acting on (path, pattern…)
	pending    *agent.PermissionEvent

	// loop state (/loop)
	loopActive bool
	loopLeft   int
	loopTotal  int

	cancel context.CancelFunc
	quit   bool
}

const loopContinue = "Continue with the task. When it is fully complete, reply with only the word: DONE"

var pasteRe = regexp.MustCompile(`\[Pasted text #(\d+)[^\]]*\]`)

// pasteLimit is how many characters a paste can be before it collapses to a
// [Pasted text #n] placeholder. Configurable via ui.paste_threshold in
// loyi.json; defaults to 100.
func (c *Chat) pasteLimit() int {
	if c.set != nil {
		return c.set.PasteThreshold()
	}
	return 100
}

// stashPaste stores a long paste and returns the placeholder to show in the
// input, noting its line and character count.
func (c *Chat) stashPaste(content string) string {
	c.pastes = append(c.pastes, content)
	lines := strings.Count(content, "\n") + 1
	return fmt.Sprintf("[Pasted text #%d · %d lines · %d chars]", len(c.pastes), lines, len(content))
}

// expandPastes swaps [Pasted text #n] placeholders back to their full content
// before the message is sent to the model; unknown refs are left as-is.
func (c *Chat) expandPastes(s string) string {
	return pasteRe.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(pasteRe.FindStringSubmatch(m)[1])
		if n >= 1 && n <= len(c.pastes) {
			return c.pastes[n-1]
		}
		return m
	})
}

func NewChat(cfg *config.Config, set *config.Settings, sess *agent.Session, orch *agent.Orchestrator, th theme.Theme) *Chat {
	in := textinput.New()
	in.Placeholder = "what are we building?"
	in.SetVirtualCursor(true)
	c := &Chat{
		cfg:        cfg,
		set:        set,
		sess:       sess,
		orch:       orch,
		th:         th,
		s:          th.Styles(),
		input:      in,
		pup:        mascot.New(mascot.Mini, th),
		cycler:     mascot.NewCycler(rand.New(rand.NewSource(time.Now().UnixNano()))),
		word:       "ready",
		providerID: cfg.DefaultProvider,
		events:     make(chan agent.Event, 64),
		focus:      -1,
		vp:         viewport.New(),
		stick:      true,
	}
	c.vp.SoftWrap = true // wrap long replies to the window instead of clipping them
	c.restyleInput()
	return c
}

// restyleInput colors the input for the current theme. Styles must be set on
// c.input (the struct's copy) — styling the local value would be lost.
func (c *Chat) restyleInput() {
	st := textinput.DefaultDarkStyles()
	st.Focused.Prompt = c.s.Accent
	st.Focused.Text = c.s.Text       // typed text is full-bright primary
	st.Focused.Placeholder = c.s.Dim // placeholder is dim
	st.Cursor.Color = lipgloss.Color(c.th.Accent)
	c.input.SetStyles(st)
	c.input.Prompt = "› "
}

// previewTheme recolors the whole UI for a theme without persisting it.
func (c *Chat) previewTheme(t theme.Theme) {
	c.th = t
	c.s = t.Styles()
	c.pup.SetTheme(t)
	c.restyleInput()
	c.lastContent = "" // force the viewport to re-render in the new colors
}

// applyTheme switches to a theme and saves it so it sticks across launches.
func (c *Chat) applyTheme(t theme.Theme) {
	c.previewTheme(t)
	c.cfg.Theme = t.Name
	_ = c.cfg.Save()
}

func (c *Chat) Init() tea.Cmd {
	if c.showGreeting() {
		c.appendText(c.greeting())
	}
	return tea.Batch(c.input.Focus(), c.pup.Init())
}

// header is the persistent branding bar at the top of the screen: the loyi
// wordmark, the active agent, and a full-width rule.
func (c *Chat) header() string {
	w := c.width
	if w < 1 {
		w = 1
	}
	brand := c.s.Accent.Bold(true).Render("loyi") + c.s.Dim.Render("  ·  "+c.sess.Agent.Label)
	rule := c.s.Border.Render(strings.Repeat("─", w))
	return indent(brand) + "\n" + rule
}

// greeting is the one-time welcome line, added as the first transcript entry.
func (c *Chat) greeting() string {
	who := c.cfg.Name
	greet := "hey. describe what you want to build, or /help for commands."
	if who != "" {
		greet = fmt.Sprintf("hey %s. describe what you want to build, or /help for commands.", who)
	}
	return indent(c.s.Dim.Render(greet))
}

// footer is the pinned bottom region: the / command menu (when open), the
// input box, and the status line.
func (c *Chat) footer() string {
	var b strings.Builder
	if matches := c.slashMatches(); len(matches) > 0 {
		b.WriteString(c.slashMenu(matches) + "\n")
	}
	b.WriteString(c.inputBox() + "\n" + c.statusLine())
	return b.String()
}

type slashItem struct{ name, desc string }

// slashCommands is the list offered in the / autocomplete menu.
var slashCommands = []slashItem{
	{"help", "show all commands"},
	{"agent", "switch persona: plan · build · ship · construct · pm"},
	{"agents", "live monitor of the sub-agent team"},
	{"model", "pick a model across all providers"},
	{"theme", "change the accent color"},
	{"effort", "reasoning effort: low · medium · high"},
	{"permission", "how edits are gated"},
	{"connect", "connect another provider"},
	{"usage", "tokens and tool calls this session"},
	{"loop", "run a task repeatedly until DONE"},
	{"clear", "clear the conversation"},
	{"quit", "leave loyi"},
}

// slashMatches returns the commands the / menu should show for what's typed,
// or nil when it shouldn't open (no leading /, or args are already being typed).
func (c *Chat) slashMatches() []slashItem {
	v := c.input.Value()
	if !strings.HasPrefix(v, "/") || strings.Contains(v, " ") {
		return nil
	}
	prefix := strings.ToLower(strings.TrimPrefix(v, "/"))
	var out []slashItem
	for _, sc := range slashCommands {
		if strings.HasPrefix(sc.name, prefix) {
			out = append(out, sc)
		}
	}
	return out
}

func (c *Chat) clampSlash(matches []slashItem) int {
	if c.slashIdx >= len(matches) {
		return len(matches) - 1
	}
	if c.slashIdx < 0 {
		return 0
	}
	return c.slashIdx
}

// slashMenu renders the / autocomplete list shown above the input box.
func (c *Chat) slashMenu(matches []slashItem) string {
	sel := c.clampSlash(matches)
	var b strings.Builder
	for i, m := range matches {
		cursor := "  "
		name := c.s.Dim.Render(padTo("/"+m.name, 14))
		if i == sel {
			cursor = c.s.Accent.Render("› ")
			name = c.s.Text.Render(padTo("/"+m.name, 14))
		}
		b.WriteString(strings.Repeat(" ", pad) + cursor + name + c.s.Dim.Render(m.desc) + "\n")
	}
	b.WriteString(strings.Repeat(" ", pad) + c.s.Dim.Render("↑↓ choose   ⇥ complete   ⏎ run   esc dismiss"))
	return b.String()
}

func (c *Chat) showGreeting() bool {
	if c.set == nil {
		return true
	}
	switch c.set.BannerMode() {
	case "never":
		return false
	case "always":
		return true
	default: // first-run
		return c.set.CreatedNow()
	}
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

// userLine formats a user turn as a filled bubble hugging the right edge, like
// a chat app: your prompts sit on the right, loyi's replies on the left.
func (c *Chat) userLine(text string) string {
	bubble := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Neutrals.Text)).
		Background(lipgloss.Color(theme.Neutrals.Surface)).
		Padding(0, 1).
		Render(text)
	w := c.width
	if w < 1 {
		w = lipgloss.Width(bubble) + pad*2
	}
	left := w - pad - lipgloss.Width(bubble)
	if left < pad {
		left = pad
	}
	return strings.Repeat(" ", left) + bubble
}

// loyiLine formats a loyi turn: accent ▸ caret on the first line, with the text
// word-wrapped to the window and continuation lines aligned under it.
func (c *Chat) loyiLine(text string) string {
	w := c.width - pad - 2 // room after the "▸ " / "  " prefix
	if w < 20 {
		w = 20
	}
	p := strings.Repeat(" ", pad)
	var out []string
	first := true
	for _, para := range strings.Split(renderMarkdown(text, c.s), "\n") {
		for _, ln := range strings.Split(lipgloss.Wrap(para, w, ""), "\n") {
			if first {
				out = append(out, p+c.s.Accent.Render("▸")+" "+ln)
				first = false
			} else {
				out = append(out, p+"  "+ln)
			}
		}
	}
	return strings.Join(out, "\n")
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

// monitorTick drives the live team view; it re-renders a few times a second so
// elapsed times and activity update while sub-agents work.
func (c *Chat) monitorTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return monitorTickMsg{} })
}

// openMonitor shows the live team monitor.
func (c *Chat) openMonitor() tea.Cmd {
	c.monitorActive = true
	return c.monitorTick()
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
			return c, c.push(indent(c.s.Danger.Render("✗ ") + c.s.Dim.Render(err.Error())))
		}
		c.sess.Provider = p
		c.providerID = e.Provider
	}
	c.sess.Model = e.Model
	line := c.s.Accent.Render("→ ") + c.s.Text.Render(e.Model) + c.s.Dim.Render(" · "+e.Provider)
	return c, c.push(indent(line))
}

// connect pauses the chat, runs `loyi setup`, then reloads the config so newly
// connected providers show up in the picker.
func (c *Chat) connect() tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		return c.push(indent(c.s.Dim.Render("run `loyi setup` to connect a provider")))
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
		c.height = msg.Height
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

	case monitorTickMsg:
		if c.monitorActive {
			return c, c.monitorTick() // re-arm; View re-renders with fresh times
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
			return c, c.push(indent(c.s.Dim.Render("couldn't list models: " + msg.err)))
		}
		if len(msg.entries) == 0 {
			return c, c.push(indent(c.s.Dim.Render("no models found — run /connect to add a provider")))
		}
		c.pickerEntries = msg.entries
		c.pickerActive = true
		c.pickerIdx = c.currentModelIndex()
		return c, nil

	case reloadedMsg:
		c.cfg = msg.cfg
		return c, c.push(indent(c.s.Dim.Render("providers refreshed — /model to pick from them")))

	case eventMsg:
		return c.handleEvent(msg.ev)

	case tea.PasteMsg:
		if c.pickerActive || c.pending != nil {
			return c, nil
		}
		// collapse a long paste to a placeholder; expanded again on send
		if len(msg.Content) > c.pasteLimit() {
			msg = tea.PasteMsg{Content: c.stashPaste(msg.Content)}
		}
		var cmd tea.Cmd
		c.input, cmd = c.input.Update(msg)
		return c, cmd

	case tea.MouseWheelMsg:
		// let the viewport scroll a few lines per notch, then track whether
		// we're back at the bottom so new output resumes auto-following
		var cmd tea.Cmd
		c.vp, cmd = c.vp.Update(msg)
		c.stick = c.vp.AtBottom()
		return c, cmd

	case tea.KeyPressMsg:
		return c.handleKey(msg)
	}
	return c, nil
}

func (c *Chat) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// The team monitor toggles with ctrl+t from anywhere — including mid-turn,
	// so you can watch the team work.
	if key == "ctrl+t" {
		if c.monitorActive {
			c.monitorActive = false
			return c, nil
		}
		return c, c.openMonitor()
	}
	// While the monitor is open it owns the keyboard (except ctrl+c to stop).
	if c.monitorActive {
		switch key {
		case "esc", "q", "enter":
			c.monitorActive = false
			return c, nil
		case "ctrl+c":
			c.monitorActive = false
			if c.working && c.cancel != nil {
				c.stopLoop()
				c.working = false
				c.cancel()
				return c, tea.Sequence(c.push(indent(c.s.Dim.Render("interrupted"))), c.setActivity(mascot.ActReady))
			}
		}
		return c, nil
	}

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

	// Agent picker takes over the keyboard.
	if c.agentPickerActive {
		switch key {
		case "up", "k":
			c.agentPickerIdx = (c.agentPickerIdx + len(agent.Agents) - 1) % len(agent.Agents)
		case "down", "j":
			c.agentPickerIdx = (c.agentPickerIdx + 1) % len(agent.Agents)
		case "enter":
			c.agentPickerActive = false
			a := agent.Agents[c.agentPickerIdx]
			c.sess.SwitchAgent(a)
			return c, c.push(indent(c.s.Accent.Render("→ ") + c.s.Text.Render(a.Label) + c.s.Dim.Render(" · "+a.Tagline)))
		case "esc", "ctrl+c", "q":
			c.agentPickerActive = false
		}
		return c, nil
	}

	// Theme picker takes over the keyboard, previewing as you move.
	if c.themePickerActive {
		all := theme.All()
		switch key {
		case "up", "k":
			c.themePickerIdx = (c.themePickerIdx + len(all) - 1) % len(all)
			c.previewTheme(all[c.themePickerIdx])
		case "down", "j":
			c.themePickerIdx = (c.themePickerIdx + 1) % len(all)
			c.previewTheme(all[c.themePickerIdx])
		case "enter":
			c.themePickerActive = false
			t := all[c.themePickerIdx]
			c.applyTheme(t)
			return c, c.push(indent(c.s.Accent.Render("→ ") + c.s.Text.Render(t.Name) + c.s.Dim.Render(" theme")))
		case "esc", "ctrl+c", "q":
			c.themePickerActive = false
			c.previewTheme(theme.Get(c.themePickerOrig)) // revert the preview
		}
		return c, nil
	}

	// Permission card takes over the keyboard.
	if c.pending != nil {
		resume := func(extra ...tea.Cmd) tea.Cmd {
			cmds := append(extra, c.setActivity(mascot.ActThinking), c.waitEvent())
			return tea.Sequence(cmds...)
		}
		switch key {
		case "y", "enter":
			c.pending.Reply <- agent.ReplyAllow
			c.pending = nil
			return c, resume()
		case "a":
			rule := config.RuleFor(c.pending.Tool, c.pending.Target)
			note := fmt.Sprintf("always allowing %s — saved to %s", rule, ruleFileName(c.set))
			c.appendText(indent(c.s.Dim.Render(note)))
			c.pending.Reply <- agent.ReplyAlways
			c.pending = nil
			return c, resume()
		case "n", "esc":
			c.pending.Reply <- agent.ReplyDeny
			c.pending = nil
			return c, resume()
		case "ctrl+c":
			c.pending.Reply <- agent.ReplyDeny
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
			c.appendText(indent(c.s.Dim.Render("interrupted")))
			return c, c.setActivity(mascot.ActReady)
		}
		c.quit = true
		return c, tea.Quit
	case "ctrl+d":
		c.quit = true
		return c, tea.Quit
	}

	// A focused tool block owns the keyboard until focus returns to the input.
	if c.focus >= 0 {
		return c.handleBlockKey(key)
	}

	// The / command menu owns up/down/tab/enter/esc while it's open.
	if matches := c.slashMatches(); len(matches) > 0 {
		switch key {
		case "up":
			c.slashIdx = (c.clampSlash(matches) + len(matches) - 1) % len(matches)
			return c, nil
		case "down":
			c.slashIdx = (c.clampSlash(matches) + 1) % len(matches)
			return c, nil
		case "tab":
			c.input.SetValue("/" + matches[c.clampSlash(matches)].name + " ")
			c.slashIdx = 0
			return c, nil
		case "enter":
			name := matches[c.clampSlash(matches)].name
			c.input.SetValue("")
			c.slashIdx = 0
			return c.runCommand("/" + name)
		case "esc":
			c.input.SetValue("")
			c.slashIdx = 0
			return c, nil
		}
		c.slashIdx = 0 // any other key edits the input; reset the highlight
	}

	switch key {
	case "pgup":
		c.vp.ScrollUp(c.vp.Height() / 2)
		c.stick = c.vp.AtBottom()
		return c, nil
	case "pgdown", "pgdn":
		c.vp.ScrollDown(c.vp.Height() / 2)
		c.stick = c.vp.AtBottom()
		return c, nil
	case "up":
		c.vp.ScrollUp(1)
		c.stick = c.vp.AtBottom()
		return c, nil
	case "down":
		c.vp.ScrollDown(1)
		c.stick = c.vp.AtBottom()
		return c, nil
	case "tab":
		return c, c.focusLastBlock()
	case "ctrl+v":
		return c, pasteFromClipboard
	case "enter":
		if c.working {
			return c, nil
		}
		shown := strings.TrimSpace(c.input.Value()) // with paste placeholders
		if shown == "" {
			return c, nil
		}
		text := strings.TrimSpace(c.expandPastes(shown)) // full text for the model
		c.input.SetValue("")
		c.pastes = nil
		if strings.HasPrefix(shown, "/") {
			return c.runCommand(text)
		}
		// show the real (expanded) content in the transcript and send it too
		return c, c.beginTurn(text, c.userLine(text))
	}

	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return c, cmd
}

// focusLastBlock moves focus from the input to the nearest (last) expandable
// block, if there is one.
func (c *Chat) focusLastBlock() tea.Cmd {
	idxs := c.focusableItems()
	if len(idxs) == 0 {
		return nil
	}
	c.focus = idxs[len(idxs)-1]
	c.input.Blur()
	return nil
}

// handleBlockKey drives the focused block: up/down (and j/k) move focus or
// scroll an overflowing full view, enter/tab cycles states, esc backs out.
func (c *Chat) handleBlockKey(key string) (tea.Model, tea.Cmd) {
	b := c.focusedBlock()
	if b == nil {
		return c, c.refocusInput()
	}
	scrolling := b.state == blockFull && b.overflows()
	switch key {
	case "up", "k":
		if scrolling {
			b.vp.ScrollUp(1)
			return c, nil
		}
		c.moveFocus(-1)
	case "down", "j":
		if scrolling {
			b.vp.ScrollDown(1)
			return c, nil
		}
		if !c.moveFocus(1) {
			return c, c.refocusInput()
		}
	case "enter", "tab":
		b.cycle(c)
	case "esc":
		if b.state != blockCollapsed {
			b.state = blockCollapsed
			return c, nil
		}
		return c, c.refocusInput()
	}
	return c, nil
}

// moveFocus shifts focus among expandable blocks; false means it walked past
// the last one (back toward the input).
func (c *Chat) moveFocus(dir int) bool {
	idxs := c.focusableItems()
	cur := -1
	for i, idx := range idxs {
		if idx == c.focus {
			cur = i
			break
		}
	}
	next := cur + dir
	if next < 0 {
		return true // already at the first block — stay
	}
	if next >= len(idxs) {
		return false
	}
	c.focus = idxs[next]
	return true
}

func (c *Chat) refocusInput() tea.Cmd {
	c.focus = -1
	return c.input.Focus()
}

func (c *Chat) startTurn(text string) (tea.Model, tea.Cmd) {
	return c, c.beginTurn(text, c.userLine(text))
}

// beginTurn kicks off one agent turn: the previous round flushes to
// scrollback, echo prints above the live view, and the event pump starts.
// echo may be empty for a silent (loop) step.
func (c *Chat) beginTurn(text, echo string) tea.Cmd {
	c.working = true
	c.stream.Reset()
	c.toolLine = ""
	c.toolTarget = ""

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	sess := c.sess
	events := c.events
	go func() {
		sess.Run(ctx, text, func(e agent.Event) { events <- e })
	}()

	c.stick = true // a new turn jumps the viewport back to the bottom
	if echo != "" {
		c.appendText(echo)
	}
	return tea.Sequence(c.waitEvent(), c.setActivity(mascot.ActThinking))
}

// push appends a line to the conversation and pins the viewport to the bottom.
// It stands in for the old tea.Println scrollback calls; the returned nil cmd
// keeps call sites (including inside tea.Sequence) unchanged.
func (c *Chat) push(s string) tea.Cmd {
	c.appendText(s)
	c.stick = true
	return nil
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
		c.appendText(c.s.Accent.Render("  ✓ ") + c.s.Dim.Render("loop done — agent reported the task complete"))
		return nil
	}
	c.loopLeft--
	if c.loopLeft <= 0 {
		c.loopActive = false
		c.appendText(c.s.Dim.Render(fmt.Sprintf("  loop stopped after %d iterations", c.loopTotal)))
		return nil
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
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return mascotRestMsg{} })
}

func (c *Chat) handleEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case agent.TextEvent:
		c.stream.WriteString(e.Text)
		return c, c.waitEvent()

	case agent.ToolStartEvent:
		if s := strings.TrimSpace(c.stream.String()); s != "" {
			c.appendText(c.loyiLine(s))
			c.stream.Reset()
		}
		c.toolLine = e.Summary
		c.toolTarget = targetOf(e.Summary)
		return c, tea.Batch(c.setActivity(activityForTool(e.Name)), c.waitEvent())

	case agent.ToolResultEvent:
		c.toolLine = ""
		c.appendBlock(c.newBlock(e))
		// after a tool, the model is thinking again
		return c, tea.Batch(c.setActivity(mascot.ActThinking), c.waitEvent())

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
		if final != "" {
			c.appendText(c.loyiLine(final))
		}
		if cont := c.loopNext(final); cont != nil {
			return c, cont
		}
		// no more loop work — flash success, then settle back to ready
		return c, tea.Sequence(c.setActivity(mascot.ActSuccess), restMascot())

	case agent.ErrorEvent:
		c.working = false
		c.cancel = nil
		c.stopLoop()
		if s := strings.TrimSpace(c.stream.String()); s != "" {
			c.appendText(c.loyiLine(s))
			c.stream.Reset()
		}
		c.appendText(indent(c.s.Danger.Render("✗ ") + c.s.Dim.Render(e.Err.Error())))
		return c, tea.Sequence(c.setActivity(mascot.ActError), restMascot())
	}
	return c, c.waitEvent()
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
		return altView("")
	}
	if c.pickerActive {
		return altView(c.pickerView())
	}
	if c.agentPickerActive {
		return altView(c.agentPickerView())
	}
	if c.themePickerActive {
		return altView(c.themePickerView())
	}
	if c.monitorActive {
		return altView(c.monitorView())
	}

	header := c.header()
	footer := c.footer()
	if c.pickerLoading {
		footer += "\n" + indent(c.s.Dim.Render("listing models…"))
	}

	// The viewport fills the space between the header and footer.
	vpH := c.height - lipgloss.Height(header) - lipgloss.Height(footer) - 1
	if vpH < 1 {
		vpH = 1
	}
	c.vp.SetWidth(max(c.width, 1))
	c.vp.SetHeight(vpH)

	// Rebuild content only when it changes so scroll position survives the
	// mascot/word ticks that re-render every couple of seconds.
	if content := c.transcript(); content != c.lastContent {
		c.lastContent = content
		c.vp.SetContent(content)
	}
	if c.stick {
		c.vp.GotoBottom()
	}

	return altView(header + "\n" + c.vp.View() + "\n" + footer)
}

// altView wraps content in a full-screen alt-screen view with loyi's warm
// background, so launching loyi clears the terminal and restores it on exit.
func altView(s string) tea.View {
	v := tea.NewView(s)
	v.AltScreen = true
	// Deliberately no mouse capture: grabbing the mouse would break kitty's
	// copy-on-select and right-click paste. Scroll with the keyboard instead.
	v.BackgroundColor = lipgloss.Color(theme.Neutrals.Background)
	return v
}

// transcript renders the whole conversation for the scrolling viewport: every
// message and tool block, the live streaming text, the running tool line, and
// any pending permission card.
func (c *Chat) transcript() string {
	var parts []string
	for i, it := range c.items {
		s := it.text
		if it.block != nil {
			s = c.blockView(it.block, i == c.focus)
		}
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(c.stream.String()); s != "" {
		parts = append(parts, c.loyiLine(s))
	}
	if c.toolLine != "" {
		parts = append(parts, toolIndent()+c.s.Dim.Render(c.word+"  ")+c.s.Text.Render(c.toolLine))
	}
	if c.pending != nil {
		parts = append(parts, c.permissionCard())
	}
	return strings.Join(parts, "\n\n")
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

// agentPickerView renders the agent chooser: each persona with its tagline,
// the active one marked, cursor on the highlighted row.
func (c *Chat) agentPickerView() string {
	var b strings.Builder
	b.WriteString(indent(c.s.Text.Render("switch agent")) + "\n\n")
	for i, a := range agent.Agents {
		cursor := "  "
		label := c.s.Dim.Render(padTo(a.Label, 8))
		if i == c.agentPickerIdx {
			cursor = c.s.Accent.Render("› ")
			label = c.s.Text.Render(padTo(a.Label, 8))
		}
		mark := "  "
		if a.ID == c.sess.Agent.ID {
			mark = c.s.Accent.Render("• ")
		}
		b.WriteString(strings.Repeat(" ", pad) + cursor + mark + label + c.s.Dim.Render(a.Tagline) + "\n")
	}
	b.WriteString("\n" + indent(c.s.Dim.Render("↑↓ move   ⏎ switch   esc cancel")))
	return b.String()
}

// themePickerView renders the accent chooser with a color swatch per theme,
// the active one marked; the whole UI previews as you move.
func (c *Chat) themePickerView() string {
	var b strings.Builder
	b.WriteString(indent(c.s.Text.Render("pick your accent")) + "\n\n")
	for i, t := range theme.All() {
		cursor := "  "
		name := c.s.Dim.Render(padTo(t.Name, 8))
		if i == c.themePickerIdx {
			cursor = c.s.Accent.Render("› ")
			name = c.s.Text.Render(padTo(t.Name, 8))
		}
		swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Render("●")
		b.WriteString(strings.Repeat(" ", pad) + cursor + swatch + " " + name + "\n")
	}
	b.WriteString("\n" + indent(c.s.Dim.Render("↑↓ preview   ⏎ apply   esc cancel")))
	return b.String()
}

// monitorView renders the live team pyramid: the orchestrating agent at the
// top, its sub-agents fanned out below with status, activity, and how long each
// has been working. It refreshes a few times a second while the team runs.
func (c *Chat) monitorView() string {
	nodes := c.orch.Snapshot()
	running := 0
	for _, n := range nodes {
		if n.Status == agent.RunRunning {
			running++
		}
	}

	var b strings.Builder
	title := c.s.Text.Render("team monitor")
	switch {
	case running > 0:
		title += "   " + c.s.Accent.Render(fmt.Sprintf("%d working", running))
	case len(nodes) > 0:
		title += "   " + c.s.Dim.Render("all done")
	}
	b.WriteString(indent(title) + "\n\n")

	// the root: the session's own agent, orchestrating
	rootDot := c.s.Dim.Render("○")
	sub := "idle"
	if running > 0 {
		rootDot = c.s.Accent.Render("●")
		sub = "orchestrating the team"
	}
	b.WriteString(strings.Repeat(" ", pad) + rootDot + " " + c.s.Text.Render(c.sess.Agent.Label) + "  " + c.s.Dim.Render(sub) + "\n")

	if len(nodes) == 0 {
		b.WriteString("\n" + indent(c.s.Dim.Render("no sub-agents yet — switch to construct and give it a goal")))
		b.WriteString("\n\n" + indent(c.s.Dim.Render("⌃t or esc to close")))
		return b.String()
	}

	for i, n := range nodes {
		conn := "├─"
		if i == len(nodes)-1 {
			conn = "└─"
		}
		var dot string
		switch n.Status {
		case agent.RunFailed:
			dot = c.s.Danger.Render("✗")
		case agent.RunDone:
			dot = c.s.Dim.Render("✓")
		default:
			dot = c.s.Accent.Render("●")
		}
		activity := shorten(n.Task, 36)
		switch {
		case n.Status == agent.RunRunning && n.Activity != "":
			activity = shorten(n.Activity, 36)
		case n.Status == agent.RunDone:
			activity = "done · " + shorten(n.Task, 28)
		case n.Status == agent.RunFailed:
			activity = "failed"
		}
		row := strings.Repeat(" ", pad) + c.s.Dim.Render(conn+" ") + dot + " " +
			c.s.Text.Render(padTo(n.Agent, 9)) + c.s.Dim.Render(padTo(activity, 40)+elapsed(n.Elapsed()))
		b.WriteString(row + "\n")
	}
	b.WriteString("\n" + indent(c.s.Dim.Render("live · ⌃t or esc to close")))
	return b.String()
}

// permissionCard renders the pending approval as a small bordered card in the
// conversation flow, indented under loyi's message. Only the key letters get
// the accent; everything else stays dim.
func (c *Chat) permissionCard() string {
	key := func(k, label string) string {
		return c.s.Dim.Render("[") + c.s.Accent.Render(k) + c.s.Dim.Render("] "+label)
	}
	question := c.s.Dim.Render("allow " + c.pending.Summary + "?")
	keys := key("y", "yes") + "   " + key("n", "no") + "   " + key("a", "always")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Neutrals.Border)).
		Padding(0, 1).
		MarginLeft(pad + 2).
		Render(question + "\n" + keys)
}

// ruleFileName names where an always-rule lands, for the confirmation note.
func ruleFileName(set *config.Settings) string {
	if set == nil || set.RuleFile() == "" {
		return "loyi.json"
	}
	if _, project, hasProject := set.Sources(); hasProject && set.RuleFile() == project {
		return "loyi.json"
	}
	return "~/.loyi/loyi.json"
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
		left = c.pupView() + c.s.Dim.Render("waiting on you")
	} else {
		wordStyle := c.s.Dim
		switch c.pup.State() {
		case mascot.Success:
			wordStyle = c.s.Accent
		case mascot.Error:
			wordStyle = c.s.Danger
		}
		left = c.pupView() + wordStyle.Render(c.word)
		switch {
		case c.focus >= 0:
			right = c.s.Dim.Render("↑↓ move   ⏎ toggle   esc back")
		case c.working:
			right = c.s.Dim.Render("⌃c stop")
		case len(c.focusableItems()) > 0:
			right = c.s.Dim.Render("↑↓ scroll   ⇥ blocks   ⏎ send")
		default:
			right = c.s.Dim.Render("↑↓ scroll   ⏎ send   ⌃c quit")
		}
		// while a team runs, advertise the live monitor
		if c.orch != nil && c.orch.Active() > 0 {
			right = c.s.Accent.Render(fmt.Sprintf("⌃t team (%d)", c.orch.Active())) + c.s.Dim.Render("  ·  ") + right
		}
		// surface a looser permission mode so it's never a surprise
		if c.sess.Perm != "" && c.sess.Perm != agent.PermAsk {
			right = c.s.Accent.Render(c.sess.Perm.Label()) + c.s.Dim.Render("  ·  ") + right
		}
	}

	gap := cw - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return lead + left + strings.Repeat(" ", gap) + right
}

// pupView is the status-line mascot (plus its gap), or nothing when loyi.json
// turns the mascot off.
func (c *Chat) pupView() string {
	if c.set != nil && !c.set.MascotEnabled() {
		return ""
	}
	return c.pup.View() + "  "
}
