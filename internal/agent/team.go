package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const teamToolName = "team_report"

// TeamTool gives an agent (the pm) a read-only window into every sub-agent the
// orchestrator has run: their tasks, status, how long they took, and what they
// reported back. It's how the pm answers "where are we?" from the team's actual
// work rather than guessing.
type TeamTool struct {
	orch *Orchestrator
}

// NewTeamTool wires a team-report tool to an orchestrator.
func NewTeamTool(orch *Orchestrator) *TeamTool { return &TeamTool{orch: orch} }

func (t *TeamTool) Name() string { return teamToolName }

func (t *TeamTool) Description() string {
	return "Review the work of the sub-agent team: every sub-agent that has run, its task, status, how long it took, and the report it returned. Use this to ground an assessment of where the project stands and what's next."
}

func (t *TeamTool) Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}

func (t *TeamTool) Mutating(json.RawMessage) bool  { return false }
func (t *TeamTool) Summary(json.RawMessage) string { return "review the team's work" }

func (t *TeamTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	nodes := t.orch.Snapshot()
	if len(nodes) == 0 {
		return "No sub-agents have run yet. The team is empty — nothing has been delegated.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The team has run %d sub-agent(s):\n\n", len(nodes))
	for i, n := range nodes {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, n.Agent, n.Task)
		fmt.Fprintf(&b, "   status: %s · %s\n", n.Status, elapsedShort(n.Elapsed()))
		if n.Err != "" {
			fmt.Fprintf(&b, "   error: %s\n", n.Err)
		}
		report := strings.TrimSpace(n.Report)
		if report == "" {
			report = "(nothing reported yet)"
		}
		if len(report) > 800 {
			report = report[:800] + "…"
		}
		fmt.Fprintf(&b, "   report: %s\n\n", indentReport(report))
	}
	return strings.TrimSpace(b.String()), nil
}

// indentReport keeps a multi-line report readable under its bullet.
func indentReport(s string) string {
	return strings.ReplaceAll(s, "\n", "\n           ")
}

func elapsedShort(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}
