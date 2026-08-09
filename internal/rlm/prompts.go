package rlm

import (
	"fmt"
	"strings"
)

// PlannerPrompt builds the planner system prompt. The planner must return
// ONLY a JSON object; the schema and the guard context are spelled out so a
// misstep is a format error, not a silent deviation (pi-rlm prompts.ts:10-29).
func PlannerPrompt(task Task, pc PlanContext) string {
	var b strings.Builder
	b.WriteString("You are the planner node of a recursive task-decomposition engine. Decide whether the task should be solved directly or decomposed into subtasks.\n\n")
	fmt.Fprintf(&b, "Task: %s\n", task.Text)
	fmt.Fprintf(&b, "Depth: %d/%d\n", pc.Depth+1, pc.MaxDepth)
	fmt.Fprintf(&b, "Remaining node budget: %d\n", pc.RemainingNodes)
	if len(pc.Lineage) > 0 {
		fmt.Fprintf(&b, "Ancestor tasks (do not repeat any of them): %s\n", strings.Join(pc.Lineage, " | "))
	}
	if pc.Mode == ModeDecompose {
		b.WriteString("Mode: decompose — you MUST decompose into 2-4 subtasks.\n")
	} else {
		b.WriteString("Mode: auto — decompose only when the task genuinely benefits; otherwise solve directly.\n")
	}
	b.WriteString("\nReturn ONLY a JSON object with this schema:\n")
	b.WriteString(`{"action":"solve"|"decompose","subtasks":["subtask 1","subtask 2"],"reason":"short justification"}`)
	b.WriteString("\nDo not call any tools; answer directly. Do not include markdown or prose outside the JSON.")
	return b.String()
}

// MaxChildOutputBytes truncates each child output fed to the synthesizer so a
// deep tree cannot grow the prompt exponentially.
const MaxChildOutputBytes = 2000

// SynthesizePrompt builds the synthesizer prompt. Completed children are
// evidence; failed or cancelled children mean missing work, never inferred
// answers (pi-rlm prompts.ts:70-85).
func SynthesizePrompt(task Task, children []ChildResult) string {
	var b strings.Builder
	b.WriteString("You are the synthesizer node of a recursive task-decomposition engine. Merge the child outputs into the final answer for the parent task.\n\n")
	fmt.Fprintf(&b, "Parent task: %s\n", task.Text)
	b.WriteString("\nChild outputs:\n")
	for _, c := range children {
		fmt.Fprintf(&b, "- [%s] %s", c.Status, c.Task.Text)
		if c.Reason != "" {
			fmt.Fprintf(&b, " (%s)", c.Reason)
		}
		out := c.Output
		if len(out) > MaxChildOutputBytes {
			out = out[:MaxChildOutputBytes] + "…[truncated]"
		}
		if out != "" {
			fmt.Fprintf(&b, "\n  %s", out)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nRules: COMPLETED children are evidence and must be reflected in the answer. FAILED or CANCELLED children indicate missing work — do not infer their answers. Synthesize best-effort and state explicitly which parts are missing. Return ONLY the final answer text.")
	return b.String()
}
