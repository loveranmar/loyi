package tui

import (
	"regexp"
	"strings"

	"github.com/loveranmar/loyi/internal/theme"
)

// A deliberately small markdown renderer: enough of CommonMark that model
// output reads cleanly (bold, italic, inline code, fenced code, headings,
// bullets) without pulling in a full renderer. It is segment-oriented — each
// run of text is rendered exactly once with a single style, never nested, so
// an inner reset can't drop the surrounding foreground color.

var (
	reMDHeading  = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*)$`)
	reMDBullet   = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	reMDNumbered = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.*)$`)

	// One combined matcher for inline spans. Double markers precede single
	// ones so ** / __ win over * / _; code is first so *stars* inside `code`
	// aren't treated as emphasis.
	reMDInline = regexp.MustCompile("`[^`]+`|\\*\\*[^*]+\\*\\*|__[^_]+__|\\*[^*\\n]+\\*|_[^_\\n]+_")
	reMDMarker = regexp.MustCompile("[*_`]")
)

// renderMarkdown converts a subset of markdown into styled terminal text.
func renderMarkdown(s string, st theme.Styles) string {
	lines := strings.Split(s, "\n")
	inFence := false
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			continue // drop the fence markers themselves
		}
		if inFence {
			out = append(out, "  "+st.Code.Render(" "+ln+" "))
			continue
		}
		out = append(out, renderMDLine(ln, st))
	}
	return strings.Join(out, "\n")
}

func renderMDLine(ln string, st theme.Styles) string {
	if m := reMDHeading.FindStringSubmatch(ln); m != nil {
		return st.Accent.Bold(true).Render(reMDMarker.ReplaceAllString(m[2], ""))
	}
	if m := reMDBullet.FindStringSubmatch(ln); m != nil {
		return m[1] + st.Accent.Render("• ") + renderMDInline(m[2], st)
	}
	if m := reMDNumbered.FindStringSubmatch(ln); m != nil {
		return m[1] + st.Accent.Render(m[2]+". ") + renderMDInline(m[3], st)
	}
	return renderMDInline(ln, st)
}

// renderMDInline styles bold, italic, and inline code within one line, leaving
// everything else in the base text color. Segments never nest.
func renderMDInline(s string, st theme.Styles) string {
	var b strings.Builder
	last := 0
	for _, loc := range reMDInline.FindAllStringIndex(s, -1) {
		if loc[0] > last {
			b.WriteString(st.Text.Render(s[last:loc[0]]))
		}
		b.WriteString(styleMDToken(s[loc[0]:loc[1]], st))
		last = loc[1]
	}
	if last < len(s) {
		b.WriteString(st.Text.Render(s[last:]))
	}
	return b.String()
}

func styleMDToken(tok string, st theme.Styles) string {
	switch {
	case strings.HasPrefix(tok, "`"):
		return st.Code.Render(" " + strings.Trim(tok, "`") + " ")
	case strings.HasPrefix(tok, "**") || strings.HasPrefix(tok, "__"):
		return st.Bold.Render(tok[2 : len(tok)-2])
	case strings.HasPrefix(tok, "*") || strings.HasPrefix(tok, "_"):
		return st.Italic.Render(tok[1 : len(tok)-1])
	}
	return st.Text.Render(tok)
}
