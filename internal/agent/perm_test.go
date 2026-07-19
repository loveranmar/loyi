package agent

import (
	"encoding/json"
	"testing"

	"github.com/loveranmar/loyi/internal/tool"
)

func cmdInput(c string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": c})
	return b
}

func TestNeedsApproval(t *testing.T) {
	write := &tool.WriteTool{}
	run := &tool.RunTool{}
	safeCmd := cmdInput("go test ./...")
	riskyCmd := cmdInput("rm -rf /")

	cases := []struct {
		mode         Perm
		tool         tool.Tool
		input        json.RawMessage
		wantApproval bool
	}{
		{PermAsk, write, nil, true},
		{PermAsk, run, safeCmd, true},
		{PermBypass, write, nil, false},
		{PermBypass, run, riskyCmd, false},
		{PermAcceptEdits, write, nil, false},   // edits auto-approved
		{PermAcceptEdits, run, safeCmd, true},  // commands still ask
		{PermAuto, write, nil, false},          // edits are safe
		{PermAuto, run, safeCmd, false},        // known-safe command runs
		{PermAuto, run, riskyCmd, true},        // dangerous command asks
		{PermAuto, run, cmdInput("xyz"), true}, // unknown command asks
	}
	for _, tc := range cases {
		s := &Session{Perm: tc.mode}
		if got := s.needsApproval(tc.tool, tc.input); got != tc.wantApproval {
			t.Errorf("mode=%s tool=%s: needsApproval=%v, want %v", tc.mode, tc.tool.Name(), got, tc.wantApproval)
		}
	}
}
