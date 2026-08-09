package agent

import (
	"encoding/json"
	"strings"

	"reasonix/internal/provider"
)

// recordInterruptedDisplay persists one bounded LocalOnly recovery record for
// a turn that stopped before producing a clean final answer (exhausted stream
// retries, a non-retryable error, or a host deadline). The next real user
// message folds it into the recovery prompt.
func (a *Agent) recordInterruptedDisplay(text, reasoning string, calls []provider.ToolCall, pending bool, workDurationMs int64) {
	displayCalls := make([]provider.ToolCall, 0, len(calls))
	interrupted := make([]string, 0, len(calls))
	interruptedArgs := make([]string, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		key := call.ID + "\x00" + name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		displayArgs := validStreamedArgs(call.Arguments)
		displayCalls = append(displayCalls, provider.ToolCall{ID: call.ID, Name: name, Arguments: displayArgs})
		if name != "" {
			interrupted = append(interrupted, name)
			interruptedArgs = append(interruptedArgs, displayArgs)
		}
	}
	a.session.Add(provider.Message{
		Role:             provider.RoleTool,
		Content:          text,
		ReasoningContent: reasoning,
		ToolCalls:        displayCalls,
		ToolCallID:       provider.LocalOnlyToolID,
		Name:             provider.LocalOnlyToolName,
		WorkDurationMs:   workDurationMs,
		LocalOnly:        true,
		InterruptedTurn: &provider.InterruptedTurnRecovery{
			Pending:                 pending,
			InterruptedTools:        interrupted,
			InterruptedToolArgs:     interruptedArgs,
			DroppedPartialText:      strings.TrimSpace(text) != "",
			DroppedPartialReasoning: strings.TrimSpace(reasoning) != "",
		},
	})
}

// validStreamedArgs returns the tool-call arguments only when they streamed
// to completion; a truncated JSON blob is dropped rather than replayed.
func validStreamedArgs(args string) string {
	if json.Valid([]byte(args)) {
		return args
	}
	return ""
}
