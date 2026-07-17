// Package theme holds loyi's design system.
//
// The rules: one accent color per theme, used rarely (prompt caret, active
// state, success). Everything else comes from the shared warm neutral ramp.
// Switching themes swaps only the accent — structure never changes.
package theme

import "charm.land/lipgloss/v2"

// Neutrals is the warm-toned ramp shared by every theme.
type NeutralRamp struct {
	Text       string // primary text
	Dim        string // secondary/dim text
	Border     string // borders, rules
	Background string
}

var Neutrals = NeutralRamp{
	Text:       "#EDE8E0",
	Dim:        "#A39E94",
	Border:     "#5C574F",
	Background: "#1A1815",
}

type Theme struct {
	Name   string
	Accent string
}

var (
	Mauve = Theme{Name: "mauve", Accent: "#C77DA8"}
	Ember = Theme{Name: "ember", Accent: "#C4614B"}
	Sage  = Theme{Name: "sage", Accent: "#7A9E7E"}
	Honey = Theme{Name: "honey", Accent: "#FFD24C"}
)

// Default is mauve.
var Default = Mauve

var themes = map[string]Theme{
	Mauve.Name: Mauve,
	Ember.Name: Ember,
	Sage.Name:  Sage,
	Honey.Name: Honey,
}

// Get returns the named theme, falling back to Default for unknown names.
func Get(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return Default
}

// All returns every theme in stable display order, Default first.
func All() []Theme {
	return []Theme{Mauve, Ember, Sage, Honey}
}

// Styles is the small set of lipgloss styles the whole UI is built from.
// Hierarchy comes from dim/bright contrast and whitespace; the accent is
// used rarely (caret, active state, success).
type Styles struct {
	Text   lipgloss.Style
	Dim    lipgloss.Style
	Accent lipgloss.Style
}

// Styles builds the style set for this theme.
func (t Theme) Styles() Styles {
	return Styles{
		Text:   lipgloss.NewStyle().Foreground(lipgloss.Color(Neutrals.Text)),
		Dim:    lipgloss.NewStyle().Foreground(lipgloss.Color(Neutrals.Dim)),
		Accent: lipgloss.NewStyle().Foreground(lipgloss.Color(t.Accent)),
	}
}
