// Package tui holds loyi's bubbletea app.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/loveranmar/loyi/internal/auth"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/mascot"
	"github.com/loveranmar/loyi/internal/theme"
)

const logo = `    __            _
   / /___  __  __(_)
  / / __ \/ / / / /
 / / /_/ / /_/ / /
/_/\____/\__, /_/
        /____/`

const tagline = "your agentic cli, for people who actually ship."

type step int

const (
	stepSplash step = iota
	stepName
	stepTheme
	stepSetup
	stepClaudeAuth
	stepCodexAuth
	stepProvider
	stepCustomURL
	stepCustomModel
	stepKey
	stepDone
)

type splashDoneMsg struct{}

type codexCallbackMsg struct{ result auth.CallbackResult }

type authDoneMsg struct {
	provider string
	tokens   auth.Tokens
	err      error
}

var apiKeyProviders = []string{"anthropic", "openai", "openrouter", "custom"}

// Onboarding is the first-run flow: splash, name, theme, provider setup.
// It doubles as the `loyi setup` screen, starting at the setup step.
type Onboarding struct {
	cfg    *config.Config
	th     theme.Theme
	s      theme.Styles
	step   step
	width  int
	height int

	nameInput  textinput.Model
	keyInput   textinput.Model
	authInput  textinput.Model
	urlInput   textinput.Model
	modelInput textinput.Model

	themeIdx int
	setupIdx int
	provIdx  int

	customURL   string
	customModel string

	claudeFlow *auth.AnthropicFlow
	codexFlow  *auth.OpenAIFlow
	codexSrv   *auth.CallbackServer

	busy                bool
	authErr             string
	saveErr             error
	claudeCodeAvailable bool
}

// setupAction identifies a choice on the provider-setup screen. Using actions
// instead of raw indices keeps dispatch stable when the Claude Code import
// option is shown or hidden.
type setupAction int

const (
	actImportClaudeCode setupAction = iota
	actClaudeLogin
	actCodexLogin
	actAPIKey
	actSkip
)

func NewOnboarding() *Onboarding {
	return newApp(&config.Config{Theme: theme.Default.Name}, stepSplash)
}

// NewSetup jumps straight to provider setup with an existing config.
func NewSetup(cfg *config.Config) *Onboarding {
	if cfg.Theme == "" {
		cfg.Theme = theme.Default.Name
	}
	return newApp(cfg, stepSetup)
}

func newApp(cfg *config.Config, start step) *Onboarding {
	th := theme.Get(cfg.Theme)

	o := &Onboarding{cfg: cfg, th: th, s: th.Styles(), step: start}
	o.claudeCodeAvailable = auth.ClaudeCodeAvailable()

	o.nameInput = newInput("your name")
	o.nameInput.CharLimit = 32
	o.keyInput = newInput("paste your api key")
	o.keyInput.EchoMode = textinput.EchoPassword
	o.keyInput.EchoCharacter = '•'
	o.authInput = newInput("paste the code here")
	o.urlInput = newInput("https://my-endpoint.example/v1")
	o.modelInput = newInput("model id, e.g. llama-3.3-70b")

	o.restyleInputs()
	return o
}

func newInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Placeholder = placeholder
	in.SetVirtualCursor(true)
	return in
}

func (o *Onboarding) inputs() []*textinput.Model {
	return []*textinput.Model{&o.nameInput, &o.keyInput, &o.authInput, &o.urlInput, &o.modelInput}
}

func (o *Onboarding) restyleInputs() {
	st := textinput.DefaultDarkStyles()
	st.Focused.Prompt = o.s.Accent
	st.Blurred.Prompt = o.s.Accent
	st.Focused.Text = o.s.Text
	st.Blurred.Text = o.s.Text
	st.Focused.Placeholder = o.s.Dim
	st.Blurred.Placeholder = o.s.Dim
	st.Cursor.Color = lipgloss.Color(o.th.Accent)
	for _, in := range o.inputs() {
		in.SetStyles(st)
		in.Prompt = "> "
	}
}

func (o *Onboarding) Init() tea.Cmd {
	if o.step == stepSplash {
		return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return splashDoneMsg{} })
	}
	return nil
}

