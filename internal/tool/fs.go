package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---- read ----

type ReadTool struct{ WS *Workspace }

func (t *ReadTool) Name() string { return "read" }
func (t *ReadTool) Description() string {
	return "Read a file from the workspace. Returns the contents with line numbers. Use before editing."
}
func (t *ReadTool) Schema() map[string]any {
	return obj(props{
		"path": str("Path to the file, relative to the workspace root."),
	}, "path")
}
func (t *ReadTool) Mutating(json.RawMessage) bool { return false }
func (t *ReadTool) Summary(in json.RawMessage) string {
	return "read " + stringField(in, "path")
}
func (t *ReadTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	abs, err := t.WS.resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	var b strings.Builder
	for i, ln := range lines {
		if i == len(lines)-1 && ln == "" {
			break
		}
		fmt.Fprintf(&b, "%5d  %s\n", i+1, ln)
	}
	if b.Len() == 0 {
		return "(empty file)", nil
	}
	return b.String(), nil
}

// ---- write ----

type WriteTool struct {
	WS   *Workspace
	last *DisplayInfo
}

func (t *WriteTool) LastDisplay() *DisplayInfo { return t.last }
func (t *WriteTool) Name() string              { return "write" }
func (t *WriteTool) Description() string {
	return "Write a file to the workspace, creating parent directories and overwriting any existing file. Use for new files."
}
func (t *WriteTool) Schema() map[string]any {
	return obj(props{
		"path":    str("Path to write, relative to the workspace root."),
		"content": str("Full file contents."),
	}, "path", "content")
}
func (t *WriteTool) Mutating(json.RawMessage) bool { return true }
func (t *WriteTool) Summary(in json.RawMessage) string {
	return "write " + stringField(in, "path")
}
func (t *WriteTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	t.last = nil
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	abs, err := t.WS.resolve(a.Path)
	if err != nil {
		return "", err
	}
	old, _ := os.ReadFile(abs) // missing file diffs as all-new
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	n := strings.Count(a.Content, "\n")
	if len(a.Content) > 0 && !strings.HasSuffix(a.Content, "\n") {
		n++
	}
	t.last = &DisplayInfo{
		Content: diffLines(string(old), a.Content),
		Detail:  countNoun(n, "line"),
		OK:      true,
	}
	return fmt.Sprintf("wrote %s · %s", t.WS.rel(abs), countNoun(n, "line")), nil
}

// ---- edit ----

type EditTool struct {
	WS   *Workspace
	last *DisplayInfo
}

func (t *EditTool) LastDisplay() *DisplayInfo { return t.last }
func (t *EditTool) Name() string              { return "edit" }
func (t *EditTool) Description() string {
	return "Replace an exact string in a file. old must appear exactly once. Read the file first so old matches precisely."
}
func (t *EditTool) Schema() map[string]any {
	return obj(props{
		"path": str("Path to the file, relative to the workspace root."),
		"old":  str("Exact text to replace. Must be unique in the file."),
		"new":  str("Replacement text."),
	}, "path", "old", "new")
}
func (t *EditTool) Mutating(json.RawMessage) bool { return true }
func (t *EditTool) Summary(in json.RawMessage) string {
	return "edit " + stringField(in, "path")
}
func (t *EditTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	t.last = nil
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	abs, err := t.WS.resolve(a.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	s := string(data)
	switch strings.Count(s, a.Old) {
	case 0:
		return "", fmt.Errorf("old text not found in %s", t.WS.rel(abs))
	case 1:
	default:
		return "", fmt.Errorf("old text appears more than once in %s — add surrounding context to make it unique", t.WS.rel(abs))
	}
	if err := os.WriteFile(abs, []byte(strings.Replace(s, a.Old, a.New, 1)), 0o644); err != nil {
		return "", err
	}
	diff := diffLines(a.Old, a.New)
	added, removed := diffStat(diff)
	changed := added
	if removed > changed {
		changed = removed
	}
	t.last = &DisplayInfo{
		Content: diff,
		Detail:  countNoun(changed, "line") + " changed",
		OK:      true,
	}
	return fmt.Sprintf("edited %s", t.WS.rel(abs)), nil
}

// ---- tree ----

type TreeTool struct{ WS *Workspace }

func (t *TreeTool) Name() string { return "tree" }
func (t *TreeTool) Description() string {
	return "Show the workspace file tree. Skips noise like .git and node_modules. Optional path to scope it."
}
func (t *TreeTool) Schema() map[string]any {
	return obj(props{
		"path": str("Subdirectory to show, relative to the workspace root. Omit for the whole workspace."),
	})
}
func (t *TreeTool) Mutating(json.RawMessage) bool { return false }
func (t *TreeTool) Summary(in json.RawMessage) string {
	p := stringField(in, "path")
	if p == "" {
		p = "."
	}
	return "tree " + p
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, ".next": true, "target": true}

func (t *TreeTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	root, err := t.WS.resolve(a.Path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return entries[i].Name() < entries[j].Name()
		})
		var shown []os.DirEntry
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") && e.Name() != ".env.example" || skipDirs[e.Name()] {
				if e.IsDir() && skipDirs[e.Name()] {
					continue
				}
			}
			if skipDirs[e.Name()] {
				continue
			}
			if t.WS.ignored(t.WS.rel(filepath.Join(dir, e.Name()))) {
				continue
			}
			shown = append(shown, e)
		}
		for i, e := range shown {
			branch, next := "├── ", prefix+"│   "
			if i == len(shown)-1 {
				branch, next = "└── ", prefix+"    "
			}
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			fmt.Fprintf(&b, "%s%s%s\n", prefix, branch, name)
			if e.IsDir() {
				if err := walk(filepath.Join(dir, e.Name()), next); err != nil {
					return err
				}
			}
		}
		return nil
	}
	fmt.Fprintf(&b, "%s/\n", t.WS.rel(root))
	if err := walk(root, ""); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ---- ls ----

