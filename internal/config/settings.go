package config

// loyi.json is the human-edited settings file — safe to commit, never holds
// secrets (API keys are env references). A project-level ./loyi.json overlays
// the global ~/.loyi/loyi.json. This is separate from config.json, which
// holds auth state (oauth tokens, onboarding) and stays private.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Settings is the loyi.json schema.
type Settings struct {
	Theme       string                 `json:"theme"`
	Model       ModelSettings          `json:"model"`
	Providers   map[string]ProviderRef `json:"providers"`
	Permissions Permissions            `json:"permissions"`
	Context     ContextSettings        `json:"context"`
	UI          UISettings             `json:"ui"`

	source sources
}

type ModelSettings struct {
	Default  string `json:"default"`
	Provider string `json:"provider"`
	Effort   string `json:"effort"` // "", low, medium, high
}

// ProviderRef configures a backend in loyi.json. APIKey is always an env
// reference ("env:OPENROUTER_API_KEY") — raw keys are rejected on load.
type ProviderRef struct {
	APIKey string `json:"apiKey"`
}

// Key resolves the env reference to the actual key, "" when the reference or
// the variable is unset.
func (p ProviderRef) Key() string {
	name, ok := strings.CutPrefix(p.APIKey, "env:")
	if !ok {
		return ""
	}
	return os.Getenv(name)
}

type Permissions struct {
	Mode  string   `json:"mode"` // ask (default), auto, readonly
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

type ContextSettings struct {
	Ignore   []string `json:"ignore"`
	MaxFiles int      `json:"maxFiles"` // 0 = no limit
}

type UISettings struct {
	Mascot *bool  `json:"mascot"`
	Banner string `json:"banner"` // first-run (default), always, never
}

// sources tracks where the settings came from and where new rules persist.
type sources struct {
	global     string
	project    string
	hasProject bool
	writeTo    string // project file when present, global otherwise
	createdNow bool   // the global file was just written with defaults
}

// Decision is the outcome of checking a tool call against the permissions.
type Decision int

const (
	Ask Decision = iota
	Allow
	Deny
)

// DefaultSettings is the skeleton written on first run: every field present so
// the file documents itself, nothing surprising turned on.
func DefaultSettings() *Settings {
	mascot := true
	return &Settings{
		Providers:   map[string]ProviderRef{},
		Permissions: Permissions{Mode: "ask", Allow: []string{}, Deny: []string{}},
		Context:     ContextSettings{Ignore: []string{}},
		UI:          UISettings{Mascot: &mascot, Banner: "first-run"},
	}
}

// ProjectSettingsFile is the project-level file name, looked up in the cwd.
const ProjectSettingsFile = "loyi.json"

// GlobalSettingsPath returns ~/.loyi/loyi.json.
func GlobalSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".loyi", ProjectSettingsFile), nil
}

// LoadSettings resolves loyi.json for a project dir: defaults, overlaid by
// the global file (created with defaults when missing), overlaid by the
// project file when present.
func LoadSettings(projectDir string) (*Settings, error) {
	gp, err := GlobalSettingsPath()
	if err != nil {
		return nil, err
	}
	return LoadSettingsFiles(gp, filepath.Join(projectDir, ProjectSettingsFile))
}

// LoadSettingsFiles is LoadSettings with explicit file locations.
func LoadSettingsFiles(globalPath, projectPath string) (*Settings, error) {
	s := DefaultSettings()
	s.source = sources{global: globalPath, project: projectPath, writeTo: globalPath}

	g, err := readSettingsFile(globalPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if writeSettingsFile(globalPath, s) == nil {
			s.source.createdNow = true
		}
	case err != nil:
		return nil, err
	default:
		s.overlay(g)
	}

	p, err := readSettingsFile(projectPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		s.overlay(p)
		s.source.hasProject = true
		s.source.writeTo = projectPath
	}
	return s, nil
}

// overlay applies o on top of s: set scalars win, rule and ignore lists merge.
func (s *Settings) overlay(o *Settings) {
	if o.Theme != "" {
		s.Theme = o.Theme
	}
	if o.Model.Default != "" {
		s.Model.Default = o.Model.Default
	}
	if o.Model.Provider != "" {
		s.Model.Provider = o.Model.Provider
	}
	if o.Model.Effort != "" {
		s.Model.Effort = o.Model.Effort
	}
	for id, p := range o.Providers {
		if s.Providers == nil {
			s.Providers = map[string]ProviderRef{}
		}
		s.Providers[id] = p
	}
	if o.Permissions.Mode != "" {
		s.Permissions.Mode = o.Permissions.Mode
	}
	s.Permissions.Allow = mergeRules(s.Permissions.Allow, o.Permissions.Allow)
	s.Permissions.Deny = mergeRules(s.Permissions.Deny, o.Permissions.Deny)
	s.Context.Ignore = mergeRules(s.Context.Ignore, o.Context.Ignore)
	if o.Context.MaxFiles > 0 {
		s.Context.MaxFiles = o.Context.MaxFiles
	}
	if o.UI.Mascot != nil {
		s.UI.Mascot = o.UI.Mascot
	}
	if o.UI.Banner != "" {
		s.UI.Banner = o.UI.Banner
	}
}

