package rlm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// stubPlanner returns scripted decisions.
type stubPlanner struct {
	mu       sync.Mutex
	decideFn func(ctx context.Context, task Task, pc PlanContext) (Decision, error)
	calls    []string
}

func (p *stubPlanner) Decide(ctx context.Context, task Task, pc PlanContext) (Decision, error) {
	p.mu.Lock()
	p.calls = append(p.calls, task.Text)
	p.mu.Unlock()
	return p.decideFn(ctx, task, pc)
}

type stubSolver struct {
	mu     sync.Mutex
	output map[string]string
	errs   map[string]error
	calls  []string
}

func (s *stubSolver) Solve(ctx context.Context, task Task) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, task.Text)
	s.mu.Unlock()
	if err := s.errs[task.Text]; err != nil {
		return "", err
	}
	return "answer(" + task.Text + ")", nil
}

type stubSynthesizer struct {
	mu    sync.Mutex
	calls []string
}

func (s *stubSynthesizer) Synthesize(ctx context.Context, task Task, children []ChildResult) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, task.Text)
	s.mu.Unlock()
	var b strings.Builder
	b.WriteString("merged(")
	for i, c := range children {
		if i > 0 {
			b.WriteString("+")
		}
		b.WriteString(c.Output)
	}
	b.WriteString(")")
	return b.String(), nil
}

func newTestEngine(t *testing.T, planner *stubPlanner, solver *stubSolver, syn *stubSynthesizer, mutate func(*Options)) *Engine {
	t.Helper()
	opts := Options{Planner: planner, Solver: solver, Synthesizer: syn}
	if mutate != nil {
		mutate(&opts)
	}
	e, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func solvePlanner() *stubPlanner {
	return &stubPlanner{decideFn: func(_ context.Context, _ Task, _ PlanContext) (Decision, error) {
		return Decision{Action: "solve"}, nil
	}}
}

func decomposePlanner(subtasks []string) *stubPlanner {
	return &stubPlanner{decideFn: func(_ context.Context, _ Task, _ PlanContext) (Decision, error) {
		return Decision{Action: "decompose", Subtasks: subtasks}, nil
	}}
}

func TestSimpleTaskSolvesDirectly(t *testing.T) {
	planner := solvePlanner()
	solver := &stubSolver{output: map[string]string{}}
	syn := &stubSynthesizer{}
	e := newTestEngine(t, planner, solver, syn, nil)
	res, err := e.Run(context.Background(), Task{ID: "r", Text: "do a thing"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCompleted || !strings.Contains(res.Output, "do a thing") {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Children) != 0 {
		t.Fatalf("no decomposition expected, got children %+v", res.Children)
	}
}

func TestDecomposeTreeMergesChildren(t *testing.T) {
	planner := decomposePlanner([]string{"sub a", "sub b"})
	solver := &stubSolver{}
	syn := &stubSynthesizer{}
	e := newTestEngine(t, planner, solver, syn, nil)
	res, err := e.Run(context.Background(), Task{ID: "r", Text: "big task"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if len(res.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(res.Children))
	}
	if res.Output != "merged(answer(sub a)+answer(sub b))" {
		t.Fatalf("synthesized output = %q", res.Output)
	}
	if len(syn.calls) != 1 {
		t.Fatalf("synthesizer calls = %d, want 1", len(syn.calls))
	}
}

func TestDepthGuardForcesSolve(t *testing.T) {
	planner := decomposePlanner([]string{"c1", "c2"})
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, func(o *Options) { o.MaxDepth = 1 })
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	// Root decomposes at depth 0; both children hit the depth cap and are
	// forced to solve directly (no planner call for them).
	if len(res.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(res.Children))
	}
	for _, c := range res.Children {
		if c.Reason != "depth limit reached" {
			t.Fatalf("child reason = %q, want depth limit", c.Reason)
		}
	}
	if len(planner.calls) != 1 {
		t.Fatalf("planner calls = %v, want only root", planner.calls)
	}
}

func TestLineageCycleForcesSolve(t *testing.T) {
	// c1's child "ROOT" normalizes to an ancestor: the cycle guard must
	// force solve on that node instead of recursing forever.
	planner := &stubPlanner{decideFn: func(_ context.Context, task Task, _ PlanContext) (Decision, error) {
		switch task.Text {
		case "root":
			return Decision{Action: "decompose", Subtasks: []string{"c1", "c2"}}, nil
		case "c1":
			return Decision{Action: "decompose", Subtasks: []string{"ROOT", "c1x"}}, nil
		default:
			return Decision{Action: "solve"}, nil
		}
	}}
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, nil)
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	var cycled bool
	var walk func(r Result)
	walk = func(r Result) {
		if r.Reason == "cycle detected in task lineage" {
			cycled = true
		}
		for _, c := range r.Children {
			walk(c)
		}
	}
	walk(res)
	if !cycled {
		t.Fatalf("no cycle-guard node found in tree: %+v", res)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("root status = %s, want completed", res.Status)
	}
}

func TestNodeBudgetExhaustionForcesSolve(t *testing.T) {
	planner := decomposePlanner([]string{"a", "b", "c"})
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, func(o *Options) { o.MaxNodes = 2 })
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	// Root consumes node 1; budget 2 leaves 1 < 2 → forced solve at root.
	if res.Reason != "remaining node budget too small to decompose" {
		t.Fatalf("reason = %q, want budget guard", res.Reason)
	}
}

