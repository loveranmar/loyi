// Package mascot is loyi's wolf-pup companion — a small terminal character
// that reflects what the agent is doing. It ties to the name (loyi = loyal →
// a loyal pup) and recolors with the active theme.
//
// Two variants:
//   - Mini: one line, for the status/prompt area.   ฅ(•ᴥ•)ฅ
//   - Full: three lines, for onboarding and version. (\_/) / ( •ᴥ• ) / />   \
//
// Faces swap the eyes per state; a slow blink keeps it alive while idle, and a
// faster two-frame flutter reads as "working" while thinking. Animation is
// driven by a bubbletea tick, never a blocking sleep.
package mascot

import (
	"math/rand"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/loveranmar/loyi/internal/theme"
)

type Variant int

const (
	Mini Variant = iota
	Full
)

type State int

const (
	Idle      State = iota // waiting, calm       •  •
	Listening              // your turn            o  o
	Thinking               // working              -  -  (flutters)
	Success                // done, in accent      ^  ^
	Error                  // failed, in terracotta x  x
)

// face is the eye set for a state. The mouth is always ᴥ. closed is the
// blink/alternate eye; accent/danger pick a non-default color.
type face struct {
	open   string
	closed string
	accent bool
	danger bool
}

// faces is the single place to tweak how each state looks.
var faces = map[State]face{
	Idle:      {open: "•", closed: "-"},
	Listening: {open: "o", closed: "-"},
	Thinking:  {open: "•", closed: "-"}, // flutters between the two
	Success:   {open: "^", accent: true},
	Error:     {open: "x", danger: true},
}

// body renders a variant with the given eye glyph substituted in.
func body(v Variant, eye string) string {
	switch v {
	case Full:
		return "(\\_/)\n( " + eye + "ᴥ" + eye + " )\n/>   \\"
	default:
		return "ฅ(" + eye + "ᴥ" + eye + ")ฅ"
	}
}

func styleFor(f face, th theme.Theme) lipgloss.Style {
	s := th.Styles()
	switch {
	case f.accent:
		return s.Accent
	case f.danger:
		return s.Danger
	default:
		return s.Dim
	}
}

// Render draws a static frame (open eyes) — for onboarding, version, and any
// non-animated spot.
func Render(v Variant, st State, th theme.Theme) string {
	f := faces[st]
	return styleFor(f, th).Render(body(v, f.open))
}

// TickMsg advances the animation. gen guards against stale ticks left over
// from a previous state.
type TickMsg struct{ gen int }

// Model is an animated mascot. Embed it in a bubbletea model, forward TickMsg
// to Update, set state from your app's activity, and render View.
type Model struct {
	Variant Variant

	state State
	th    theme.Theme
	gen   int
	swap  bool // blink (idle/listening) or flutter frame (thinking)
	rnd   *rand.Rand
}

// New builds an idle mascot for the given variant and theme.
func New(v Variant, th theme.Theme) Model {
	return Model{
		Variant: v,
		state:   Idle,
		th:      th,
		rnd:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Init starts the animation loop.
func (m Model) Init() tea.Cmd { return m.tick(m.restDelay()) }

// State returns the current state.
func (m Model) State() State { return m.state }

// SetTheme recolors the mascot (e.g. when the theme changes).
func (m *Model) SetTheme(th theme.Theme) { m.th = th }

// SetState changes what the mascot is doing and returns the tick command to
// drive the new state's animation (nil for the static Success/Error states).
func (m *Model) SetState(s State) tea.Cmd {
	if m.state == s {
		return nil
	}
	m.state = s
	m.swap = false
	m.gen++
	if s == Success || s == Error {
		return nil // static
	}
	if s == Thinking {
		return m.tick(flutter)
	}
	return m.tick(m.restDelay())
}

const flutter = 400 * time.Millisecond
const blinkHold = 150 * time.Millisecond

// restDelay is the pause between blinks while idle/listening (3–5s).
func (m Model) restDelay() time.Duration {
	return time.Duration(3000+m.rnd.Intn(2000)) * time.Millisecond
}

func (m Model) tick(d time.Duration) tea.Cmd {
	gen := m.gen
	return tea.Tick(d, func(time.Time) tea.Msg { return TickMsg{gen: gen} })
}

// Update advances animation on a matching TickMsg. Other messages pass through
// untouched.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	t, ok := msg.(TickMsg)
	if !ok || t.gen != m.gen {
		return m, nil
	}
	switch m.state {
	case Thinking:
		m.swap = !m.swap
		return m, m.tick(flutter)
	case Idle, Listening:
		m.swap = !m.swap
		if m.swap {
			return m, m.tick(blinkHold) // eyes closed, reopen shortly
		}
		return m, m.tick(m.restDelay())
	default:
		return m, nil // static
	}
}

// View renders the current animation frame, styled for the theme.
func (m Model) View() string {
	f := faces[m.state]
	eye := f.open
	switch m.state {
	case Idle, Listening:
		if m.swap && f.closed != "" {
			eye = f.closed
		}
	case Thinking:
		if m.swap {
			eye = f.closed
		}
	}
	return styleFor(f, m.th).Render(body(m.Variant, eye))
}
