package agent

import (
	"fmt"
	"strings"

	"reasonix/internal/event"
)

// scopeEstimationTag names the transient user-turn block carrying the E3-style
// execution-scope estimate (arXiv 2607.13034): judge difficulty first, read
// the minimum, expand only on verification failure.
const scopeEstimationTag = "scope-estimate"

// scopeEstimationBlock is constant text so the provider-visible prefix stays
// byte-stable for the same input; it is stripped from displays like every
// other transient block (preview.go TransientUserBlockTags).
const scopeEstimationBlock = `<scope-estimate>
Before acting, estimate this task's execution scope: single-file change, cross-file change, or repository-wide task. Read the minimum set of files the task needs; do not re-read files already seen in this conversation; expand scope only when verification fails or the minimum path proves insufficient.
</scope-estimate>`

// maybeInjectScopeEstimate resets the per-turn read counter and prepends the
// E3 estimation block unless the turn already carries one (idempotent).
func (a *Agent) maybeInjectScopeEstimate(input string) string {
	a.scopeReadCalls.Store(0)
	if a.disableScopeEstimation {
		return input
	}
	if strings.Contains(input, "<"+scopeEstimationTag+">") {
		return input
	}
	return scopeEstimationBlock + "\n" + input
}

// scopeReadBudgetThreshold is the per-turn read-call count beyond which every
// read-tool result carries the accumulated scope hint. The hint makes how much
// the agent has already read visible so it can converge, or expand deliberately
// instead of drifting (E3 Expand).
const scopeReadBudgetThreshold = 15

// plannerScopeEstimatePart appends the E3 execution-scope instruction to the
// planner prompt (coordinator.go DefaultPlannerPrompt) without inflating that
// file's size debt.
const plannerScopeEstimatePart = `

Before researching, estimate the task's execution scope and reflect it in the
plan: single-file change, cross-file change, or repository-wide task. Keep
research targeted to the minimum set of files the plan actually needs; widen
the set only when verification or evidence demands it. A one-line fix does not
warrant a repository audit.`

var scopeReadBudgetHint = "\n\n[scope] %d read calls this turn. If the task is not located yet, expand scope deliberately (state the new scope) or reconsider the approach."

// scopeReadToolPrefixes recognizes the read-only discovery tools whose calls
// count toward the turn's read budget. Prefix match keeps the list stable as
// builtin read/grep/glob/ls tools evolve.
var scopeReadToolPrefixes = []string{"read", "glob", "grep", "ls", "list"}

func isScopeReadTool(name string) bool {
	for _, p := range scopeReadToolPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// scopeReadHint returns the budget hint for a read tool that pushed the turn
// over the threshold, or "" when the turn is still within budget.
func (a *Agent) scopeReadHint() string {
	if n := a.scopeReadCalls.Load(); n > scopeReadBudgetThreshold {
		return fmt.Sprintf(scopeReadBudgetHint, n)
	}
	return ""
}

// maybeScopeHint counts one read-only discovery call toward the turn's scope
// budget and appends the accumulated hint to its result once the threshold is
// crossed, so the model converges or expands deliberately (E3 Expand).
func (a *Agent) maybeScopeHint(results []string, i int, name, errMsg string) {
	if a.disableScopeEstimation || errMsg != "" || !isScopeReadTool(name) {
		return
	}
	a.scopeReadCalls.Add(1)
	if hint := a.scopeReadHint(); hint != "" {
		results[i] += hint
	}
}

// emitScopeNotice surfaces the turn's actual read-call count for ACRR-style
// telemetry (E3 P2); trajectories can compare actual vs estimated effort.
func (a *Agent) emitScopeNotice() {
	if a.disableScopeEstimation {
		return
	}
	if n := a.scopeReadCalls.Load(); n > 0 {
		a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
			Code: event.NoticeCodeScope, Text: fmt.Sprintf("scope: %d read call(s) this turn", n)})
	}
}
