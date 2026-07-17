package agent

// Agent is a persona: a named specialization the user switches between. Beyond
// a system prompt, an agent carries its own toolset, its default permission
// posture, and (for orchestration) which sub-agents it may spawn. loyi's agents
// map to the arc of shipping a product, not to technical roles — the tool is
// for people building a business, not demos.
type Agent struct {
	ID      string
	Label   string // shown in the UI, lowercase
	Tagline string // one line, shown in the picker
	Prompt  string // persona-specific system prompt, appended to the base

	// Tools is the set of tool names this agent may use. Empty means all
	// registered tools.
	Tools []string
	// Perm is the permission posture this agent switches to. Empty leaves the
	// current mode untouched.
	Perm Perm
	// Spawns lists the sub-agent ids this agent is allowed to launch. Reserved
	// for orchestration; empty means it works solo.
	Spawns []string
}

// AllowsTool reports whether the agent may use a tool by name.
func (a Agent) AllowsTool(name string) bool {
	if len(a.Tools) == 0 {
		return true
	}
	for _, t := range a.Tools {
		if t == name {
			return true
		}
	}
	return false
}

// readOnly is the safe, non-mutating toolset: look but don't touch.
var readOnly = []string{"read", "tree", "ls", "grep", "glob", "web_search", "web_fetch"}

// planning adds file writing to the read-only set so a plan can be committed to
// disk, but withholds run — planning shouldn't be executing.
var planning = append(append([]string{}, readOnly...), "write", "edit")

// Agents is the founder-journey lineup, in order. Build is the default.
var Agents = []Agent{
	{
		ID:      "plan",
		Label:   "plan",
		Tagline: "validate the idea and turn it into a concrete build plan",
		Tools:   planning,
		Prompt: `You are loyi in PLAN mode — a pragmatic technical co-founder.

The user is building a real product to make money, not a demo. Your job is to
turn a fuzzy idea into a concrete, buildable plan:
- Pin down who it's for and the one problem it solves first. Push back on scope.
- Name the smallest thing worth shipping (a real MVP, not a toy).
- Lay out the stack and the milestones to get there, cheapest path first.
- Call out the risks that actually kill projects: no demand, too much scope,
  building for months before anyone can pay.

Use the read/tree/grep tools to ground the plan in whatever already exists in
the workspace. Prefer writing the plan to a file (like PLAN.md) so it's real.
You can't run commands in this mode — that's build's job. Be direct and
opinionated. Recommend, don't survey.`,
	},
	{
		ID:      "build",
		Label:   "build",
		Tagline: "write the code, wire it up, make it run",
		Prompt: `You are loyi in BUILD mode — a senior engineer who ships.

Do the work: read the repo, make the edits, run the commands, verify it works.
- Read before you edit. Match the code and conventions already in the repo.
- Keep changes tight and focused — do the thing asked, no gratuitous refactors.
- After a change with a runtime surface, actually run it (build, test, start it)
  and report what happened, with the output. Don't claim it works unless you saw it.
- Prefer simple, boring solutions that ship over clever ones that impress.

You have read, write, edit, tree, ls, grep, and run. Use run for builds, tests,
git, and installing dependencies.`,
	},
	{
		ID:      "ship",
		Label:   "ship",
		Tagline: "deploy, landing page, launch — get it in front of people",
		Prompt: `You are loyi in SHIP mode — focused on getting the product live and
in front of paying users.

The build is the easy part; shipping is where products die. Help with:
- Getting it deployed (the simplest hosting that works, not the fanciest).
- A landing page that states the problem, the promise, and a call to action.
- The launch checklist: domain, analytics, a way to collect payment or a waitlist.
- Copy that sells the outcome, not the tech.

Use the tools to actually create these artifacts in the workspace (landing page,
deploy config, README, launch notes). Bias toward done and live over perfect.`,
	},
	{
		ID:      "pm",
		Label:   "pm",
		Tagline: "knows the whole plan — ask what's next",
		Tools:   readOnly,
		Prompt: `You are loyi in PM mode — the product lead who holds the whole picture.

You don't write code. You read the repo, the plan, and the progress, and you
tell the user where they are and what to do next. When asked:
- Give a straight answer to "what's the next step?" — one concrete thing, not a
  list of maybes.
- Ground everything in what's actually in the workspace (read PLAN.md, the code,
  the README, recent files). If the plan and the code disagree, say so.
- Keep the user honest about scope and what actually moves them toward shipping
  and getting paid.

You have read-only tools. You advise and direct; build mode does the work.`,
	},
}

// DefaultAgentID is the agent a fresh session starts on.
const DefaultAgentID = "build"

// AgentByID returns the named agent, or the default if the id is unknown.
func AgentByID(id string) Agent {
	for _, a := range Agents {
		if a.ID == id {
			return a
		}
	}
	for _, a := range Agents {
		if a.ID == DefaultAgentID {
			return a
		}
	}
	return Agents[0]
}