func TestSanitizeDropsEmptyParentAndDuplicates(t *testing.T) {
	got := sanitizeSubtasks([]string{"", "  ", "real work", "real work", "REAL WORK", "fix parser", "Fix Parser"}, "fix parser")
	want := []string{"real work"}
	if len(got) != len(want) {
		t.Fatalf("sanitized = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sanitized = %v, want %v", got, want)
		}
	}
}

func TestTruncateRespectsBranchingAndBudget(t *testing.T) {
	subs := []string{"a", "b", "c", "d", "e"}
	if got := truncateSubtasks(subs, 4, 100); len(got) != 4 {
		t.Fatalf("branching cap: got %d", len(got))
	}
	if got := truncateSubtasks(subs, 4, 3); len(got) != 3 {
		t.Fatalf("budget cap: got %d", len(got))
	}
	if got := truncateSubtasks(subs, 4, 0); got != nil {
		t.Fatalf("zero budget: got %v", got)
	}
}

func TestChildFailureDoesNotCascade(t *testing.T) {
	planner := decomposePlanner([]string{"good", "bad"})
	solver := &stubSolver{errs: map[string]error{"bad": errors.New("boom")}}
	syn := &stubSynthesizer{}
	e := newTestEngine(t, planner, solver, syn, nil)
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	if res.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed despite one failed child", res.Status)
	}
	if len(res.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(res.Children))
	}
	var statuses []Status
	for _, c := range res.Children {
		statuses = append(statuses, c.Status)
	}
	if statuses[0] != StatusCompleted || statuses[1] != StatusFailed {
		t.Fatalf("child statuses = %v", statuses)
	}
}

func TestAllChildrenFailedMarksParentFailed(t *testing.T) {
	planner := decomposePlanner([]string{"a", "b"})
	solver := &stubSolver{errs: map[string]error{"a": errors.New("x"), "b": errors.New("y")}}
	syn := &stubSynthesizer{}
	e := newTestEngine(t, planner, solver, syn, nil)
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	if res.Status != StatusFailed || res.Reason != "all children failed" {
		t.Fatalf("result = %+v", res)
	}
	if len(syn.calls) != 0 {
		t.Fatalf("synthesizer must not run when all children failed")
	}
}

func TestModeSolveSkipsPlanner(t *testing.T) {
	planner := solvePlanner()
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, func(o *Options) { o.Mode = ModeSolve })
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	if res.Status != StatusCompleted {
		t.Fatalf("status = %s", res.Status)
	}
	if len(planner.calls) != 0 {
		t.Fatalf("planner called in solve mode: %v", planner.calls)
	}
}

