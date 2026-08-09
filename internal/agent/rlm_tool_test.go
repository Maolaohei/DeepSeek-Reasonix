package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/rlm"
	"reasonix/internal/tool"
)

func rlmTestTool(t *testing.T, prov *testutil.MockProvider, reg *tool.Registry) *RLMTool {
	t.Helper()
	taskTool := NewTaskToolWithOptions(TaskToolOptions{
		Provider:       prov,
		ParentRegistry: reg,
		MaxSteps:       20,
		SysPrompt:      "",
		ArchiveDir:     t.TempDir(),
	}).WithTranscripts(NewSubagentStore(t.TempDir()), t.TempDir(), "", "")
	return NewRLMTool(taskTool)
}

// TestRLMToolSolvesDirectly proves a planner "solve" decision runs the solver
// once and returns its answer plus the tree.
func TestRLMToolSolvesDirectly(t *testing.T) {
	reg := tool.NewRegistry()
	prov := testutil.NewMock("m",
		// planner node: returns a solve decision
		testutil.Turn{Text: `{"action":"solve","reason":"simple"}`},
		// solver node: final answer
		testutil.Turn{Text: "the answer is 42"},
	)
	rlm := rlmTestTool(t, prov, reg)
	out, err := rlm.Execute(context.Background(), json.RawMessage(`{"prompt":"answer the question"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the answer is 42") {
		t.Fatalf("output missing answer: %q", out)
	}
	if !strings.Contains(out, "Decomposition tree") || !strings.Contains(out, "[completed] answer the question") {
		t.Fatalf("output missing tree: %q", out)
	}
}

// TestRLMToolDecomposesAndSynthesizes proves a decompose decision runs two
// solver sub-agents and one synthesizer, returning the merged answer.
func TestRLMToolDecomposesAndSynthesizes(t *testing.T) {
	reg := tool.NewRegistry()
	prov := testutil.NewMock("m",
		// root planner: decompose into two subtasks
		testutil.Turn{Text: `{"action":"decompose","subtasks":["part a","part b"]}`},
		// child planners: both solve directly
		testutil.Turn{Text: `{"action":"solve"}`},
		testutil.Turn{Text: `{"action":"solve"}`},
		// solvers
		testutil.Turn{Text: "result a"},
		testutil.Turn{Text: "result b"},
		// synthesizer
		testutil.Turn{Text: "merged a+b"},
	)
	rlm := rlmTestTool(t, prov, reg)
	out, err := rlm.Execute(context.Background(), json.RawMessage(`{"prompt":"big task"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "merged a+b") {
		t.Fatalf("output missing synthesized answer: %q", out)
	}
	if !strings.Contains(out, "[completed] big task") || !strings.Contains(out, "part a") || !strings.Contains(out, "part b") {
		t.Fatalf("tree missing nodes: %q", out)
	}
}

// TestRLMToolModeSolveSkipsPlanner proves mode=solve runs only the solver.
func TestRLMToolModeSolveSkipsPlanner(t *testing.T) {
	reg := tool.NewRegistry()
	prov := testutil.NewMock("m",
		testutil.Turn{Text: "direct answer"},
	)
	rlm := rlmTestTool(t, prov, reg)
	out, err := rlm.Execute(context.Background(), json.RawMessage(`{"prompt":"just do it","mode":"solve"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "direct answer") {
		t.Fatalf("output = %q", out)
	}
	if prov.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 (no planner)", prov.CallCount())
	}
}

// TestRLMToolPlannerJSONToleratesMarkdown proves the planner output parser
// extracts the JSON from fenced or prose-wrapped responses.
func TestRLMToolPlannerJSONToleratesMarkdown(t *testing.T) {
	reg := tool.NewRegistry()
	prov := testutil.NewMock("m",
		testutil.Turn{Text: "Here is my decision:\n```json\n{\"action\":\"solve\",\"reason\":\"fine\"}\n```"},
		testutil.Turn{Text: "done"},
	)
	rlm := rlmTestTool(t, prov, reg)
	out, err := rlm.Execute(context.Background(), json.RawMessage(`{"prompt":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("output = %q", out)
	}
}

// TestParsePlannerDecision unit-covers the parser.
func TestParsePlannerDecision(t *testing.T) {
	if dec, err := parsePlannerDecision(`{"action":"decompose","subtasks":["a","b"]}`); err != nil || dec.Action != "decompose" || len(dec.Subtasks) != 2 {
		t.Fatalf("dec = %+v, err = %v", dec, err)
	}
	if _, err := parsePlannerDecision(`no json here`); err == nil {
		t.Fatal("missing JSON must fail")
	}
	if _, err := parsePlannerDecision(`{"action":"fly"}`); err == nil {
		t.Fatal("invalid action must fail")
	}
}

// TestWriteTreeRendersNestedStatuses covers the tree renderer.
func TestWriteTreeRendersNestedStatuses(t *testing.T) {
	var b strings.Builder
	writeTree(&b, rlm.Result{
		Task:   rlm.Task{Text: "root"},
		Status: rlm.StatusCompleted,
		Children: []rlm.Result{
			{Task: rlm.Task{Text: "child"}, Status: rlm.StatusFailed, Reason: "boom"},
		},
	}, "", true)
	got := b.String()
	if !strings.Contains(got, "[completed] root") || !strings.Contains(got, "[failed] child (boom)") {
		t.Fatalf("tree = %q", got)
	}
}