func (o *Onboarding) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		o.width, o.height = msg.Width, msg.Height
		return o, nil

	case splashDoneMsg:
		if o.step == stepSplash {
			o.step = stepName
			return o, o.nameInput.Focus()
		}
		return o, nil

	case codexCallbackMsg:
		if o.step != stepCodexAuth {
			return o, nil
		}
		if msg.result.Err != nil {
			o.authErr = msg.result.Err.Error()
			return o, nil
		}
		o.busy = true
		o.authErr = ""
		return o, o.exchangeCodex(msg.result.Code)

	case authDoneMsg:
		o.busy = false
		if msg.err != nil {
			o.authErr = msg.err.Error()
			return o, nil
		}
		o.closeCodexServer()
		switch msg.provider {
		case "anthropic":
			o.cfg.SetProvider("anthropic", &config.Provider{
				Auth:    "oauth",
				Access:  msg.tokens.Access,
				Refresh: msg.tokens.Refresh,
				Expires: msg.tokens.Expires,
			})
		case "chatgpt":
			acct := auth.ChatGPTAccountID(msg.tokens.Access)
			if acct == "" {
				o.authErr = "couldn't read the chatgpt account from the login — is this a plus/pro account?"
				return o, nil
			}
			o.cfg.SetProvider("chatgpt", &config.Provider{
				Auth:      "oauth",
				Access:    msg.tokens.Access,
				Refresh:   msg.tokens.Refresh,
				Expires:   msg.tokens.Expires,
				AccountID: acct,
			})
		}
		o.finish()
		return o, nil

	case tea.PasteMsg:
		if in := o.focusedInput(); in != nil {
			var cmd tea.Cmd
			*in, cmd = in.Update(msg)
			return o, cmd
		}
		return o, nil

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			o.closeCodexServer()
			return o, tea.Quit
		}
		if msg.String() == "ctrl+v" && o.focusedInput() != nil {
			return o, pasteFromClipboard
		}
		return o.handleKey(msg)
	}
	return o, nil
}

// focusedInput returns the text input the current step types into, if any.
func (o *Onboarding) focusedInput() *textinput.Model {
	switch o.step {
	case stepName:
		return &o.nameInput
	case stepClaudeAuth, stepCodexAuth:
		return &o.authInput
	case stepCustomURL:
		return &o.urlInput
	case stepCustomModel:
		return &o.modelInput
	case stepKey:
		return &o.keyInput
	}
	return nil
}

