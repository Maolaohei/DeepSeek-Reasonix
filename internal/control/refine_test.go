package control

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/refine"
)

func TestParseRefineArgs(t *testing.T) {
	cases := []struct {
		args         string
		wantScope    refine.Scope
		wantRollback string
		wantInstr    string
	}{
		{"", refine.ScopeProject, "", ""},
		{"--global", refine.ScopeGlobal, "", ""},
		{"-g", refine.ScopeGlobal, "", ""},
		{"--rollback refine_20260701", refine.ScopeProject, "refine_20260701", ""},
		{"--rollback refine_20260701 --global", refine.ScopeGlobal, "refine_20260701", ""},
		{"prefer dedup before expand", refine.ScopeProject, "", "prefer dedup before expand"},
		{"--global avoid repeated grid scans", refine.ScopeGlobal, "", "avoid repeated grid scans"},
		{"-r x --global mixed tokens", refine.ScopeGlobal, "x", "mixed tokens"},
	}
	for _, tc := range cases {
		scope, rollback, instr := parseRefineArgs(tc.args)
		if scope != tc.wantScope || rollback != tc.wantRollback || instr != tc.wantInstr {
			t.Fatalf("parseRefineArgs(%q) = (%s, %q, %q), want (%s, %q, %q)",
				tc.args, scope, rollback, instr, tc.wantScope, tc.wantRollback, tc.wantInstr)
		}
	}
}

func TestSerializeTrajectory(t *testing.T) {
	msgs := []provider.Message{
		{Role: provider.RoleSystem, Content: "system prompt"},
		{Role: provider.RoleUser, Content: "solve the grid task"},
		{Role: provider.RoleAssistant, Content: "I will dedup first.", ToolCalls: []provider.ToolCall{
			{Name: "read_file", Arguments: `{"path":"x"}`},
		}},
		{Role: provider.RoleTool, Name: "read_file", Content: "file contents"},
		{Role: provider.RoleUser, Content: "local only", LocalOnly: true},
	}
	text := serializeTrajectory(msgs, 1<<20)
	for _, want := range []string{"## user", "solve the grid task", "## assistant", "## tool_call read_file", "## tool_result read_file"} {
		if !strings.Contains(text, want) {
			t.Fatalf("trajectory missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "system prompt") {
		t.Fatal("system messages should be excluded from the trajectory slice")
	}
	if strings.Contains(text, "local only") {
		t.Fatal("LocalOnly messages must be excluded")
	}
}

func TestSerializeTrajectoryTruncates(t *testing.T) {
	var msgs []provider.Message
	for range 50 {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: strings.Repeat("x", 5000)})
	}
	text := serializeTrajectory(msgs, 10_000)
	if len(text) > 20_000 {
		t.Fatalf("trajectory not bounded: %d chars", len(text))
	}
	if !strings.Contains(text, "truncated") {
		t.Fatal("expected truncation marker")
	}
}

func TestHarnessStoreScope(t *testing.T) {
	c := &Controller{workspaceRoot: "/work/proj"}
	project := c.harnessStore(refine.ScopeProject)
	if project.Scope != refine.ScopeProject || project.Dir == "" {
		t.Fatalf("unexpected project store: %+v", project)
	}
	if !strings.Contains(project.Dir, "harness") {
		t.Fatalf("project store dir missing harness segment: %q", project.Dir)
	}
	global := c.harnessStore(refine.ScopeGlobal)
	if global.Scope != refine.ScopeGlobal || global.Dir == "" {
		t.Fatalf("unexpected global store: %+v", global)
	}
}
