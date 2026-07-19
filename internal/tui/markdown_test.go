package tui

import (
	"strings"
	"testing"

	"github.com/loveranmar/loyi/internal/theme"
)

// plain strips SGR escapes (stripANSI lives in chat_test.go) so tests can
// assert on rendered content.
func plain(s string) string { return stripANSI(s) }

func TestRenderMarkdownStripsMarkers(t *testing.T) {
	st := theme.Default.Styles()
	cases := map[string]string{
		"plain text":             "plain text",
		"**bold** word":          "bold word",
		"some *italic* here":     "some italic here",
		"call `foo()` now":       "call  foo()  now",
		"# Heading":              "Heading",
		"- first\n- second":      "• first\n• second",
		"1. one\n2. two":         "1. one\n2. two",
		"__also bold__ and _em_": "also bold and em",
	}
	for in, want := range cases {
		if got := plain(renderMarkdown(in, st)); got != want {
			t.Errorf("renderMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderMarkdownFence(t *testing.T) {
	st := theme.Default.Styles()
	out := plain(renderMarkdown("before\n```\ncode line\n```\nafter", st))
	if !strings.Contains(out, "code line") || strings.Contains(out, "```") {
		t.Errorf("fenced code not handled: %q", out)
	}
}

func TestRenderMarkdownActuallyStyles(t *testing.T) {
	st := theme.Default.Styles()
	// bold should emit an SGR sequence (styled output differs from plain).
	out := renderMarkdown("**x**", st)
	if out == "x" || !strings.Contains(out, "\x1b[") {
		t.Errorf("expected styled output for bold, got %q", out)
	}
}