func (o *Onboarding) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch o.step {
	case stepSplash:
		o.step = stepName
		return o, o.nameInput.Focus()

	case stepName:
		if msg.String() == "enter" {
			if strings.TrimSpace(o.nameInput.Value()) == "" {
				return o, nil
			}
			o.cfg.Name = strings.TrimSpace(o.nameInput.Value())
			o.nameInput.Blur()
			o.step = stepTheme
			return o, nil
		}
		return o.updateInput(&o.nameInput, msg)

	case stepTheme:
		all := theme.All()
		switch msg.String() {
		case "up", "k":
			o.themeIdx = (o.themeIdx + len(all) - 1) % len(all)
		case "down", "j":
			o.themeIdx = (o.themeIdx + 1) % len(all)
		case "enter":
			o.step = stepSetup
			return o, nil
		}
		o.th = all[o.themeIdx]
		o.s = o.th.Styles()
		o.cfg.Theme = o.th.Name
		o.restyleInputs()
		return o, nil

	case stepSetup:
		items := o.setupItems()
		switch msg.String() {
		case "up", "k":
			o.setupIdx = (o.setupIdx + len(items) - 1) % len(items)
		case "down", "j":
			o.setupIdx = (o.setupIdx + 1) % len(items)
		case "enter":
			o.authErr = ""
			switch o.setupActions()[o.setupIdx] {
			case actImportClaudeCode:
				return o.importClaudeCode()
			case actClaudeLogin:
				return o.enterClaudeAuth()
			case actCodexLogin:
				return o.enterCodexAuth()
			case actAPIKey:
				o.step = stepProvider
			case actSkip:
				o.finish()
			}
		}
		return o, nil

	case stepClaudeAuth:
		switch msg.String() {
		case "esc":
			o.authInput.Blur()
			o.authInput.SetValue("")
			o.busy = false
			o.step = stepSetup
			return o, nil
		case "enter":
			code := strings.TrimSpace(o.authInput.Value())
			if code == "" || o.busy {
				return o, nil
			}
			o.busy = true
			o.authErr = ""
			return o, o.exchangeClaude(code)
		}
		return o.updateInput(&o.authInput, msg)

	case stepCodexAuth:
		switch msg.String() {
		case "esc":
			o.closeCodexServer()
			o.authInput.Blur()
			o.authInput.SetValue("")
			o.busy = false
			o.step = stepSetup
			return o, nil
		case "enter":
			code := auth.ParseOpenAICode(o.authInput.Value())
			if code == "" || o.busy {
				return o, nil
			}
			o.busy = true
			o.authErr = ""
			return o, o.exchangeCodex(code)
		}
		return o.updateInput(&o.authInput, msg)

	case stepProvider:
		switch msg.String() {
		case "up", "k":
			o.provIdx = (o.provIdx + len(apiKeyProviders) - 1) % len(apiKeyProviders)
		case "down", "j":
			o.provIdx = (o.provIdx + 1) % len(apiKeyProviders)
		case "esc":
			o.step = stepSetup
		case "enter":
			if apiKeyProviders[o.provIdx] == "custom" {
				o.step = stepCustomURL
				return o, o.urlInput.Focus()
			}
			o.step = stepKey
			return o, o.keyInput.Focus()
		}
		return o, nil

	case stepCustomURL:
		switch msg.String() {
		case "esc":
			o.urlInput.Blur()
			o.step = stepProvider
			return o, nil
		case "enter":
			u := strings.TrimSpace(o.urlInput.Value())
			if u == "" {
				return o, nil
			}
			if !strings.Contains(u, "://") {
				u = "https://" + u
			}
			o.customURL = strings.TrimRight(u, "/")
			o.urlInput.Blur()
			o.step = stepCustomModel
			return o, o.modelInput.Focus()
		}
		return o.updateInput(&o.urlInput, msg)

	case stepCustomModel:
		switch msg.String() {
		case "esc":
			o.modelInput.Blur()
			o.step = stepCustomURL
			return o, o.urlInput.Focus()
		case "enter":
			m := strings.TrimSpace(o.modelInput.Value())
			if m == "" {
				return o, nil
			}
			o.customModel = m
			o.modelInput.Blur()
			o.step = stepKey
			return o, o.keyInput.Focus()
		}
		return o.updateInput(&o.modelInput, msg)

	case stepKey:
		switch msg.String() {
		case "esc":
			o.keyInput.Blur()
			o.keyInput.SetValue("")
			if apiKeyProviders[o.provIdx] == "custom" {
				o.step = stepCustomModel
				return o, o.modelInput.Focus()
			}
			o.step = stepProvider
			return o, nil
		case "enter":
			id := apiKeyProviders[o.provIdx]
			k := strings.TrimSpace(o.keyInput.Value())
			if k == "" && id != "custom" {
				return o, nil
			}
			p := &config.Provider{Auth: "api_key", APIKey: k}
			if id == "custom" {
				p.BaseURL = o.customURL
				p.Model = o.customModel
			}
			o.cfg.SetProvider(id, p)
			o.keyInput.Blur()
			o.finish()
			return o, nil
		}
		return o.updateInput(&o.keyInput, msg)

	case stepDone:
		return o, tea.Quit
	}
	return o, nil
}

func (o *Onboarding) updateInput(in *textinput.Model, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	*in, cmd = in.Update(msg)
	return o, cmd
}

func (o *Onboarding) enterClaudeAuth() (tea.Model, tea.Cmd) {
	flow, err := auth.NewAnthropicFlow()
	if err != nil {
		o.authErr = err.Error()
		return o, nil
	}
	o.claudeFlow = flow
	o.step = stepClaudeAuth
	openBrowser(flow.URL)
	return o, o.authInput.Focus()
}

func (o *Onboarding) enterCodexAuth() (tea.Model, tea.Cmd) {
	flow, err := auth.NewOpenAIFlow()
	if err != nil {
		o.authErr = err.Error()
		return o, nil
	}
	o.codexFlow = flow
	o.step = stepCodexAuth
	openBrowser(flow.URL)

	var cmds []tea.Cmd
	cmds = append(cmds, o.authInput.Focus())
	srv, err := auth.StartCallbackServer(flow.State)
	if err != nil {
		o.authErr = "couldn't listen on port 1455 (another login in progress?) — paste the redirect url below instead"
	} else {
		o.codexSrv = srv
		cmds = append(cmds, func() tea.Msg { return codexCallbackMsg{<-srv.Result} })
	}
	return o, tea.Batch(cmds...)
}

func (o *Onboarding) exchangeClaude(code string) tea.Cmd {
	flow := o.claudeFlow
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tokens, err := flow.Exchange(ctx, code)
		return authDoneMsg{provider: "anthropic", tokens: tokens, err: err}
	}
}

func (o *Onboarding) exchangeCodex(code string) tea.Cmd {
	flow := o.codexFlow
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		tokens, err := flow.Exchange(ctx, code)
		return authDoneMsg{provider: "chatgpt", tokens: tokens, err: err}
	}
}

