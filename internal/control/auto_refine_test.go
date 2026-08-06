package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/refine"
)

func TestRefineToolContract(t *testing.T) {
	tool := refineTool{}
	if tool.Name() != "refine" {
		t.Fatalf("name = %q", tool.Name())
	}
	if !tool.ReadOnly() {
		t.Fatal("refine tool must be read-only so it rides the agent flow without approval")
	}
	if !strings.Contains(tool.Description(), "Continual Harness") {
		t.Fatal("description should explain the harness")
	}
	if len(tool.Schema()) == 0 || !strings.Contains(string(tool.Schema()), "instructions") {
		t.Fatal("schema should accept instructions/scope")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"scope":"global","instructions":"x"}`)); err == nil {
		t.Fatal("execute with nil controller must fail")
	}
}

func TestRefineToolParsesScope(t *testing.T) {
	// Exercise the argument parse path with a stub controller whose refiner is
	// nil: refineRun must fail fast with the planner-unavailable error, proving
	// scope/instructions reached the runner.
	c := &Controller{refiner: nil}
	tool := refineTool{c: c}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"scope":"global","instructions":"focus"}`))
	if err == nil || !strings.Contains(err.Error(), "planner unavailable") {
		t.Fatalf("expected planner-unavailable error, got %v", err)
	}
}

func TestMaybeAutoRefineDisabledNoOp(t *testing.T) {
	c := &Controller{} // harnessAutoRefine false, refiner nil
	c.maybeAutoRefine("auto")
	// No goroutine may start: refiner is nil and the flag is off. Nothing to
	// assert beyond not panicking; the throttle field must stay untouched.
	c.mu.Lock()
	zero := c.lastAutoRefine.IsZero()
	c.mu.Unlock()
	if !zero {
		t.Fatal("disabled auto-refine must not touch the throttle slot")
	}
}

func TestMaybeAutoRefineThrottle(t *testing.T) {
	// refiner nil → the gate returns before reserving the slot even when the
	// flag is on; a nil refiner must not panic or leak a goroutine.
	c := &Controller{harnessAutoRefine: true, harnessAutoRefineInterval: time.Hour}
	c.maybeAutoRefine("auto")
	c.mu.Lock()
	zero := c.lastAutoRefine.IsZero()
	c.mu.Unlock()
	if !zero {
		t.Fatal("nil refiner must not reserve the throttle slot")
	}
}

func TestOnAgentTurnDoneInterval(t *testing.T) {
	// Turn-interval gating: only turns landing on the interval advance the
	// gate; nil refiner keeps the slot untouched.
	c := &Controller{harnessAutoRefine: true, autoRefineIntervalTurns: 25}
	c.onAgentTurnDone(24)
	c.onAgentTurnDone(25) // boundary: refiner nil → no reservation, no panic
	c.onAgentTurnDone(26)
	c.mu.Lock()
	zero := c.lastAutoRefine.IsZero()
	c.mu.Unlock()
	if !zero {
		t.Fatal("nil refiner must not reserve the throttle slot on interval triggers")
	}

	// Interval 0 disables the trigger entirely.
	off := &Controller{harnessAutoRefine: true, autoRefineIntervalTurns: 0}
	off.onAgentTurnDone(25)
	off.mu.Lock()
	zero = off.lastAutoRefine.IsZero()
	off.mu.Unlock()
	if !zero {
		t.Fatal("interval 0 must disable the turn trigger")
	}
}

func TestHarnessConfigDefaults(t *testing.T) {
	cfg := config.Default()
	if !cfg.HarnessEnabled() {
		t.Fatal("harness should be enabled by default")
	}
	if !cfg.HarnessAutoRefine() {
		t.Fatal("auto-refine should be on by default (integrated into long-task machinery)")
	}
	if cfg.HarnessAutoRefineMinInterval() <= 0 {
		t.Fatal("default interval must be positive")
	}
	// Zero-value config (written before [harness] existed) falls back to on.
	zero := &config.Config{}
	if !zero.HarnessEnabled() || !zero.HarnessAutoRefine() {
		t.Fatal("zero-value harness config must default to enabled")
	}
	// Explicit opt-out is honored.
	off := &config.Config{Harness: config.HarnessConfig{Enabled: boolRef(false), AutoRefine: boolRef(false)}}
	if off.HarnessEnabled() || off.HarnessAutoRefine() {
		t.Fatal("explicit false must disable")
	}
}

func boolRef(v bool) *bool { return &v }

func TestHarnessRenderSection(t *testing.T) {
	rendered := config.RenderTOMLForScope(config.Default(), config.RenderScopeUser)
	if !strings.Contains(rendered, "[harness]") {
		t.Fatal("default render must include the [harness] section")
	}
	if !strings.Contains(rendered, "auto_refine = true") {
		t.Fatal("default render must show auto_refine = true")
	}
	if !strings.Contains(rendered, "auto_refine_min_interval_minutes = 10") {
		t.Fatal("default render must show the interval")
	}
	// An opt-out config renders the toggles as false.
	off := config.Default()
	off.Harness = config.HarnessConfig{Enabled: boolRef(false), AutoRefine: boolRef(false), AutoRefineMinIntervalMins: 30}
	rendered = config.RenderTOMLForScope(off, config.RenderScopeUser)
	if !strings.Contains(rendered, "enabled = false") || !strings.Contains(rendered, "auto_refine = false") {
		t.Fatalf("opt-out render missing toggles:\n%s", rendered)
	}
}

var _ = refine.ScopeProject // keep the import for future gate tests