type LsTool struct{ WS *Workspace }

func (t *LsTool) Name() string { return "ls" }
func (t *LsTool) Description() string {
	return "List the entries of a single directory in the workspace."
}
func (t *LsTool) Schema() map[string]any {
	return obj(props{
		"path": str("Directory to list, relative to the workspace root. Omit for the root."),
	})
}
func (t *LsTool) Mutating(json.RawMessage) bool { return false }
func (t *LsTool) Summary(in json.RawMessage) string {
	p := stringField(in, "path")
	if p == "" {
		p = "."
	}
	return "ls " + p
}
func (t *LsTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	dir, err := t.WS.resolve(a.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(empty directory)", nil
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name + "\n")
	}
	return b.String(), nil
}

// ---- grep ----

type GrepTool struct{ WS *Workspace }

func (t *GrepTool) Name() string { return "grep" }
func (t *GrepTool) Description() string {
	return "Search workspace file contents for a substring. Returns matching path:line: text. Case-sensitive."
}
func (t *GrepTool) Schema() map[string]any {
	return obj(props{
		"query": str("Substring to search for."),
		"path":  str("Subdirectory to search, relative to the workspace root. Omit for the whole workspace."),
	}, "query")
}
func (t *GrepTool) Mutating(json.RawMessage) bool { return false }
func (t *GrepTool) Summary(in json.RawMessage) string {
	return "grep " + strconvQuote(stringField(in, "query"))
}
func (t *GrepTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
		Path  string `json:"path"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	root, err := t.WS.resolve(a.Path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	matches, files := 0, 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || matches >= 200 {
			return nil
		}
		rel := t.WS.rel(path)
		if d.IsDir() {
			if skipDirs[d.Name()] || (path != root && t.WS.ignored(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if t.WS.ignored(rel) {
			return nil
		}
		files++
		if t.WS.MaxFiles > 0 && files > t.WS.MaxFiles {
			return filepath.SkipAll
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return nil
		}
		for i, ln := range strings.Split(string(data), "\n") {
			if strings.Contains(ln, a.Query) {
				fmt.Fprintf(&b, "%s:%d: %s\n", t.WS.rel(path), i+1, strings.TrimSpace(ln))
				matches++
				if matches >= 200 {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if matches == 0 {
		return "no matches", nil
	}
	return b.String(), nil
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func strconvQuote(s string) string { return `"` + s + `"` }

// ---- schema helpers ----

type props map[string]map[string]any

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func obj(p props, required ...string) map[string]any {
	properties := map[string]any{}
	for k, v := range p {
		properties[k] = v
	}
	m := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