func (o *Onboarding) closeCodexServer() {
	if o.codexSrv != nil {
		o.codexSrv.Close()
		o.codexSrv = nil
	}
}

// setupActions is the ordered list of choices on the setup screen. The Claude
// Code import only appears when a login is actually available to import.
func (o *Onboarding) setupActions() []setupAction {
	var a []setupAction
	if o.claudeCodeAvailable {
		a = append(a, actImportClaudeCode)
	}
	return append(a, actClaudeLogin, actCodexLogin, actAPIKey, actSkip)
}

func (o *Onboarding) setupItems() []string {
	labels := map[setupAction]string{
		actImportClaudeCode: "import your claude code login · no browser, no rate limits",
		actClaudeLogin:      "log in with claude",
		actCodexLogin:       "log in with chatgpt · personal use",
		actAPIKey:           "use an api key",
		actSkip:             "skip for now",
	}
	acts := o.setupActions()
	items := make([]string, len(acts))
	for i, a := range acts {
		items[i] = labels[a]
	}
	return items
}

// importClaudeCode reuses the subscription tokens Claude Code already stored,
// skipping the browser OAuth flow (and its rate-limited token endpoint).
func (o *Onboarding) importClaudeCode() (tea.Model, tea.Cmd) {
	toks, err := auth.ImportClaudeCode()
	if err != nil {
		o.authErr = err.Error()
		return o, nil
	}
	o.cfg.SetProvider("anthropic", &config.Provider{
		Auth:    "oauth",
		Access:  toks.Access,
		Refresh: toks.Refresh,
		Expires: toks.Expires,
	})
	o.finish()
	return o, nil
}

func (o *Onboarding) finish() {
	o.cfg.Onboarded = true
	o.saveErr = o.cfg.Save()
	o.step = stepDone
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// --- views ---

func (o *Onboarding) View() tea.View {
	var body string
	switch o.step {
	case stepSplash:
		body = o.viewSplash()
	case stepName:
		body = o.page("what should we call you?", o.nameInput.View(), "enter to continue")
	case stepTheme:
		body = o.viewTheme()
	case stepSetup:
		body = o.viewSetup()
	case stepClaudeAuth:
		body = o.viewClaudeAuth()
	case stepCodexAuth:
		body = o.viewCodexAuth()
	case stepProvider:
		body = o.page("which provider?",
			o.list(apiKeyProviders, o.provIdx),
			"↑↓ to move  ·  enter to choose  ·  esc to go back")
	case stepCustomURL:
		body = o.page("custom provider — base url",
			o.urlInput.View()+"\n\n"+o.s.Dim.Render("any openai-compatible endpoint works (ollama, vllm, llama.cpp, a proxy)"),
			"enter to continue  ·  esc to go back")
	case stepCustomModel:
		body = o.page("custom provider — model", o.modelInput.View(), "enter to continue  ·  esc to go back")
	case stepKey:
		hint := "stored in your loyi config, never committed anywhere  ·  enter to save  ·  esc to go back"
		title := fmt.Sprintf("paste your %s api key", apiKeyProviders[o.provIdx])
		content := o.keyInput.View()
		if apiKeyProviders[o.provIdx] == "custom" {
			content += "\n\n" + o.s.Dim.Render("no key? just press enter")
		}
		if apiKeyProviders[o.provIdx] == "anthropic" {
			content += "\n\n" + o.s.Dim.Render("a claude code token works too — run `claude setup-token` and paste what it prints")
		}
		body = o.page(title, content, hint)
	case stepDone:
		body = o.viewDone()
	}

	v := tea.NewView(body)
	v.AltScreen = true
	v.BackgroundColor = lipgloss.Color(theme.Neutrals.Background)
	return v
}

// page lays out a screen: dim brand, a bright title, content, a dim hint.
// Left-aligned with generous padding; no boxes, no borders.
func (o *Onboarding) page(title, content, hint string) string {
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString("   " + o.s.Dim.Render("loyi") + "\n\n\n")
	if title != "" {
		b.WriteString("   " + o.s.Text.Render(title) + "\n\n")
	}
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("   " + line + "\n")
	}
	b.WriteString("\n\n   " + o.s.Dim.Render(hint) + "\n")
	return b.String()
}

func (o *Onboarding) wrap(s string) string {
	w := o.width - 6
	if w < 20 {
		w = 74
	}
	var lines []string
	for len(s) > w {
		lines = append(lines, s[:w])
		s = s[w:]
	}
	lines = append(lines, s)
	return strings.Join(lines, "\n")
}

