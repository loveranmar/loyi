package tool

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Workspace is the directory the agent is confined to. Every tool resolves
// paths against Root and refuses to escape it — the agent can't read or write
// outside the project you launched it in.
type Workspace struct {
	Root string

	// Ignore holds extra glob patterns (context.ignore in loyi.json) that
	// tree, glob, and grep skip on top of the built-in noise dirs. MaxFiles
	// (context.maxFiles) caps how many files glob returns and grep scans;
	// 0 means no limit.
	Ignore   []string
	MaxFiles int
}

// NewWorkspace returns a workspace rooted at an absolute, cleaned dir.
func NewWorkspace(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: filepath.Clean(abs)}, nil
}

// resolve turns a possibly-relative path into an absolute one inside Root,
// rejecting anything that escapes (via .. or an absolute path elsewhere).
func (w *Workspace) resolve(p string) (string, error) {
	if p == "" {
		return w.Root, nil
	}
	joined := p
	if !filepath.IsAbs(p) {
		joined = filepath.Join(w.Root, p)
	}
	clean := filepath.Clean(joined)
	if clean != w.Root && !strings.HasPrefix(clean, w.Root+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the workspace", p)
	}
	return clean, nil
}

// rel returns a workspace-relative, forward-slashed display path.
func (w *Workspace) rel(abs string) string {
	r, err := filepath.Rel(w.Root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

// ignored reports whether a workspace-relative path matches an ignore
// pattern. Patterns with a slash match the whole path (** crosses dirs);
// bare patterns ("dist", "*.min.js") match the file or directory name.
func (w *Workspace) ignored(rel string) bool {
	for _, pat := range w.Ignore {
		if strings.Contains(pat, "/") {
			if re, err := globToRegexp(pat); err == nil && re.MatchString(rel) {
				return true
			}
		} else if ok, _ := path.Match(pat, path.Base(rel)); ok {
			return true
		}
	}
	return false
}
