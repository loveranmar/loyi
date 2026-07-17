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

// face holds the exact frames for a state. primary is the resting frame; alt
// is the blink (idle/listening) or flutter (thinking) frame — empty for the
// static success/error states. eye is the glyph used by the multi-line Full
// variant, which substitutes rather than using literal frames. accent/danger
// pick a non-default color.
type face struct {
	primary string
	alt     string
	eye     string
	accent  bool
	danger  bool
}

// faces is the single place to tweak how each state looks. The mini frames are
// written out literally so the exact spacing matches the brand spec.
var faces = map[State]face{
	Idle:      {primary: "ฅ(•ᴥ•)ฅ", alt: "ฅ(-ᴥ-)ฅ", eye: "•"},
	Listening: {primary: "ฅ(o ᴥ o)ฅ", alt: "ฅ(- ᴥ -)ฅ", eye: "o"},
	Thinking:  {primary: "ฅ(- ᴥ -)ฅ", alt: "ฅ(• ᴥ •)ฅ", eye: "•"}, // flutters
	Success:   {primary: "ฅ(^ᴥ^)ฅ", eye: "^", accent: true},
	Error:     {primary: "ฅ(x ᴥ x)ฅ", eye: "x", danger: true},
}

// fullBody renders the multi-line Full variant with the eye glyph substituted.
func fullBody(eye string) string {
	return "(\\_/)\n( " + eye + "ᴥ" + eye + " )\n/>   \\"
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

// Render draws a static frame — for onboarding, version, and any non-animated
// spot.
func Render(v Variant, st State, th theme.Theme) string {
	f := faces[st]
	if v == Full {
		return styleFor(f, th).Render(fullBody(f.eye))
	}
	return styleFor(f, th).Render(f.primary)
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
	if m.Variant == Full {
		return styleFor(f, m.th).Render(fullBody(f.eye))
	}
	frame := f.primary
	if m.swap && f.alt != "" {
		frame = f.alt
	}
	return styleFor(f, m.th).Render(frame)
}
