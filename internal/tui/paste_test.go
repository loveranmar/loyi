package tui

import (
	"strings"
	"testing"
)

func TestPasteStashAndExpand(t *testing.T) {
	c := testChat()
	long := strings.Repeat("line of text\n", 50) // well over the threshold

	ph := c.stashPaste(long)
	if !strings.HasPrefix(ph, "[Pasted text #1") || !strings.Contains(ph, "51 lines") {
		t.Errorf("placeholder should note the count, got %q", ph)
	}
	if len(c.pastes) != 1 {
		t.Fatalf("paste not stashed")
	}

	// a message mixing prose and the placeholder expands only the placeholder
	msg := "here is the log: " + ph + " — what's wrong?"
	got := c.expandPastes(msg)
	if !strings.Contains(got, long) || strings.Contains(got, "Pasted text #1") {
		t.Errorf("expandPastes should inline the full paste, got %q", got[:60])
	}

	// an unknown reference is left untouched
	if out := c.expandPastes("[Pasted text #9 · 1 lines]"); !strings.Contains(out, "#9") {
		t.Errorf("unknown paste ref should be left as-is, got %q", out)
	}
}
