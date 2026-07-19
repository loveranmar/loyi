package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const spawnToolName = "spawn"

// maxSpawn caps how many sub-agents one spawn call may launch, as a runaway
// backstop.
const maxSpawn = 8

// SpawnTool lets an orchestrator agent launch specialized sub-agents that run
// concurrently, each on its own part of the work, and returns their results.
// It lives in the agent package because it runs full sub-sessions.
type SpawnTool struct {
	parent *Session
	orch   *Orchestrator
}

// NewSpawnTool wires a spawn tool to a parent session and orchestrator.
func NewSpawnTool(parent *Session, orch *Orchestrator) *SpawnTool {
	return &SpawnTool{parent: parent, orch: orch}
}

func (t *SpawnTool) Name() string { return spawnToolName }

func (t *SpawnTool) Description() string {
	ids := t.parent.Agent.Spawns
	return "Launch specialized sub-agents that run concurrently, each on one part of the work, and get back their results. " +
		"Decompose the task, give each sub-agent a clear, self-contained job on different files, then integrate what they return. " +
		"Available sub-agents: " + strings.Join(ids, ", ") + "."
}

func (t *SpawnTool) Schema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"tasks"},
		"properties": map[string]any{
			"tasks": map[string]any{
				"type":        "array",
				"description": "The sub-agents to launch, run in parallel.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"agent", "task"},
					"properties": map[string]any{
						"agent": map[string]any{
							"type":        "string",
							"description": "Which sub-agent to use: " + strings.Join(t.parent.Agent.Spawns, ", ") + ".",
						},
						"task": map[string]any{
							"type":        "string",
							"description": "A clear, self-contained instruction for this sub-agent.",
						},
					},
				},
			},
		},
	}
}

func (t *SpawnTool) Mutating(json.RawMessage) bool { return true }

// AutoSafe is always false: spinning up a team is significant enough to
// confirm once, even in auto mode (bypass still skips it).
func (t *SpawnTool) AutoSafe(json.RawMessage) bool { return false }

type spawnTask struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

func (t *SpawnTool) parse(in json.RawMessage) ([]spawnTask, error) {
	var a struct {
		Tasks []spawnTask `json:"tasks"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return nil, fmt.Errorf("bad spawn input: %w", err)
	}
	return a.Tasks, nil
}

func (t *SpawnTool) Summary(in json.RawMessage) string {
	tasks, err := t.parse(in)
	if err != nil || len(tasks) == 0 {
		return "spawn sub-agents"
	}
	names := make([]string, 0, len(tasks))
	for _, tk := range tasks {
		names = append(names, tk.Agent)
	}
	return fmt.Sprintf("spawn %d agents (%s)", len(tasks), strings.Join(names, ", "))
}

func (t *SpawnTool) Run(ctx context.Context, in json.RawMessage) (string, error) {
	tasks, err := t.parse(in)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "", fmt.Errorf("no tasks given to spawn")
	}
	if len(tasks) > maxSpawn {
		tasks = tasks[:maxSpawn]
	}

	type result struct {
		agent, task, out string
		failed           bool
	}
	results := make([]result, len(tasks))
	var totals Usage
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i, tk := range tasks {
		a := AgentByID(tk.Agent)
		// only agents this orchestrator is allowed to spawn
		if !t.parent.Agent.canSpawn(tk.Agent) {
			results[i] = result{agent: tk.Agent, task: tk.Task,
				out: "not allowed to spawn " + strconvQuoteAgent(tk.Agent), failed: true}
			continue
		}
		wg.Add(1)
		go func(i int, a Agent, task string) {
			defer wg.Done()
			node := t.orch.start(rootNodeID, a.Label, task)
			child := t.parent.child(a)
			out, u := runChild(ctx, child, task, node, t.orch)
			if strings.TrimSpace(out) == "" {
				out = "(no output)"
			}
			report := out
			t.orch.update(node, func(n *RunNode) {
				n.Status = RunDone
				n.Activity = ""
				n.Report = report
				n.Ended = time.Now()
			})
			mu.Lock()
			results[i] = result{agent: a.Label, task: task, out: out}
			totals.add(u)
			mu.Unlock()
		}(i, a, tk.Task)
	}
	wg.Wait()

	// fold the team's usage into the parent session
	t.parent.usage.add(totals)

	var b strings.Builder
	for _, r := range results {
		head := "## " + r.agent + " — " + r.task
		if r.failed {
			head += " (skipped)"
		}
		b.WriteString(head + "\n" + strings.TrimSpace(r.out) + "\n\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func strconvQuoteAgent(s string) string { return `"` + s + `"` }
