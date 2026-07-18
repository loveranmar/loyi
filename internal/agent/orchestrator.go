package agent

import (
	"context"
	"sync"
	"time"
)

// rootNodeID is the ParentID for top-level spawns (the main session).
const rootNodeID = 0

// RunStatus is the lifecycle state of a spawned agent.
type RunStatus string

const (
	RunRunning RunStatus = "running"
	RunDone    RunStatus = "done"
	RunFailed  RunStatus = "failed"
)

// RunNode is one node in the live agent tree — a running (or finished)
// sub-agent. The monitor renders a snapshot of these.
type RunNode struct {
	ID       int
	ParentID int
	Agent    string // agent label, e.g. "build"
	Task     string
	Status   RunStatus
	Activity string // latest tool activity, e.g. "write main.go"
	Err      string
	Started  time.Time
	Ended    time.Time
}

// Elapsed is how long the node has been (or was) running.
func (n RunNode) Elapsed() time.Duration {
	end := n.Ended
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(n.Started)
}

// Orchestrator tracks the tree of spawned agents so the UI can show who's
// working and for how long. It is safe for concurrent use.
type Orchestrator struct {
	mu    sync.Mutex
	nodes []*RunNode
	seq   int
}

func NewOrchestrator() *Orchestrator { return &Orchestrator{} }

func (o *Orchestrator) start(parent int, agentLabel, task string) *RunNode {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	n := &RunNode{
		ID: o.seq, ParentID: parent, Agent: agentLabel, Task: task,
		Status: RunRunning, Started: time.Now(),
	}
	o.nodes = append(o.nodes, n)
	return n
}

func (o *Orchestrator) update(n *RunNode, fn func(*RunNode)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fn(n)
}

// Snapshot returns a copy of the current tree, oldest first.
func (o *Orchestrator) Snapshot() []RunNode {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]RunNode, len(o.nodes))
	for i, n := range o.nodes {
		out[i] = *n
	}
	return out
}

// Active reports how many nodes are still running.
func (o *Orchestrator) Active() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, node := range o.nodes {
		if node.Status == RunRunning {
			n++
		}
	}
	return n
}

// Clear drops finished nodes so the tree doesn't grow forever. Running nodes
// are kept.
func (o *Orchestrator) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	var keep []*RunNode
	for _, n := range o.nodes {
		if n.Status == RunRunning {
			keep = append(keep, n)
		}
	}
	o.nodes = keep
}

// child builds a sub-session that shares this session's provider, tools,
// workspace, model, and effort, running as the given agent. Children run
// autonomously: never in "ask" mode, so they don't block on a prompt.
func (s *Session) child(a Agent) *Session {
	perm := s.Perm
	if perm == "" || perm == PermAsk {
		perm = PermAuto
	}
	if a.Perm != "" {
		perm = a.Perm
	}
	return &Session{
		Provider:  s.Provider,
		Tools:     s.Tools,
		Agent:     a,
		Effort:    s.Effort,
		Model:     s.Model,
		Perm:      perm,
		Workspace: s.Workspace,
	}
}

// runChild runs a sub-session to completion, mirroring its progress onto the
// orchestrator node, and returns the agent's final text plus its usage. It
// never blocks on permission: anything the child's mode would prompt for is
// declined, so an autonomous worker can't run destructive commands unattended.
func runChild(ctx context.Context, child *Session, task string, node *RunNode, orch *Orchestrator) (string, Usage) {
	var final string
	emit := func(e Event) {
		switch ev := e.(type) {
		case PermissionEvent:
			ev.Reply <- false // autonomous: refuse anything that wants a human
		case ToolStartEvent:
			orch.update(node, func(n *RunNode) { n.Activity = ev.Summary })
		case TextEvent:
			final += ev.Text
		case ErrorEvent:
			orch.update(node, func(n *RunNode) { n.Err = ev.Err.Error() })
		}
	}
	child.Run(ctx, task, emit)
	return final, child.Usage()
}
