package rlm

import (
	"strings"
)

// normalizeTask canonicalizes a task text for lineage cycle detection:
// lowercased, whitespace-collapsed, trimmed. "Fix the bug in parser.go" and
// "fix the bug in parser.go " are the same task.
func normalizeTask(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// forcedSolveReason returns why a node must be solved directly instead of
// decomposed: a depth cap, an exhausted node budget, or a lineage cycle
// (the normalized task already appears among its ancestors).
func forcedSolveReason(task string, lineage []string, depth, maxDepth int, st *runState) string {
	norm := normalizeTask(task)
	for _, anc := range lineage {
		if anc == norm {
			return "cycle detected in task lineage"
		}
	}
	if depth >= maxDepth {
		return "depth limit reached"
	}
	if st.nodes.Load() >= st.budget {
		return "node budget exhausted"
	}
	if st.budget-st.nodes.Load() < 2 {
		return "remaining node budget too small to decompose"
	}
	return ""
}

// sanitizeSubtasks drops empty texts, the parent task itself (normalized
// equality), and duplicates, preserving order (pi-rlm engine.ts:489-507).
func sanitizeSubtasks(subtasks []string, parent string) []string {
	parentNorm := normalizeTask(parent)
	seen := make(map[string]bool, len(subtasks))
	out := make([]string, 0, len(subtasks))
	for _, s := range subtasks {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		norm := normalizeTask(trimmed)
		if norm == parentNorm {
			continue
		}
		if seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, trimmed)
	}
	return out
}

// truncateSubtasks caps the decomposed set at maxBranching and the remaining
// node budget (each child consumes at least one node).
func truncateSubtasks(subtasks []string, maxBranching, remainingBudget int) []string {
	limit := maxBranching
	if remainingBudget < limit {
		limit = remainingBudget
	}
	if limit <= 0 {
		return nil
	}
	if len(subtasks) <= limit {
		return subtasks
	}
	return subtasks[:limit]
}