func mergeRules(base, extra []string) []string {
	for _, r := range extra {
		found := false
		for _, b := range base {
			if b == r {
				found = true
				break
			}
		}
		if !found {
			base = append(base, r)
		}
	}
	return base
}

func readSettingsFile(p string) (*Settings, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: not valid json: %v", p, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &s, nil
}

func writeSettingsFile(p string, s *Settings) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o644)
}

// validate checks the file's values and returns a clear, single error.
func (s *Settings) validate() error {
	switch s.Permissions.Mode {
	case "", "ask", "auto", "readonly":
	default:
		return fmt.Errorf("permissions.mode must be ask, auto, or readonly — got %q", s.Permissions.Mode)
	}
	switch s.Model.Effort {
	case "", "low", "medium", "high":
	default:
		return fmt.Errorf("model.effort must be low, medium, or high — got %q", s.Model.Effort)
	}
	switch s.UI.Banner {
	case "", "first-run", "always", "never":
	default:
		return fmt.Errorf("ui.banner must be first-run, always, or never — got %q", s.UI.Banner)
	}
	if s.Context.MaxFiles < 0 {
		return fmt.Errorf("context.maxFiles can't be negative — got %d", s.Context.MaxFiles)
	}
	for id, p := range s.Providers {
		if p.APIKey != "" && !strings.HasPrefix(p.APIKey, "env:") {
			return fmt.Errorf("providers.%s.apiKey must be an env reference like \"env:%s_API_KEY\" — never put raw keys in loyi.json", id, strings.ToUpper(id))
		}
	}
	for _, r := range s.Permissions.Allow {
		if err := checkRule(r); err != nil {
			return fmt.Errorf("permissions.allow: %w", err)
		}
	}
	for _, r := range s.Permissions.Deny {
		if err := checkRule(r); err != nil {
			return fmt.Errorf("permissions.deny: %w", err)
		}
	}
	return nil
}

func checkRule(r string) error {
	tool, pat, ok := strings.Cut(r, ":")
	if !ok || tool == "" || pat == "" {
		return fmt.Errorf("rule %q must look like tool:pattern, e.g. \"write:*.html\"", r)
	}
	return nil
}

// Decide checks a mutating tool call against the permission config: deny
// rules win, then readonly mode, then allow rules, then auto mode. Anything
// unmatched falls through to Ask.
func (s *Settings) Decide(toolName, target string) Decision {
	for _, r := range s.Permissions.Deny {
		if ruleMatches(r, toolName, target) {
			return Deny
		}
	}
	if s.Permissions.Mode == "readonly" {
		return Deny
	}
	for _, r := range s.Permissions.Allow {
		if ruleMatches(r, toolName, target) {
			return Allow
		}
	}
	if s.Permissions.Mode == "auto" {
		return Allow
	}
	return Ask
}

// ruleMatches applies one "tool:pattern" rule to a target (a path for file
// tools, the command line for run). `*` matches anything — so "write:*.html"
// covers html files in any directory and "run:go *" covers any go command —
// and `?` matches one character.
func ruleMatches(rule, toolName, target string) bool {
	rt, pat, ok := strings.Cut(rule, ":")
	if !ok || (rt != toolName && rt != "*") {
		return false
	}
	return patternRegexp(pat).MatchString(target)
}

func patternRegexp(pat string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pat {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// RuleFor is the allow-rule recorded by "always allow": file tools scope to
// the file type, run scopes to the command's first word.
func RuleFor(toolName, target string) string {
	switch toolName {
	case "write", "edit", "read":
		if ext := path.Ext(target); ext != "" {
			return toolName + ":*" + ext
		}
	case "run":
		if first, _, ok := strings.Cut(target, " "); ok {
			return "run:" + first + " *"
		}
	}
	return toolName + ":" + target
}

// RememberAllow adds an allow rule and persists it — to the project loyi.json
// when one exists, the global file otherwise. With no backing file (tests),
// the rule still applies for the session.
func (s *Settings) RememberAllow(rule string) error {
	s.Permissions.Allow = mergeRules(s.Permissions.Allow, []string{rule})
	if s.source.writeTo == "" {
		return nil
	}
	f, err := readSettingsFile(s.source.writeTo)
	if errors.Is(err, os.ErrNotExist) {
		f = DefaultSettings()
	} else if err != nil {
		return err
	}
	f.Permissions.Allow = mergeRules(f.Permissions.Allow, []string{rule})
	return writeSettingsFile(s.source.writeTo, f)
}

// MascotEnabled reports whether the status-line mascot should render.
func (s *Settings) MascotEnabled() bool {
	return s.UI.Mascot == nil || *s.UI.Mascot
}

// BannerMode returns the greeting behavior: first-run, always, or never.
func (s *Settings) BannerMode() string {
	if s.UI.Banner == "" {
		return "first-run"
	}
	return s.UI.Banner
}

// CreatedNow reports whether the global file was just created — the "first
// run" the banner setting refers to.
func (s *Settings) CreatedNow() bool { return s.source.createdNow }

// RuleFile is where always-allow rules persist right now.
func (s *Settings) RuleFile() string { return s.source.writeTo }

// Sources returns the global and project file paths and whether the project
// file exists.
func (s *Settings) Sources() (global, project string, hasProject bool) {
	return s.source.global, s.source.project, s.source.hasProject
}
