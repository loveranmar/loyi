package tool

// Display payloads: richer UI-facing results than the text fed back to the
// model. File tools produce a line diff, run produces its captured output and
// exit status. The TUI renders these in expandable blocks.

import (
	"fmt"
	"strings"
)

// DisplayInfo is what a tool call looked like from the user's side.
type DisplayInfo struct {
	Content string // expandable content: a diff or captured output; "" = nothing to show
	Detail  string // short result note: "3 lines", "exit 0"
	OK      bool   // false renders as a failure even without a hard tool error
}

// Displayer is an optional tool interface. LastDisplay returns the payload
// for the most recent Run; the agent reads it right after each call.
type Displayer interface {
	LastDisplay() *DisplayInfo
}

// countNoun formats a count with a plain s-plural: "1 line", "3 lines".
func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// splitLines breaks content into lines without a trailing empty line.
func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// diffLines renders a line diff of old→new content: "-" removed, "+" added,
// two-space prefix for unchanged context. Uses an LCS walk; very large inputs
// fall back to a plain remove-all/add-all diff.
func diffLines(old, new string) string {
	a, b := splitLines(old), splitLines(new)
	var out []string
	if len(a)*len(b) > 250_000 {
		for _, l := range a {
			out = append(out, "- "+l)
		}
		for _, l := range b {
			out = append(out, "+ "+l)
		}
		return strings.Join(out, "\n")
	}

	// lcs[i][j] = length of the longest common subsequence of a[i:], b[j:]
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, "  "+a[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, "- "+a[i])
	}
	for ; j < len(b); j++ {
		out = append(out, "+ "+b[j])
	}
	return strings.Join(out, "\n")
}

// diffStat counts the added and removed lines of a diffLines result.
func diffStat(diff string) (added, removed int) {
	for _, l := range splitLines(diff) {
		switch {
		case strings.HasPrefix(l, "+ "):
			added++
		case strings.HasPrefix(l, "- "):
			removed++
		}
	}
	return added, removed
}