func (o *Onboarding) viewSplash() string {
	pup := mascot.Render(mascot.Full, mascot.Idle, o.th)
	art := o.s.Accent.Render(logo) + "\n\n" + pup + "\n\n" + o.s.Dim.Render(tagline)
	if o.width > 0 && o.height > 0 {
		return lipgloss.Place(o.width, o.height, lipgloss.Center, lipgloss.Center, art)
	}
	return art
}

func (o *Onboarding) list(items []string, selected int) string {
	var b strings.Builder
	for i, it := range items {
		if i == selected {
			b.WriteString(o.s.Accent.Render("› ") + o.s.Text.Render(it))
		} else {
			b.WriteString("  " + o.s.Dim.Render(it))
		}
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (o *Onboarding) viewTheme() string {
	all := theme.All()
	var b strings.Builder
	for i, t := range all {
		caret := "  "
		name := o.s.Dim.Render(t.Name)
		if i == o.themeIdx {
			caret = o.s.Accent.Render("› ")
			name = o.s.Text.Render(t.Name)
		}
		swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)).Render("●")
		b.WriteString(caret + swatch + " " + name)
		if i < len(all)-1 {
			b.WriteString("\n")
		}
	}
	return o.page("pick your accent", b.String(), "↑↓ to move  ·  enter to choose")
}

func (o *Onboarding) viewSetup() string {
	content := o.list(o.setupItems(), o.setupIdx)
	if o.authErr != "" {
		content += "\n\n" + o.s.Dim.Render(o.authErr)
	}
	return o.page("connect a model provider", content, "↑↓ to move  ·  enter to choose")
}

func (o *Onboarding) viewClaudeAuth() string {
	var b strings.Builder
	b.WriteString(o.s.Dim.Render("open this url in your browser (loyi tried to open it for you):") + "\n\n")
	b.WriteString(o.s.Accent.Render(o.wrap(o.claudeFlow.URL)) + "\n\n")
	b.WriteString(o.s.Dim.Render("log in, approve access, then paste the code you're given:") + "\n\n")
	b.WriteString(o.authInput.View())
	b.WriteString(o.statusLine())
	return o.page("log in with claude", b.String(), "enter to continue  ·  esc to go back")
}

func (o *Onboarding) viewCodexAuth() string {
	var b strings.Builder
	b.WriteString(o.s.Dim.Render("uses your chatgpt plus/pro subscription through the codex backend.") + "\n")
	b.WriteString(o.s.Dim.Render("this is a personal-use integration — not affiliated with or endorsed by openai.") + "\n\n")
	b.WriteString(o.s.Dim.Render("open this url in your browser (loyi tried to open it for you):") + "\n\n")
	b.WriteString(o.s.Accent.Render(o.wrap(o.codexFlow.URL)) + "\n\n")
	if o.codexSrv != nil {
		b.WriteString(o.s.Dim.Render("waiting for the browser to come back on localhost:1455 …") + "\n")
		b.WriteString(o.s.Dim.Render("or paste the redirect url / code here:") + "\n\n")
	} else {
		b.WriteString(o.s.Dim.Render("paste the redirect url / code here:") + "\n\n")
	}
	b.WriteString(o.authInput.View())
	b.WriteString(o.statusLine())
	return o.page("log in with chatgpt · personal use", b.String(), "enter to continue  ·  esc to go back")
}

func (o *Onboarding) statusLine() string {
	if o.busy {
		return "\n\n" + o.s.Accent.Render("…") + o.s.Dim.Render(" logging you in")
	}
	if o.authErr != "" {
		return "\n\n" + o.s.Text.Render("that didn't work: ") + o.s.Dim.Render(o.authErr)
	}
	return ""
}

func (o *Onboarding) viewDone() string {
	var lines []string
	name := o.cfg.Name
	if name == "" {
		name = "you"
	}
	lines = append(lines, mascot.Render(mascot.Full, mascot.Success, o.th), "")
	lines = append(lines, o.s.Text.Render(fmt.Sprintf("alright, %s. you're set.", name)))

	ids := make([]string, 0, len(o.cfg.Providers))
	for id := range o.cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := o.cfg.Providers[id]
		how := "api key"
		if p.Auth == "oauth" {
			how = "logged in"
		}
		lines = append(lines, "")
		lines = append(lines, o.s.Accent.Render("✓")+o.s.Dim.Render(fmt.Sprintf(" %s · %s", id, how)))
	}
	if o.saveErr != nil {
		lines = append(lines, "", o.s.Text.Render("couldn't save config: "+o.saveErr.Error()))
	}
	return o.page("", strings.Join(lines, "\n"), "press any key to start shipping — then just run: loyi")
}
