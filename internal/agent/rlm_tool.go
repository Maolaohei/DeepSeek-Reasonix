package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/rlm"
)

// RLMTool runs a guarded recursive task decomposition: a planner decides
// solve vs decompose, subtasks run as sub-agents concurrently, and a
// synthesizer merges their answers. The result includes the final answer
// and a tree summary of the decomposition.
type RLMTool struct {
	taskTool *TaskTool
}

// NewRLMTool creates the rlm tool; the task tool provides sub-agent execution.
func NewRLMTool(taskTool *TaskTool) *RLMTool {
	return &RLMTool{taskTool: taskTool}
}

func (t *RLMTool) Name() string   { return "rlm" }
func (t *RLMTool) ReadOnly() bool { return false }
func (t *RLMTool) Description() string {
	return "Recursive task decomposition: break a complex task into subtasks, solve them in parallel sub-agents, and merge the results. Unlike task (one sub-agent) or fleet (a fixed batch you list), rlm decides the decomposition itself and can nest. Use for broad analysis or multi-part work where a single pass would lose depth. Guards (depth/node budget/cycle detection) keep recursion bounded."
}
func (t *RLMTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"The task to decompose and solve"},"mode":{"type":"string","enum":["auto","decompose","solve"],"description":"auto: planner decides; decompose: force splitting; solve: force direct"},"max_depth":{"type":"integer","description":"Optional recursion depth cap (default 3)"},"max_nodes":{"type":"integer","description":"Optional total node budget (default 8)"}},"required":["prompt"],"additionalProperties":false}`)
}

// Execute runs one decomposition tree and returns the merged answer plus a
// tree summary.
func (t *RLMTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Prompt   string `json:"prompt"`
		Mode     string `json:"mode"`
		MaxDepth int    `json:"max_depth"`
		MaxNodes int    `json:"max_nodes"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	mode := rlm.ModeAuto
	switch p.Mode {
	case "", "auto":
	case "decompose":
		mode = rlm.ModeDecompose
	case "solve":
		mode = rlm.ModeSolve
	default:
		return "", fmt.Errorf("invalid mode %q", p.Mode)
	}
	// Clamp the exposed guards so a model cannot spawn an unbounded tree.
	if p.MaxDepth > 8 {
		p.MaxDepth = 8
	}
	if p.MaxNodes > 32 {
		p.MaxNodes = 32
	}
	opts := rlm.Options{
		Mode:        mode,
		MaxDepth:    p.MaxDepth,
		MaxNodes:    p.MaxNodes,
		Planner:     rlmPlanner{t: t},
		Solver:      rlmSolver{t: t},
		Synthesizer: rlmSynthesizer{t: t},
	}
	engine, err := rlm.New(opts)
	if err != nil {
		return "", err
	}
	res, err := engine.Run(ctx, rlm.Task{ID: "root", Text: strings.TrimSpace(p.Prompt)})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(res.Output)
	b.WriteString("\n\nDecomposition tree:\n")
	writeTree(&b, res, "", true)
	return b.String(), nil
}

// rlmPlanner executes the planner node as a short read-only sub-agent and
// parses its JSON decision.
type rlmPlanner struct{ t *RLMTool }

func (p rlmPlanner) Decide(ctx context.Context, task rlm.Task, pc rlm.PlanContext) (rlm.Decision, error) {
	out, err := p.t.runNodeAgent(ctx, "rlm-planner", rlm.PlannerPrompt(task, pc), task.Text, true)
	if err != nil {
		return rlm.Decision{}, err
	}
	return parsePlannerDecision(out)
}

// rlmSolver executes one task as an ordinary sub-agent.
type rlmSolver struct{ t *RLMTool }

func (s rlmSolver) Solve(ctx context.Context, task rlm.Task) (string, error) {
	return s.t.runNodeAgent(ctx, "task", "", task.Text, false)
}

// rlmSynthesizer merges child outputs as a short read-only sub-agent.
type rlmSynthesizer struct{ t *RLMTool }

func (s rlmSynthesizer) Synthesize(ctx context.Context, task rlm.Task, children []rlm.ChildResult) (string, error) {
	return s.t.runNodeAgent(ctx, "rlm-synthesizer", rlm.SynthesizePrompt(task, children), task.Text, true)
}

// runNodeAgent executes one engine node through the task tool's sub-agent
// machinery. systemPrompt overrides the child system when non-empty; short
// nodes (planner/synthesizer) run read-only with a single step.
func (t *RLMTool) runNodeAgent(ctx context.Context, kind, systemPrompt, prompt string, readOnly bool) (string, error) {
	spec := ProfileExecSpec{
		Kind:         "task",
		Name:         "task",
		Prompt:       prompt,
		ReadOnly:     readOnly,
		AllowNoTools: true,
		Nested:       SubagentDepth(ctx) > 0,
	}
	if systemPrompt != "" {
		spec.Kind = "rlm"
		spec.Name = kind
		spec.SystemPrompt = systemPrompt
		spec.UseProfilePrompt = true
		spec.MaxSteps = 1
	}
	return t.taskTool.RunProfileSpec(ctx, spec)
}

// parsePlannerDecision extracts the JSON decision from the planner output,
// tolerating markdown fences or surrounding prose.
func parsePlannerDecision(out string) (rlm.Decision, error) {
	var dec rlm.Decision
	start := strings.IndexByte(out, '{')
	end := strings.LastIndexByte(out, '}')
	if start < 0 || end <= start {
		return dec, fmt.Errorf("planner returned no JSON object: %.120q", out)
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &dec); err != nil {
		return dec, fmt.Errorf("planner JSON invalid: %w", err)
	}
	if dec.Action != "solve" && dec.Action != "decompose" {
		return dec, fmt.Errorf("planner action = %q, want solve|decompose", dec.Action)
	}
	return dec, nil
}

// writeTree renders the result tree with statuses and reasons.
func writeTree(b *strings.Builder, r rlm.Result, prefix string, last bool) {
	marker := "├─"
	childPrefix := prefix + "│  "
	if last {
		marker = "└─"
		childPrefix = prefix + "   "
	}
	fmt.Fprintf(b, "%s%s[%s] %s", prefix, marker, r.Status, r.Task.Text)
	if r.Reason != "" {
		fmt.Fprintf(b, " (%s)", r.Reason)
	}
	b.WriteString("\n")
	for i, c := range r.Children {
		writeTree(b, c, childPrefix, i == len(r.Children)-1)
	}
}
