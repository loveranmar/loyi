package mascot

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/loveranmar/loyi/internal/theme"
)

func TestRenderVariantsAndFaces(t *testing.T) {
	th := theme.Default
	cases := []struct {
		v    Variant
		s    State
		want string // substring that must appear (ANSI-stripped)
	}{
		{Mini, Idle, "ฅ(•ᴥ•)ฅ"},
		{Mini, Listening, "ฅ(oᴥo)ฅ"},
		{Mini, Thinking, "ฅ(•ᴥ•)ฅ"},
		{Mini, Success, "ฅ(^ᴥ^)ฅ"},
		{Mini, Error, "ฅ(xᴥx)ฅ"},
		{Full, Idle, "( •ᴥ• )"},
		{Full, Success, "( ^ᴥ^ )"},
		{Full, Error, "( xᴥx )"},
	}
	for _, c := range cases {
		got := stripANSI(Render(c.v, c.s, th))
		if !strings.Contains(got, c.want) {
			t.Errorf("Render(%v,%v) = %q, want substring %q", c.v, c.s, got, c.want)
		}
	}
	// full variant is three lines
	if n := strings.Count(stripANSI(Render(Full, Idle, th)), "\n"); n != 2 {
		t.Errorf("full variant should have 3 lines, got %d newlines", n)
	}
}

func TestStateColors(t *testing.T) {
	th := theme.Default // mauve accent
	s := th.Styles()
	if styleFor(faces[Success], th).GetForeground() != s.Accent.GetForeground() {
		t.Error("success should render in the theme accent")
	}
	if styleFor(faces[Error], th).GetForeground() != s.Danger.GetForeground() {
		t.Error("error should render in terracotta danger")
	}
	if styleFor(faces[Idle], th).GetForeground() != s.Dim.GetForeground() {
		t.Error("idle should render in the dim neutral")
	}
	// error's terracotta is constant even under a different-accent theme
	if styleFor(faces[Error], theme.Sage).GetForeground() != lipgloss.Color(theme.Danger) {
		t.Error("error color should be terracotta regardless of theme")
	}
}

func TestBlinkToggles(t *testing.T) {
	m := New(Mini, theme.Default)
	// force a blink frame
	m.gen = 1
	m.swap = false
	m, _ = m.Update(TickMsg{gen: 1})
	if !m.swap {
		t.Fatal("first idle tick should enter the blink (closed) frame")
	}
	if !strings.Contains(stripANSI(m.View()), "ฅ(-ᴥ-)ฅ") {
		t.Errorf("blink frame should show closed eyes, got %q", stripANSI(m.View()))
	}
	// next tick reopens
	m, _ = m.Update(TickMsg{gen: 1})
	if m.swap {
		t.Fatal("second idle tick should reopen the eyes")
	}
}

func TestThinkingFlutters(t *testing.T) {
	m := New(Mini, theme.Default)
	cmd := m.SetState(Thinking)
	if cmd == nil {
		t.Fatal("thinking should return an animation tick")
	}
	open := stripANSI(m.View())
	m, _ = m.Update(TickMsg{gen: m.gen})
	fluttered := stripANSI(m.View())
	if open == fluttered {
		t.Error("thinking frame should change between ticks")
	}
}

func TestStaleTicksIgnored(t *testing.T) {
	m := New(Mini, theme.Default)
	m.SetState(Thinking) // gen becomes 1
	before := m.swap
	// a tick from an older generation must be ignored
	m2, cmd := m.Update(TickMsg{gen: 0})
	if m2.swap != before || cmd != nil {
		t.Error("stale-generation tick should be a no-op")
	}
}

func TestSetStateStaticNoTick(t *testing.T) {
	m := New(Mini, theme.Default)
	if cmd := m.SetState(Success); cmd != nil {
		t.Error("success is static and should not schedule a tick")
	}
	if cmd := m.SetState(Error); cmd != nil {
		t.Error("error is static and should not schedule a tick")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEsc = true
		case inEsc && (r == 'm'):
			inEsc = false
		case inEsc:
			// skip
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
