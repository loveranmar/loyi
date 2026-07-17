package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlob(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"main.go", "cmd/loyi/main.go", "internal/tool/fs.go", "README.md", ".git/config"} {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("x"), 0o644)
	}
	ws, _ := NewWorkspace(dir)
	g := &GlobTool{WS: ws}

	run := func(pattern string) string {
		in, _ := json.Marshal(map[string]string{"pattern": pattern})
		out, err := g.Run(context.Background(), in)
		if err != nil {
			t.Fatalf("glob %q: %v", pattern, err)
		}
		return out
	}

	cases := map[string][]string{
		"**/*.go": {"cmd/loyi/main.go", "internal/tool/fs.go", "main.go"},
		"*.go":    {"main.go"},
		"*.md":    {"README.md"},
		"cmd/**":  {"cmd/loyi/main.go"},
	}
	for pattern, want := range cases {
		got := run(pattern)
		lines := splitNonEmpty(got)
		if len(lines) != len(want) {
			t.Errorf("glob %q = %v, want %v", pattern, lines, want)
			continue
		}
		for i := range want {
			if lines[i] != want[i] {
				t.Errorf("glob %q line %d = %q, want %q", pattern, i, lines[i], want[i])
			}
		}
	}

	// .git is skipped, so a pattern that would match inside it finds nothing
	if got := run("**/config"); got != "no files match **/config" {
		t.Errorf(".git should be skipped, got %q", got)
	}
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
