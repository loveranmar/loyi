package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunTool executes a shell command in the workspace. Always mutating — every
// command goes through the permission gate.
type RunTool struct {
	WS   *Workspace
	last *DisplayInfo
}

func (t *RunTool) LastDisplay() *DisplayInfo { return t.last }
func (t *RunTool) Name() string              { return "run" }
func (t *RunTool) Description() string {
	return "Run a shell command in the workspace and return its combined stdout and stderr. Use for builds, tests, git, installing deps, scaffolding."
}
func (t *RunTool) Schema() map[string]any {
	return obj(props{
		"command": str("The shell command to run, e.g. `go test ./...` or `npm install`."),
	}, "command")
}
func (t *RunTool) Mutating(json.RawMessage) bool { return true }
func (t *RunTool) Summary(in json.RawMessage) string {
	return "run " + strconvQuote(stringField(in, "command"))
}

// AutoSafe reports whether a command is safe to run without asking in "auto"
// mode: a known read-only/build command, and nothing that looks destructive.
// Unknown commands return false — when unsure, ask.
func (t *RunTool) AutoSafe(in json.RawMessage) bool {
	cmd := strings.TrimSpace(stringField(in, "command"))
	if cmd == "" {
		return false
	}
	low := strings.ToLower(cmd)
	for _, bad := range dangerousPatterns {
		if strings.Contains(low, bad) {
			return false
		}
	}
	// The first word must be a recognized safe command. Chained commands
	// (&&, ;, |) fall through to false so they get a look.
	if strings.ContainsAny(cmd, "&;|`$(") {
		return false
	}
	first := strings.Fields(cmd)[0]
	if i := strings.LastIndex(first, "/"); i >= 0 {
		first = first[i+1:] // strip any path
	}
	return safeCommands[first]
}

// safeCommands are read-only or build/test commands fine to run unprompted.
var safeCommands = map[string]bool{
	"ls": true, "cat": true, "pwd": true, "echo": true, "head": true, "tail": true,
	"wc": true, "which": true, "grep": true, "rg": true, "find": true, "tree": true,
	"go": true, "gofmt": true, "node": true, "npm": true, "pnpm": true, "yarn": true,
	"python": true, "python3": true, "pytest": true, "cargo": true, "make": true,
	"git": true, "date": true, "env": true, "stat": true, "file": true, "diff": true,
}

// dangerousPatterns force a prompt even in auto mode.
var dangerousPatterns = []string{
	"rm -r", "rm -f", "rmdir", "sudo", "mkfs", "dd ", ":(){", "shutdown", "reboot",
	"chmod -r", "chown -r", "git push", "git reset --hard", "git clean", "> /dev/",
	"npm publish", "curl", "wget", "force", "> /etc", "truncate",
}

func (t *RunTool) Run(ctx context.Context, in json.RawMessage) (string, error) {
	t.last = nil
	var a struct {
		Command string `json:"command"`
	}
	if err := parseInput(in, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("command is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", a.Command)
	cmd.Dir = t.WS.Root
	out, err := cmd.CombinedOutput()

	text := strings.TrimRight(string(out), "\n")
	display := &DisplayInfo{Content: text, Detail: "exit 0", OK: true}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			display.Detail = fmt.Sprintf("exit %d", ee.ExitCode())
		} else {
			display.Detail = "failed"
		}
		display.OK = false
	}
	if ctx.Err() == context.DeadlineExceeded {
		display.Detail = "timed out"
		display.OK = false
		t.last = display
		return text + "\n\n(command timed out after 2m)", nil
	}
	t.last = display
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return fmt.Sprintf("%s\n\n(exited with error: %v)", text, err), nil
	}
	if text == "" {
		return "(no output)", nil
	}
	return text, nil
}
