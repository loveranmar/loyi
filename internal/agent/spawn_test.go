package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/loveranmar/loyi/internal/provider"
	"github.com/loveranmar/loyi/internal/tool"
)

// spawnProvider is concurrency-safe and content-driven: the orchestrator (which
// is offered the spawn tool) fans out once then integrates; children (no spawn
// tool) just report done.
type spawnProvider struct{}

func (p *spawnProvider) Name() string { return "spawn-test" }
func (p *spawnProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 4)
	go func() {
		defer close(ch)
		if hasSpawnTool(req.Tools) {
			if lastIsToolResult(req.Messages) {
				ch <- provider.Chunk{Text: "integrated the team's work"}
				ch <- provider.Chunk{Done: true}
				return
			}
			call := provider.ToolCall{ID: "s1", Name: "spawn", Input: json.RawMessage(
				`{"tasks":[{"agent":"build","task":"build the api"},{"agent":"build","task":"build the ui"}]}`)}
			ch <- provider.Chunk{Done: true, ToolCalls: []provider.ToolCall{call}}
			return
		}
		// a child
		ch <- provider.Chunk{Text: "done"}
		ch <- provider.Chunk{Done: true}
	}()
	return ch, nil
}

func hasSpawnTool(tools []provider.ToolDef) bool {
	for _, t := range tools {
		if t.Name == spawnToolName {
			return true
		}
	}
	return false
}

func lastIsToolResult(msgs []provider.Message) bool {
	if len(msgs) == 0 {
		return false
	}
	return msgs[len(msgs)-1].ToolCallID != ""
}

func TestSpawnFansOutConcurrently(t *testing.T) {
	dir := t.TempDir()
	ws, err := tool.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry(&tool.ReadTool{WS: ws}, &tool.WriteTool{WS: ws}, &tool.RunTool{WS: ws})
	sess := &Session{
		Provider:  &spawnProvider{},
		Tools:     reg,
		Agent:     AgentByID("construct"),
		Workspace: dir,
		Perm:      PermAsk,
	}
	orch := NewOrchestrator()
	reg.Add(NewSpawnTool(sess, orch))

	var sawPermission bool
	sess.Run(context.Background(), "build me a saas", func(e Event) {
		if pe, ok := e.(PermissionEvent); ok {
			sawPermission = true
			pe.Reply <- ReplyAllow // approve the fan-out
		}
	})

	if !sawPermission {
		t.Error("spawning a team should ask for approval once")
	}
	nodes := orch.Snapshot()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 sub-agent nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.Status != RunDone {
			t.Errorf("node %d status = %s, want done", n.ID, n.Status)
		}
		if n.Agent != "build" {
			t.Errorf("node agent = %s, want build", n.Agent)
		}
		// the child's output is retained so the pm can review it later
		if !strings.Contains(n.Report, "done") {
			t.Errorf("node %d report not stored: %q", n.ID, n.Report)
		}
	}
	// the children's usage folded into the parent (2 child turns + the spawn call)
	if sess.Usage().ToolCalls < 1 {
		t.Error("expected the spawn call counted in usage")
	}
	// the final integration text came through
	last := sess.history[len(sess.history)-1]
	if last.Role != provider.RoleAssistant || !strings.Contains(last.Content, "integrated") {
		t.Errorf("expected final integration text, got %q", last.Content)
	}
}

func TestNonOrchestratorCannotSpawn(t *testing.T) {
	build := AgentByID("build")
	if build.canUseTool("spawn") {
		t.Error("build must not be able to use the spawn tool")
	}
	construct := AgentByID("construct")
	if !construct.canUseTool("spawn") {
		t.Error("construct should be able to use the spawn tool")
	}
	if !construct.canSpawn("build") || construct.canSpawn("construct") {
		t.Error("construct should spawn build but not itself")
	}
}
