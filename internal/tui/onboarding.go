// Package tui holds loyi's bubbletea app.
package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/loveranmar/loyi/internal/config"
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
	stepProvider
	stepKey
	stepNote
	stepDone
)

type splashDoneMsg struct{}

var providers = []string{"anthropic", "openai", "openrouter"}

// Onboarding is the first-run flow: splash, name, theme, provider setup.
type Onboarding struct {
	cfg    *config.Config
	th     theme.Theme
	s      theme.Styles
	step   step
	width  int
	height int

	nameInput textinput.Model
	keyInput  textinput.Model

	themeIdx int
	setupIdx int
	provIdx  int

	note    string
	saveErr error
}

func NewOnboarding() *Onboarding {
	th := theme.Default

	name := textinput.New()
	name.Placeholder = "your name"
	name.CharLimit = 32
	name.SetVirtualCursor(true)

	key := textinput.New()
	key.Placeholder = "paste your api key"
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'
	key.SetVirtualCursor(true)

	o := &Onboarding{
		cfg:       &config.Config{Theme: th.Name},
		th:        th,
		s:         th.Styles(),
		nameInput: name,
		keyInput:  key,
	}
	o.restyleInputs()
	return o
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
	for _, in := range []*textinput.Model{&o.nameInput, &o.keyInput} {
		in.SetStyles(st)
		in.Prompt = "> "
	}
}

func (o *Onboarding) Init() tea.Cmd {
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return splashDoneMsg{} })
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

	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return o, tea.Quit
		}
		return o.handleKey(msg)
	}
	return o, nil
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
		var cmd tea.Cmd
		o.nameInput, cmd = o.nameInput.Update(msg)
		return o, cmd

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
			switch o.setupIdx {
			case 0:
				o.note = "claude login isn't wired up yet — it lands with the provider work.\nuse an api key in the meantime."
				o.step = stepNote
			case 1:
				o.note = "chatgpt login isn't wired up yet — it lands with the provider work.\nuse an api key in the meantime."
				o.step = stepNote
			case 2:
				o.step = stepProvider
			case 3:
				o.finish()
			}
		}
		return o, nil

	case stepProvider:
		switch msg.String() {
		case "up", "k":
			o.provIdx = (o.provIdx + len(providers) - 1) % len(providers)
		case "down", "j":
			o.provIdx = (o.provIdx + 1) % len(providers)
		case "esc":
			o.step = stepSetup
		case "enter":
			o.step = stepKey
			return o, o.keyInput.Focus()
		}
		return o, nil

	case stepKey:
		switch msg.String() {
		case "esc":
			o.keyInput.Blur()
			o.keyInput.SetValue("")
			o.step = stepProvider
			return o, nil
		case "enter":
			k := strings.TrimSpace(o.keyInput.Value())
			if k == "" {
				return o, nil
			}
			if o.cfg.APIKeys == nil {
				o.cfg.APIKeys = map[string]string{}
			}
			o.cfg.APIKeys[providers[o.provIdx]] = k
			o.keyInput.Blur()
			o.finish()
			return o, nil
		}
		var cmd tea.Cmd
		o.keyInput, cmd = o.keyInput.Update(msg)
		return o, cmd

	case stepNote:
		o.step = stepSetup
		return o, nil

	case stepDone:
		return o, tea.Quit
	}
	return o, nil
}

func (o *Onboarding) setupItems() []string {
	return []string{
		"log in with claude",
		"log in with chatgpt",
		"use an api key",
		"skip for now",
	}
}

func (o *Onboarding) finish() {
	o.cfg.Onboarded = true
	o.saveErr = o.cfg.Save()
	o.step = stepDone
}

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
	case stepProvider:
		body = o.viewProvider()
	case stepKey:
		body = o.page(
			fmt.Sprintf("paste your %s api key", providers[o.provIdx]),
			o.keyInput.View(),
			"stored in your loyi config, never committed anywhere  ·  enter to save  ·  esc to go back",
		)
	case stepNote:
		body = o.page("heads up", o.s.Text.Render(o.note), "press any key to go back")
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
	b.WriteString("   " + o.s.Text.Render(title) + "\n\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("   " + line + "\n")
	}
	b.WriteString("\n\n   " + o.s.Dim.Render(hint) + "\n")
	return b.String()
}

func (o *Onboarding) viewSplash() string {
	art := o.s.Accent.Render(logo) + "\n\n" + o.s.Dim.Render(tagline)
	if o.width > 0 && o.height > 0 {
		return lipgloss.Place(o.width, o.height, lipgloss.Center, lipgloss.Center, art)
	}
	return art
}

func (o *Onboarding) list(items []string, selected int, render func(i int, line string) string) string {
	var b strings.Builder
	for i, it := range items {
		caret := "  "
		line := it
		if render != nil {
			line = render(i, it)
		}
		if i == selected {
			caret = o.s.Accent.Render("› ")
			b.WriteString(caret + o.s.Text.Render(line))
		} else {
			b.WriteString(caret + o.s.Dim.Render(line))
		}
		if i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (o *Onboarding) viewTheme() string {
	all := theme.All()
	var rows []string
	for _, t := range all {
		rows = append(rows, t.Name)
	}
	var b strings.Builder
	for i, t := range all {
		caret := "  "
		name := o.s.Dim.Render(rows[i])
		if i == o.themeIdx {
			caret = o.s.Accent.Render("› ")
			name = o.s.Text.Render(rows[i])
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
	return o.page(
		"connect a model provider",
		o.list(o.setupItems(), o.setupIdx, nil),
		"↑↓ to move  ·  enter to choose",
	)
}

func (o *Onboarding) viewProvider() string {
	return o.page(
		"which provider?",
		o.list(providers, o.provIdx, nil),
		"↑↓ to move  ·  enter to choose  ·  esc to go back",
	)
}

func (o *Onboarding) viewDone() string {
	var lines []string
	lines = append(lines, o.s.Text.Render(fmt.Sprintf("alright, %s. you're set.", o.cfg.Name)))
	if len(o.cfg.APIKeys) > 0 {
		for p := range o.cfg.APIKeys {
			lines = append(lines, "")
			lines = append(lines, o.s.Accent.Render("✓")+o.s.Dim.Render(" "+p+" key saved"))
		}
	}
	if o.saveErr != nil {
		lines = append(lines, "")
		lines = append(lines, o.s.Text.Render("couldn't save config: "+o.saveErr.Error()))
	}
	return o.page("", strings.Join(lines, "\n"), "press any key to start shipping")
}
