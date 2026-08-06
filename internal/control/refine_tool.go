package control

import (
	"context"
	"encoding/json"
	"fmt"

	"reasonix/internal/refine"
	"reasonix/internal/tool"
)

// refineTool lets the model trigger a Continual Harness refinement from the
// trajectory: analyze what worked/failed and persist small evidence-backed
// prompt notes, memories, skills, or subagent specs. It is host-side (bound to
// the Controller) and runs an isolated no-tool planner call, so it must not
// recurse. It writes only the harness/memory/skill stores — never the
// workspace — and is declared read-only so ordinary Ask/Auto gates do not
// interrupt the agent's flow; every edit is recorded and rollbackable.
type refineTool struct{ c *Controller }

// NewRefineTool returns the model-facing `refine` tool.
func NewRefineTool(c *Controller) tool.Tool { return refineTool{c: c} }

func (refineTool) Name() string { return "refine" }

func (refineTool) Description() string {
	return "Run a Continual Harness refinement: analyze the recent trajectory and persist small, " +
		"evidence-backed reusable lessons as prompt notes, memories, skills, or subagent specs. " +
		"Use after a repeated failure, when a reusable tactic emerges, when a durable fact or " +
		"preference becomes clear, or when a user correction should persist. Edits apply to the " +
		"project harness by default; use scope \"global\" only for stable cross-session lessons " +
		"that should affect every workspace. The base system prompt, REASONIX.md, and AGENTS.md " +
		"are never modified. Keep edits small and grounded in trajectory evidence."
}

func (refineTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"instructions": {"type": "string", "description": "Optional focus guidance, e.g. which failure to learn from or which policy to capture."},
			"scope": {"type": "string", "enum": ["project", "global"], "description": "Where edits persist. Default project (this workspace). global affects every workspace — use sparingly."}
		}
	}`)
}

// ReadOnly is true on purpose: refine writes only harness/memory/skill state
// (never the workspace), and autonomous refinement is the feature — Prime
// Agent's model-triggered refine has no approval gate either. Every applied
// edit is recorded with before/after snapshots and is rollbackable.
func (refineTool) ReadOnly() bool { return true }

// PlanModeSafe is false: harness edits are host state management, not part of
// planning a task, so the tool is unavailable during plan mode.
func (refineTool) PlanModeSafe() bool { return false }

func (t refineTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Instructions string `json:"instructions"`
		Scope        string `json:"scope"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	scope := refine.ScopeProject
	if in.Scope == string(refine.ScopeGlobal) {
		scope = refine.ScopeGlobal
	}
	if t.c == nil {
		return "", fmt.Errorf("refine tool unavailable")
	}
	text, err := t.c.refineRun(ctx, scope, "", in.Instructions)
	if err != nil {
		return "", err
	}
	return text, nil
}
