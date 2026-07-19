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
	"github.com/loveranmar/loyi/internal/tool"
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
		c := NewChat(cfg, nil, sess, agent.NewOrchestrator(), theme.Default)
		c.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
		c.input.Focus()
		return c
	}
	// seed adds the greeting + a user turn so the conversation screens have
	// something in the viewport, like a real session.
	seed := func(c *Chat) *Chat {
		c.appendText(c.greeting())
		c.appendText(c.userLine("build me a landing page"))
		return c
	}

	// 1. chat, fresh start
	c := seed(newChatFor())
	dump("chat-1-idle", c.View().Content)

	// 2. chat mid-turn: streaming reply + live tool action, accent border
	c2 := seed(newChatFor())
	c2.working = true
	c2.stream.WriteString("i'll put together a **landing page** for you.")
	c2.toolLine = "writing index.html"
	c2.toolTarget = "index.html"
	c2.word = "sniffing…"
	c2.pup.SetState(mascot.Thinking)
	dump("chat-2-working", c2.View().Content)

	// 3. chat, permission card
	c3 := seed(newChatFor())
	c3.working = true
	c3.stream.WriteString("i'll put together a landing page for you.")
	c3.pending = &agent.PermissionEvent{Tool: "write", Target: "index.html", Summary: "write index.html"}
	c3.word = "waiting on you"
	c3.pup.SetState(mascot.Listening)
	dump("chat-3-permission", c3.View().Content)

	// demoRound builds a finished round with an expandable write block.
	diff := "+ <!doctype html>\n+ <html>\n+ <head>\n+   <title>hi</title>\n+ </head>\n+ <body>\n+   <h1>hello</h1>\n+ </body>\n+ </html>"
	demoRound := func() (*Chat, *toolBlock) {
		ch := seed(newChatFor())
		ch.appendText(ch.loyiLine("i'll put together a landing page for you."))
		ch.toolTarget = "index.html"
		blk := ch.newBlock(agent.ToolResultEvent{Name: "write",
			Display: &tool.DisplayInfo{Content: diff, Detail: "9 lines", OK: true}})
		ch.appendBlock(blk)
		ch.appendText(ch.loyiLine("done — starter landing page is in `index.html`.\nwant a signup form next?"))
		return ch, blk
	}

	// 4. chat, finished conversation (blocks collapsed, input focused)
	c4, _ := demoRound()
	dump("chat-4-conversation", c4.View().Content)

	// 6. block focused (collapsed) — accent marker + expand hint
	c6, _ := demoRound()
	c6.focusLastBlock()
	dump("chat-6-block-focused", c6.View().Content)

	// 7. block peek — first lines + more hint
	c7, b7 := demoRound()
	c7.focusLastBlock()
	b7.cycle(c7)
	dump("chat-7-block-peek", c7.View().Content)

	// 8. block full
	c8, b8 := demoRound()
	c8.focusLastBlock()
	b8.cycle(c8)
	b8.cycle(c8)
	dump("chat-8-block-full", c8.View().Content)

	// 9. run block, full: command output with exit status
	c9 := seed(newChatFor())
	c9.appendText(c9.loyiLine("tests aren't happy — one failure."))
	c9.toolTarget = "go test ./..."
	rb := c9.newBlock(agent.ToolResultEvent{Name: "run",
		Display: &tool.DisplayInfo{
			Content: "--- FAIL: TestSignup (0.00s)\n    signup_test.go:14: want 200, got 500\nFAIL\nFAIL\texample.com/site\t0.012s",
			Detail:  "exit 1", OK: false}})
	c9.appendBlock(rb)
	c9.focusLastBlock()
	rb.cycle(c9)
	dump("chat-9-run-block", c9.View().Content)

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

	// mascot states, all five faces
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
