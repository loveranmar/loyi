package agent

import (
	"context"
	"strings"
	"testing"
)

func TestTeamToolSummarizesRuns(t *testing.T) {
	orch := NewOrchestrator()
	n := orch.start(rootNodeID, "build", "build the API")
	orch.update(n, func(n *RunNode) {
		n.Status = RunDone
		n.Report = "wrote handlers.go and db.go"
	})

	out, err := (&TeamTool{orch: orch}).Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"build", "build the API", "done", "wrote handlers.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("team report missing %q:\n%s", want, out)
		}
	}
}

func TestTeamToolEmpty(t *testing.T) {
	out, _ := (&TeamTool{orch: NewOrchestrator()}).Run(context.Background(), nil)
	if !strings.Contains(out, "No sub-agents") {
		t.Errorf("empty team report = %q", out)
	}
}

func TestPMHasTeamReport(t *testing.T) {
	pm := AgentByID("pm")
	if !pm.canUseTool(teamToolName) {
		t.Error("pm should be able to review the team")
	}
	if pm.canUseTool("write") || pm.canUseTool("run") {
		t.Error("pm is still read-only for the workspace")
	}
	// plan has an explicit toolset that doesn't include team_report
	if AgentByID("plan").canUseTool(teamToolName) {
		t.Error("plan should not have team_report")
	}
}
