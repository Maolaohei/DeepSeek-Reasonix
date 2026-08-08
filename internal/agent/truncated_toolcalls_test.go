package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// TestSplitLengthTruncatedCallsRejectsIncompleteArguments verifies the pure
// split: only calls whose streamed arguments fail json.Valid are rejected,
// and only when the turn actually hit the output token limit.
func TestSplitLengthTruncatedCallsRejectsIncompleteArguments(t *testing.T) {
	complete := provider.ToolCall{ID: "c1", Name: "echo", Arguments: `{"text":"hello"}`}
	truncated := provider.ToolCall{ID: "c2", Name: "echo", Arguments: `{"text":"trunc`}
	empty := provider.ToolCall{ID: "c3", Name: "echo", Arguments: ""}

	calls := []provider.ToolCall{complete, truncated, empty}

	// No usage or non-length finish: nothing is rejected.
	if got, rejected := splitLengthTruncatedCalls(calls, nil); len(rejected) != 0 || len(got) != 3 {
		t.Fatalf("nil usage: got %d exec, rejected %v", len(got), rejected)
	}
	if got, rejected := splitLengthTruncatedCalls(calls, &provider.Usage{FinishReason: "stop"}); len(rejected) != 0 || len(got) != 3 {
		t.Fatalf("stop finish: got %d exec, rejected %v", len(got), rejected)
	}

	// Length finish: the incomplete call is rejected, others execute.
	exec, rejected := splitLengthTruncatedCalls(calls, &provider.Usage{FinishReason: "length"})
	if len(exec) != 2 {
		t.Fatalf("exec = %d calls, want 2", len(exec))
	}
	if _, ok := rejected[1]; !ok {
		t.Fatalf("index 1 not rejected: %v", rejected)
	}
	if len(rejected) != 1 {
		t.Fatalf("rejected = %v, want only index 1", rejected)
	}
	for _, c := range exec {
		if c.ID == "c2" {
			t.Fatalf("truncated call leaked into exec batch: %+v", c)
		}
	}
	msg := rejected[1]
	if !strings.Contains(msg, "output token limit") {
		t.Fatalf("rejection message lacks truncation explanation: %q", msg)
	}
}

// TestRunRejectsTruncatedToolCallWithoutExecutingIt proves an incomplete call
// from a length-truncated turn is never executed and the model receives an
// explicit truncation notice, then continues with the next turn.
func TestRunRejectsTruncatedToolCallWithoutExecutingIt(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "echo", Arguments: `{"text":"hello"}`},
				{ID: "c2", Name: "echo", Arguments: `{"text":"trunc`},
			},
			Usage: &provider.Usage{FinishReason: "length"},
		},
		testutil.Turn{Text: "done"},
	)
	a := New(mp, echoRegistry(), NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var toolResults []provider.Message
	for _, m := range a.Session().Messages {
		if m.Role == provider.RoleTool {
			toolResults = append(toolResults, m)
		}
	}
	if len(toolResults) != 2 {
		t.Fatalf("tool results = %d, want 2", len(toolResults))
	}
	if got := toolResults[0].Content; !strings.HasPrefix(got, "echoed:") {
		t.Fatalf("complete call result = %q, want echo output", got)
	}
	if got := toolResults[1].Content; !strings.Contains(got, "output token limit") || strings.HasPrefix(got, "echoed:") {
		t.Fatalf("truncated call result = %q, want truncation notice", got)
	}
	if got := toolResults[1].ToolCallID; got != "c2" {
		t.Fatalf("rejected result paired with call %q, want c2", got)
	}
}

// TestRunLengthTruncatedTurnWithOnlyCompleteCallsExecutes verifies a length
// finish does not reject calls whose arguments streamed in full: DeepSeek
// thinking turns routinely hit the output limit after the call completed.
func TestRunLengthTruncatedTurnWithOnlyCompleteCallsExecutes(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "echo", Arguments: `{"text":"full"}`},
			},
			Usage: &provider.Usage{FinishReason: "length"},
		},
		testutil.Turn{Text: "done"},
	)
	a := New(mp, echoRegistry(), NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var toolResults []provider.Message
	for _, m := range a.Session().Messages {
		if m.Role == provider.RoleTool {
			toolResults = append(toolResults, m)
		}
	}
	if len(toolResults) != 1 || !strings.HasPrefix(toolResults[0].Content, "echoed:") {
		t.Fatalf("tool results = %+v, want one echo result", toolResults)
	}
}

// TestRejectedTruncatedCallStillRepairsOnReplay proves the execution-time
// rejection and the replay-time repair are independent layers: the truncated
// call is refused once, yet the replayed history still assembles into valid
// provider messages (SanitizeToolPairing closes the cut-off arguments at the
// adapter boundary).
func TestRejectedTruncatedCallStillRepairsOnReplay(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "echo", Arguments: `{"text":"trunc`},
			},
			Usage: &provider.Usage{FinishReason: "length"},
		},
		testutil.Turn{Text: "done"},
	)
	a := New(mp, echoRegistry(), NewSession(""), Options{}, event.Discard)
	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The rejected tool call must be refused...
	var rejected bool
	for _, m := range a.Session().Messages {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, "output token limit") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("truncated call was not rejected")
	}
	// ...yet the same history still assembles into a valid provider request:
	// the adapter boundary repairs the truncated JSON for replay.
	assembled := false
	for _, m := range provider.SanitizeToolPairing(provider.ModelMessages(a.Session().Messages)) {
		for _, tc := range m.ToolCalls {
			if tc.ID == "c1" && json.Valid([]byte(tc.Arguments)) {
				assembled = true
			}
		}
	}
	if !assembled {
		t.Fatal("replay assembly did not repair the truncated arguments")
	}
}
