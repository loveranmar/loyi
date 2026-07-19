package agent

import "testing"

func TestAllowsTool(t *testing.T) {
	build := AgentByID("build") // no Tools set → allows everything
	if !build.AllowsTool("run") || !build.AllowsTool("write") {
		t.Error("build should allow all tools")
	}

	plan := AgentByID("plan")
	if plan.AllowsTool("run") {
		t.Error("plan must not allow run")
	}
	if !plan.AllowsTool("write") || !plan.AllowsTool("read") {
		t.Error("plan should allow read and write")
	}

	pm := AgentByID("pm")
	if pm.AllowsTool("write") || pm.AllowsTool("run") {
		t.Error("pm is read-only; must not allow write or run")
	}
	if !pm.AllowsTool("read") || !pm.AllowsTool("web_search") {
		t.Error("pm should allow read-only tools")
	}
}

func TestAgentByIDUnknownFallsBackToDefault(t *testing.T) {
	a := AgentByID("does-not-exist")
	if a.ID != DefaultAgentID {
		t.Errorf("unknown id fell back to %q, want %q", a.ID, DefaultAgentID)
	}
}

func TestSwitchAgentAppliesPerm(t *testing.T) {
	s := &Session{Perm: PermAsk}
	// an agent with an explicit posture applies it
	s.SwitchAgent(Agent{ID: "x", Perm: PermBypass})
	if s.Perm != PermBypass {
		t.Errorf("perm = %q, want bypass", s.Perm)
	}
	// an agent without a posture leaves the current mode
	s.SwitchAgent(Agent{ID: "y"})
	if s.Perm != PermBypass {
		t.Errorf("perm = %q, want bypass to persist", s.Perm)
	}
}
