package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// GlobTool finds files by pattern. Supports * (within a segment), ** (any
// depth), and ? — matched against workspace-relative paths.
type GlobTool struct{ WS *Workspace }

func (t *GlobTool) Name() string { return "glob" }
func (t *GlobTool) Description() string {
	return "Find files by glob pattern (e.g. `**/*.go`, `cmd/**`, `*.md`). Returns matching paths, one per line."
}
func (t *GlobTool) Schema() map[string]any {
	return obj(props{
		"pattern": str("Glob pattern. `*` matches within a path segment, `**` matches any depth, `?` matches one character."),
	}, "pattern")
}
func (t *GlobTool) Mutating(json.RawMessage) bool { return false }
func (t *GlobTool) Summary(in json.RawMessage) string {
	return "glob " + stringField(in, "pattern")
}
func (t *GlobTool) Run(_ context.Context, in json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	if a.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	re, err := globToRegexp(a.Pattern)
	if err != nil {
		return "", err
	}
	var matches []string
	err = filepath.WalkDir(t.WS.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != t.WS.Root {
				return filepath.SkipDir
			}
			return nil
		}
		rel := t.WS.rel(path)
		if re.MatchString(rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "no files match " + a.Pattern, nil
	}
	sort.Strings(matches)
	if len(matches) > 300 {
		matches = matches[:300]
	}
	return strings.Join(matches, "\n"), nil
}

// globToRegexp converts a glob into an anchored regexp over slash-separated
// paths. ** matches across separators; * and ? do not.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				i++
				// consume an optional trailing slash so "**/x" also matches "x"
				if i+1 < len(runes) && runes[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteRune('\\')
			b.WriteRune(runes[i])
		default:
			b.WriteRune(runes[i])
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
