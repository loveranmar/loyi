package tool

import (
	"strings"
	"testing"
)

func TestDiffLines(t *testing.T) {
	old := "a\nb\nc\n"
	new := "a\nB\nc\nd\n"
	got := diffLines(old, new)
	want := []string{"  a", "- b", "+ B", "  c", "+ d"}
	if got != strings.Join(want, "\n") {
		t.Errorf("diff =\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	added, removed := diffStat(got)
	if added != 2 || removed != 1 {
		t.Errorf("stat = +%d -%d, want +2 -1", added, removed)
	}
}

func TestDiffNewFile(t *testing.T) {
	got := diffLines("", "one\ntwo\n")
	if got != "+ one\n+ two" {
		t.Errorf("new-file diff = %q", got)
	}
}

func TestCountNoun(t *testing.T) {
	if countNoun(1, "line") != "1 line" || countNoun(3, "line") != "3 lines" {
		t.Error("plural broken")
	}
}
