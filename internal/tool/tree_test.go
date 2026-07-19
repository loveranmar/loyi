package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeSkipsUnreadableDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("x"), 0o644)
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(locked, "secret"), []byte("x"), 0o644)
	// Make the subdirectory unreadable, like a root-owned dir would be.
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) }) // so TempDir cleanup can recurse
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("running as root — can't simulate an unreadable dir")
	}

	ws, _ := NewWorkspace(dir)
	tr := &TreeTool{WS: ws}
	in, _ := json.Marshal(map[string]string{})
	out, err := tr.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("tree aborted on an unreadable subdir: %v", err)
	}
	if !strings.Contains(out, "locked/") || !strings.Contains(out, "readme.md") {
		t.Errorf("tree should still list the readable entries, got:\n%s", out)
	}
	if !strings.Contains(out, "permission denied") {
		t.Errorf("tree should note the unreadable dir, got:\n%s", out)
	}
}