func TestModeDecomposeFailsBelowTwoSubtasks(t *testing.T) {
	planner := decomposePlanner([]string{"only one"})
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, func(o *Options) { o.Mode = ModeDecompose })
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	if res.Status != StatusFailed || !strings.Contains(res.Reason, "at least 2") {
		t.Fatalf("result = %+v", res)
	}
}

func TestCancellationAbortsTree(t *testing.T) {
	planner := &stubPlanner{decideFn: func(ctx context.Context, _ Task, _ PlanContext) (Decision, error) {
		<-ctx.Done()
		return Decision{}, ctx.Err()
	}}
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := e.Run(ctx, Task{ID: "r", Text: "root"})
	if res.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", res.Status)
	}
}

func TestEventsEmitted(t *testing.T) {
	planner := decomposePlanner([]string{"a", "b"})
	solver := &stubSolver{}
	var mu sync.Mutex
	var kinds []string
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, func(o *Options) {
		// The engine may emit from concurrent child goroutines; the
		// callback must be safe to call concurrently.
		o.OnEvent = func(ev Event) {
			mu.Lock()
			kinds = append(kinds, ev.Kind)
			mu.Unlock()
		}
	})
	_, _ = e.Run(context.Background(), Task{ID: "r", Text: "root"})
	joined := strings.Join(kinds, ",")
	for _, want := range []string{"run_start", "node_start", "node_decompose", "node_end"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("events missing %q: %s", want, joined)
		}
	}
}

func TestPlannerErrorFailsNode(t *testing.T) {
	planner := &stubPlanner{decideFn: func(_ context.Context, _ Task, _ PlanContext) (Decision, error) {
		return Decision{}, errors.New("bad json")
	}}
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, nil)
	res, _ := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	if res.Status != StatusFailed || !strings.Contains(res.Reason, "planner error") {
		t.Fatalf("result = %+v", res)
	}
}

// TestConcurrentDecompositionRaceFree runs a wide tree under the race
// detector to prove the node budget is counted atomically across sibling
// goroutines.
func TestConcurrentDecompositionRaceFree(t *testing.T) {
	planner := &stubPlanner{decideFn: func(_ context.Context, task Task, _ PlanContext) (Decision, error) {
		if task.Text == "root" {
			return Decision{Action: "decompose", Subtasks: []string{"a", "b", "c"}}, nil
		}
		return Decision{Action: "decompose", Subtasks: []string{"x1", "x2"}}, nil
	}}
	solver := &stubSolver{}
	e := newTestEngine(t, planner, solver, &stubSynthesizer{}, func(o *Options) {
		o.MaxDepth = 3
		o.MaxNodes = 8
		o.Concurrency = 4
	})
	res, err := e.Run(context.Background(), Task{ID: "r", Text: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if len(res.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(res.Children))
	}
}

func TestNewValidatesInterfaces(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing planner must be rejected")
	}
	if _, err := New(Options{Planner: solvePlanner()}); err == nil {
		t.Fatal("missing solver must be rejected")
	}
	if _, err := New(Options{Planner: solvePlanner(), Solver: &stubSolver{}}); err == nil {
		t.Fatal("missing synthesizer must be rejected")
	}
}

func TestPromptsContainConstraints(t *testing.T) {
	pc := PlanContext{Depth: 1, MaxDepth: 3, RemainingNodes: 5, Mode: ModeDecompose}
	p := PlannerPrompt(Task{Text: "t"}, pc)
	if !strings.Contains(p, "ONLY a JSON object") || !strings.Contains(p, `"action"`) {
		t.Fatalf("planner prompt missing JSON constraint: %q", p)
	}
	if !strings.Contains(p, "MUST decompose") {
		t.Fatalf("decompose mode constraint missing: %q", p)
	}
	s := SynthesizePrompt(Task{Text: "t"}, []ChildResult{{Status: StatusFailed, Task: Task{Text: "c"}}})
	if !strings.Contains(s, "do not infer") {
		t.Fatalf("synthesizer no-inference rule missing: %q", s)
	}
}
