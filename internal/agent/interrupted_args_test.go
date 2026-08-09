package agent

import (
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestRecordInterruptedDisplayKeepsStreamedArguments pins L2-a: a tool call
// whose arguments streamed to completion before the turn was cut keeps them
// (so the recovery context can re-issue the exact call), while a truncated
// argument blob is dropped instead of leaking partial JSON.
func TestRecordInterruptedDisplayKeepsStreamedArguments(t *testing.T) {
	a := New(&fakeProvider{reply: "x"}, tool.NewRegistry(), NewSession("sys"), Options{}, event.Discard)
	a.recordInterruptedDisplay("partial text", "partial reasoning",
		[]provider.ToolCall{
			{ID: "c1", Name: "write_file", Arguments: `{"path":"a.txt","content":"ok"}`},
			{ID: "c2", Name: "bash", Arguments: `{"command":"go test ./...`}, // truncated
		}, true, 90050)

	msgs := a.Session().Messages
	if len(msgs) == 0 {
		t.Fatal("no interrupted record persisted")
	}
	m := msgs[len(msgs)-1]
	if !m.LocalOnly || m.InterruptedTurn == nil || !m.InterruptedTurn.Pending {
		t.Fatalf("record not a pending interrupted turn: %+v", m)
	}
	if len(m.ToolCalls) != 2 {
		t.Fatalf("display calls = %d, want 2", len(m.ToolCalls))
	}
	if got := m.ToolCalls[0].Arguments; got != `{"path":"a.txt","content":"ok"}` {
		t.Fatalf("complete args dropped: %q", got)
	}
	if got := m.ToolCalls[1].Arguments; got != "" {
		t.Fatalf("truncated args leaked: %q", got)
	}
	got := m.InterruptedTurn.InterruptedTools
	want := []string{"write_file", "bash"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("interrupted tools = %v, want %v", got, want)
	}
	if gotArgs := m.InterruptedTurn.InterruptedToolArgs; len(gotArgs) != 2 || gotArgs[0] != `{"path":"a.txt","content":"ok"}` || gotArgs[1] != "" {
		t.Fatalf("interrupted tool args = %v, want aligned [args, empty]", gotArgs)
	}
}

// TestRecoveryBlockRendersInterruptedArgs pins the recovery user block: an
// interrupted tool with preserved arguments shows them clipped so the model
// can re-issue the exact call; absent args keep the historical one-line form.
func TestRecoveryBlockRendersInterruptedArgs(t *testing.T) {
	block := interruptedRecoveryBlock(&provider.InterruptedTurnRecovery{
		Pending:          true,
		InterruptedTools: []string{"bash"},
		InterruptedToolArgs: []string{
			`{"command":"go test ./internal/agent/ -count=1","timeout":600}`,
		},
	})
	for _, want := range []string{"interrupted_tools: bash", "args={", "&#34;command&#34;:&#34;go test"} {
		if !strings.Contains(block, want) {
			t.Fatalf("recovery block missing %q:\n%s", want, block)
		}
	}

	plain := interruptedRecoveryBlock(&provider.InterruptedTurnRecovery{
		Pending:          true,
		InterruptedTools: []string{"bash"},
	})
	if !strings.Contains(plain, "interrupted_tools: bash") || strings.Contains(plain, " args=") {
		t.Fatalf("plain recovery block changed form:\n%s", plain)
	}
}
