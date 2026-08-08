package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// argsShapeBroken reports a transient model JSON glitch: undecodable args or a
// field-level decode error, both fixable by echoing the tool schema.
func argsShapeBroken(args string, err error) bool {
	return args != "" && (!json.Valid([]byte(args)) || strings.Contains(err.Error(), "json:"))
}

// pauseRecoveryRound pairs tool-call/tool-result without executing and returns
// the typed pause so the host surfaces recovery_paused, not a send failure.
func (a *Agent) pauseRecoveryRound(ctx context.Context, state *runLoopState, calls []provider.ToolCall, usage *provider.Usage) (bool, error) {
	reason := ""
	if ctrl := a.recoveryEpisodeControl(); ctrl != nil {
		_, _ = ctrl.ConsumeFinalization(a.recoveryTaskID)
	}
	msg := "blocked: Auto recovery already paused this turn. Do not call tools; the user will continue in the next message."
	for _, call := range calls {
		a.session.Add(provider.Message{
			Role:       provider.RoleTool,
			Content:    msg,
			ToolCallID: call.ID,
			Name:       call.Name,
		})
	}
	a.maybeCompact(ctx, usage)
	return false, &RecoveryPauseError{
		Message:    "Automatic retries paused. Reasonix stopped repeated attempts and kept completed work. Send \"continue\" to start a fresh attempt, or add instructions to change direction.",
		StopReason: reason,
	}
}

// splitLengthTruncatedCalls rejects tool calls whose streamed arguments were
// cut off by an output-token-limit truncation: executing the best-effort
// closed JSON would run a shape the model never issued, so it must re-issue.
func splitLengthTruncatedCalls(calls []provider.ToolCall, usage *provider.Usage) (exec []provider.ToolCall, rejected map[int]string) {
	if usage == nil || usage.FinishReason != "length" {
		return calls, nil
	}
	exec = make([]provider.ToolCall, 0, len(calls))
	for i, call := range calls {
		if call.Arguments != "" && !json.Valid([]byte(call.Arguments)) {
			if rejected == nil {
				rejected = make(map[int]string)
			}
			rejected[i] = fmt.Sprintf("blocked: the response hit the output token limit, so the arguments of tool call %q may be truncated. Re-issue the call with complete arguments.", call.Name)
			continue
		}
		exec = append(exec, call)
	}
	return exec, rejected
}

// mergeRejectedToolResults re-aligns batch results with the original call
// order after splitLengthTruncatedCalls removed rejected calls from execution.
func mergeRejectedToolResults(calls []provider.ToolCall, batch batchExecution, rejected map[int]string) ([]string, [][]string, []*tool.ShellExecution) {
	results := make([]string, len(calls))
	images := make([][]string, len(calls))
	executions := make([]*tool.ShellExecution, len(calls))
	j := 0
	for i := range calls {
		if msg, ok := rejected[i]; ok {
			results[i] = msg
			continue
		}
		results[i] = batch.results[j]
		images[i] = batch.images[j]
		if j < len(batch.executions) {
			executions[i] = batch.executions[j]
		}
		j++
	}
	return results, images, executions
}
