package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(updateGoal{}) }

// updateGoal records the model's structured per-turn goal disposition for the
// active goal turn. Like complete_step it has no host side effects: the call
// only records candidate state, and the real FSM transition happens after the
// turn ends, once Delivery readiness and budget checks pass. It is a host
// workflow operation — it never requires write approval and grants no
// permissions. Outside an active goal turn it fails closed without changing
// any state, so plain chat cannot be hijacked into goal machinery.
type updateGoal struct{}

func (updateGoal) Name() string { return "update_goal" }

func (updateGoal) Description() string {
	return "Report this turn's disposition for the active goal. Call it at the end of every goal turn instead of using prose markers: `continue` (work is ongoing — give a concrete next_action), `complete` (the request is fully done, output format and constraints satisfied, and verification was attempted or reported unavailable), or `blocked` (only the user can unblock: missing user-only information, an irreversible/externally visible operation, or changed scope). The host validates your claim against Delivery acceptance criteria and decides whether to continue automatically. Fields: `status` (required, one of continue|complete|blocked), `reason` (required for continue and blocked, optional for complete), `next_action` (optional concrete next step; recommended for continue)."
}

func (updateGoal) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "status":{"type":"string","enum":["continue","complete","blocked"],"description":"continue = keep working autonomously; complete = the goal is fully done and verified; blocked = only the user can unblock."},
  "reason":{"type":"string","description":"Short explanation. REQUIRED for continue and blocked; optional for complete."},
  "next_action":{"type":"string","description":"Optional concrete next step. Recommended for continue so the host can guide the next turn."}
},
"required":["status"]
}`)
}

// ReadOnly is true: update_goal only records a claim; the host performs the
// state transition after the turn. It never needs approval and cannot expand
// tool permissions or bypass sandbox policy.
func (updateGoal) ReadOnly() bool { return true }

// PlanModeSafe reports true: the tool is read-only host bookkeeping, and
// outside an active goal turn its Execute fails closed anyway.
func (updateGoal) PlanModeSafe() bool { return true }

func (updateGoal) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Status     string `json:"status"`
		Reason     string `json:"reason"`
		NextAction string `json:"next_action"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid update_goal args: %w", err)
	}
	// Tolerate the common off-contract spellings models actually emit
	// ("done", "finished", "in_progress", "stuck", ...) by normalizing them
	// onto the contract statuses; anything unrecognized still fails closed
	// with the accepted set spelled out.
	p.Status = normalizeGoalStatus(p.Status)
	switch p.Status {
	case "continue", "complete", "blocked":
	default:
		return "", fmt.Errorf("update_goal: status must be one of continue|complete|blocked, got %q — no goal state was changed", p.Status)
	}
	// Reason fallback: for continue/blocked a missing reason borrows
	// next_action when present (the model usually restates the reason there
	// anyway); only a fully empty pair fails, keeping the host's decision
	// input intact.
	reason := strings.TrimSpace(p.Reason)
	if reason == "" && (p.Status == "continue" || p.Status == "blocked") {
		reason = strings.TrimSpace(p.NextAction)
	}
	if reason == "" && (p.Status == "continue" || p.Status == "blocked") {
		return "", fmt.Errorf("update_goal: reason is required for %s — no goal state was changed", p.Status)
	}
	recorder, ok := tool.GoalTurnRecorderFromContext(ctx)
	if !ok {
		return "", fmt.Errorf("update_goal is only available while an active goal turn is running — no goal state was changed")
	}
	return recorder.RecordGoalReport(tool.GoalReport{
		Status:     p.Status,
		Reason:     reason,
		NextAction: strings.TrimSpace(p.NextAction),
	})
}

// normalizeGoalStatus maps common off-contract status spellings onto the
// update_goal contract values. Unrecognized values pass through unchanged so
// the caller's fail-closed error keeps the model's own wording.
func normalizeGoalStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "continue", "complete", "blocked":
		return s
	}
	switch s {
	case "done", "finished", "finish", "success", "succeeded", "yes", "completed", "resolved":
		return "complete"
	case "working", "in_progress", "in-progress", "progress", "ongoing", "running":
		return "continue"
	case "stuck", "failed", "cannot", "can't", "unable", "error", "waiting", "pending":
		return "blocked"
	}
	return s
}
