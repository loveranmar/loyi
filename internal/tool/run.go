package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunTool executes a shell command in the workspace. Always mutating — every
// command goes through the permission gate.
type RunTool struct{ WS *Workspace }

func (t *RunTool) Name() string { return "run" }
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
func (t *RunTool) Run(ctx context.Context, in json.RawMessage) (string, error) {
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
	if ctx.Err() == context.DeadlineExceeded {
		return text + "\n\n(command timed out after 2m)", nil
	}
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
