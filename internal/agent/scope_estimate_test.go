package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// scopeTestRegistry registers the shared fakeReadFileTool (delivery_hardening_test.go)
// so read-call counting and telemetry are exercised without real files.
func scopeTestRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	reg.Add(fakeReadFileTool{})
	return reg
}

// TestScopeEstimateBlockInjectedOnce pins P0-1: the E3 scope-estimation block
// is prepended to a user turn, idempotent on retries, and stripped from
// display text like every other transient block.
func TestScopeEstimateBlockInjectedOnce(t *testing.T) {
	mp := testutil.NewMock("m", testutil.Turn{Text: "done"}, testutil.Turn{Text: "done"})
	a := New(mp, scopeTestRegistry(), NewSession("sys"), Options{}, event.Discard)
	if err := a.Run(context.Background(), "fix the icon link"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := mp.Requests()[0]
	last := req.Messages[len(req.Messages)-1]
	if last.Role != provider.RoleUser || !strings.Contains(last.Content, "<scope-estimate>") {
		t.Fatalf("scope-estimate block missing from user turn: %q", last.Content)
	}
	if !strings.Contains(last.Content, "estimate this task's execution scope") {
		t.Fatalf("scope-estimate block lost its instruction text")
	}
	// Idempotent: a second Run on the same input must not double-inject.
	if err := a.Run(context.Background(), "fix the icon link"); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	req2 := mp.Requests()[1]
	last2 := req2.Messages[len(req2.Messages)-1]
	if got := strings.Count(last2.Content, "<scope-estimate>"); got != 1 {
		t.Fatalf("scope-estimate injected %d times, want 1", got)
	}
	// Display stripping: the block must not surface as user text.
	if got := StripTransientUserBlocks(last2.Content); strings.Contains(got, "scope-estimate") {
		t.Fatalf("scope-estimate leaked into display: %q", got)
	}
}

// TestScopeEstimateDisabled pins the opt-out: no block, no read-budget hint,
// no telemetry notice.
func TestScopeEstimateDisabled(t *testing.T) {
	mp := testutil.NewMock("m", testutil.Turn{Text: "done"})
	a := New(mp, scopeTestRegistry(), NewSession("sys"), Options{DisableScopeEstimation: true}, event.Discard)
	if err := a.Run(context.Background(), "fix the icon link"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	last := mp.Requests()[0].Messages[len(mp.Requests()[0].Messages)-1]
	if strings.Contains(last.Content, "<scope-estimate>") {
		t.Fatalf("scope-estimate injected despite opt-out: %q", last.Content)
	}
	// The gate must also silence the per-turn counter, hint and telemetry.
	if a.scopeReadCalls.Load() != 0 {
		t.Fatalf("read counter advanced despite opt-out: %d", a.scopeReadCalls.Load())
	}
	a.scopeReadCalls.Store(scopeReadBudgetThreshold + 5)
	results := []string{"out"}
	a.maybeScopeHint(results, 0, "read_file", "")
	if results[0] != "out" {
		t.Fatalf("hint attached despite opt-out: %q", results[0])
	}
	if a.scopeReadCalls.Load() != scopeReadBudgetThreshold+5 {
		t.Fatalf("counter advanced despite opt-out: %d", a.scopeReadCalls.Load())
	}
	sink := &recordingSink{}
	a.sink = sink
	a.emitScopeNotice()
	if sink.sawScopeNotice() {
		t.Fatalf("scope notice emitted despite opt-out: %v", sink.notices)
	}
}

// TestScopeReadBudgetHint pins P1-1/P1-2: successful read-tool calls count
// toward the turn's read budget; once the threshold is crossed the result
// carries the accumulated hint so the model converges or expands deliberately.
func TestScopeReadBudgetHint(t *testing.T) {
	calls := make([]provider.ToolCall, 0, scopeReadBudgetThreshold+2)
	for i := 0; i < scopeReadBudgetThreshold+2; i++ {
		calls = append(calls, provider.ToolCall{
			ID:        string(rune('a' + i)),
			Name:      "read_file",
			Arguments: `{"path":"/w/seed.txt"}`,
		})
	}
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: calls},
		testutil.Turn{Text: "done"},
	)
	a := New(mp, scopeTestRegistry(), NewSession("sys"), Options{}, event.Discard)
	if err := a.Run(context.Background(), "inspect the file"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The batch's tool results are persisted in the session; the threshold
	// hint must appear on the over-budget results.
	hinted := 0
	for _, m := range a.Session().Messages {
		if m.Role != provider.RoleTool {
			continue
		}
		if strings.Contains(m.Content, "[scope]") {
			hinted++
		}
	}
	if hinted == 0 {
		t.Fatalf("no read-budget hint surfaced in tool results")
	}
	if got := a.scopeReadCalls.Load(); got < scopeReadBudgetThreshold {
		t.Fatalf("read calls counted = %d, want >= %d", got, scopeReadBudgetThreshold)
	}
}

// TestScopeReadBudgetThresholdUnit pins the counting and hint logic directly.
func TestScopeReadBudgetThresholdUnit(t *testing.T) {
	a := &Agent{}
	for i := 0; i < scopeReadBudgetThreshold; i++ {
		a.scopeReadCalls.Add(1)
	}
	if hint := a.scopeReadHint(); hint != "" {
		t.Fatalf("hint before threshold: %q", hint)
	}
	a.scopeReadCalls.Add(1)
	hint := a.scopeReadHint()
	if !strings.Contains(hint, "[scope]") || !strings.Contains(hint, "16 read calls") {
		t.Fatalf("hint after threshold missing count: %q", hint)
	}
	if !isScopeReadTool("read_file") || !isScopeReadTool("glob") || !isScopeReadTool("grep") {
		t.Fatalf("read-tool prefixes not recognized")
	}
	if isScopeReadTool("write_file") || isScopeReadTool("bash") {
		t.Fatalf("writer/exec tools counted as reads")
	}
}

// TestPlannerPromptIncludesScopeEstimate pins P0-2.
func TestPlannerPromptIncludesScopeEstimate(t *testing.T) {
	for _, want := range []string{"estimate the task's execution scope", "single-file change", "repository-wide task", "minimum set of files"} {
		if !strings.Contains(DefaultPlannerPrompt, want) {
			t.Fatalf("DefaultPlannerPrompt missing %q", want)
		}
	}
}

// TestScopeTelemetryNotice pins P2: a turn that read files emits the scope
// notice; a turn that read nothing emits none.
func TestScopeTelemetryNotice(t *testing.T) {
	sink := &recordingSink{}
	mp := testutil.NewMock("m",
		testutil.Turn{ToolCalls: []provider.ToolCall{{
			ID: "r0", Name: "read_file", Arguments: `{"path":"/w/seed.txt"}`,
		}}},
		testutil.Turn{Text: "done"},
	)
	a := New(mp, scopeTestRegistry(), NewSession("sys"), Options{}, sink)
	if err := a.Run(context.Background(), "read it"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sink.sawScopeNotice() {
		t.Fatalf("scope notice not emitted; events: %v", sink.notices)
	}
}

type recordingSink struct {
	notices []string
}

func (s *recordingSink) Emit(e event.Event) {
	if e.Kind == event.Notice {
		s.notices = append(s.notices, e.Text)
	}
}

func (s *recordingSink) sawScopeNotice() bool {
	for _, n := range s.notices {
		if strings.HasPrefix(n, "scope: ") {
			return true
		}
	}
	return false
}
