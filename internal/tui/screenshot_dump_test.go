package tui

// Temporary harness: dumps ANSI renders of every screen to LOYI_SHOT_DIR so
// they can be turned into screenshots. Not part of the suite — skips unless
// the env var is set. Delete freely.

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/loveranmar/loyi/internal/agent"
	"github.com/loveranmar/loyi/internal/config"
	"github.com/loveranmar/loyi/internal/mascot"
	"github.com/loveranmar/loyi/internal/provider/factory"
	"github.com/loveranmar/loyi/internal/theme"
)

func TestDumpScreens(t *testing.T) {
	dir := os.Getenv("LOYI_SHOT_DIR")
	if dir == "" {
		t.Skip("set LOYI_SHOT_DIR to dump screen renders")
	}
	dump := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name+".ans"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	newChatFor := func() *Chat {
		cfg := &config.Config{Theme: theme.Default.Name}
		sess := &agent.Session{Agent: agent.Agents[1]}
		c := NewChat(cfg, sess, theme.Default)
		c.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
		c.input.Focus()
		return c
	}

	// 1. chat, fresh start
	c := newChatFor()
	dump("chat-1-idle", c.banner()+"\n"+c.View().Content)

	// transcript shared by the conversation screens
	transcript := c.banner() + "\n" +
		"\n" + c.userLine("build me a landing page") + "\n"

	// 2. chat mid-turn: streaming reply + live tool action, accent border
	c2 := newChatFor()
	c2.working = true
	c2.stream.WriteString("i'll put together a landing page for you.")
	c2.toolLine = "writing index.html"
	c2.toolTarget = "index.html"
	c2.word = "sniffing…"
	c2.pup.SetState(mascot.Thinking)
	dump("chat-2-working", transcript+c2.View().Content)

	// 3. chat, permission prompt
	c3 := newChatFor()
	c3.working = true
	c3.stream.WriteString("i'll put together a landing page for you.")
	c3.pending = &agent.PermissionEvent{Summary: "write index.html"}
	c3.word = "waiting on you"
	c3.pup.SetState(mascot.Listening)
	dump("chat-3-permission", transcript+c3.View().Content)

	// 4. chat, finished conversation (the target layout)
	c4 := newChatFor()
	c4.toolTarget = "index.html"
	done := transcript +
		"\n" + c4.loyiLine("i'll put together a landing page for you.") + "\n" +
		"\n" + c4.toolResultLine(agent.ToolResultEvent{Name: "write", Output: "wrote index.html · 1 line"}) + "\n" +
		"\n" + c4.loyiLine("done — starter landing page is in index.html.\nwant a signup form next?") + "\n"
	dump("chat-4-conversation", done+c4.View().Content)

	// 5. model picker
	c5 := newChatFor()
	c5.pickerActive = true
	c5.pickerEntries = []factory.ModelEntry{
		{Provider: "anthropic", Model: "claude-sonnet-5"},
		{Provider: "anthropic", Model: "claude-opus-4-8"},
		{Provider: "openai", Model: "gpt-5.2"},
	}
	c5.pickerIdx = 1
	dump("chat-5-model-picker", c5.View().Content)

	// 6. mascot states, all five faces
	s := theme.Default.Styles()
	strip := ""
	for _, st := range []struct {
		st    mascot.State
		label string
	}{
		{mascot.Idle, "idle / ready"},
		{mascot.Listening, "listening"},
		{mascot.Thinking, "thinking"},
		{mascot.Success, "success"},
		{mascot.Error, "error"},
	} {
		strip += "  " + mascot.Render(mascot.Mini, st.st, theme.Default) + "   " + s.Dim.Render(st.label) + "\n"
	}
	strip += "\n" + indent(mascot.Render(mascot.Full, mascot.Idle, theme.Default)) + "\n"
	dump("mascot-states", strip)

	// onboarding screens
	o := NewOnboarding()
	o.width, o.height = 64, 20
	dump("onboarding-1-splash", o.viewSplash())

	o.step = stepName
	o.nameInput.Focus()
	dump("onboarding-2-name", o.View().Content)

	o.step = stepTheme
	dump("onboarding-3-theme", o.View().Content)

	o.step = stepSetup
	dump("onboarding-4-setup", o.View().Content)

	ocfg := &config.Config{Name: "love", Theme: theme.Default.Name}
	ocfg.SetProvider("anthropic", &config.Provider{Auth: "oauth"})
	od := NewSetup(ocfg)
	od.width, od.height = 64, 20
	od.step = stepDone
	dump("onboarding-5-done", od.View().Content)
}
