package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

type noticeSink struct {
	notices []string
}

func (s *noticeSink) Emit(e event.Event) {
	if e.Kind == event.Notice {
		s.notices = append(s.notices, e.Text)
	}
}

func overflowTestSession() *Session {
	// Small context so the pressure preflight does not fire first; the mock
	// provider's overflow error simulates an estimate underestimate. The
	// overflow compaction itself is exercised in overflowRecover directly.
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("y", 2000)})
	return s
}

// TestRunOverflowErrorCompactsAndRetries proves a context-overflow provider
// error triggers one forced compaction (whose summarizer consumes one turn)
// and the same turn is retried, instead of failing the run.
func TestRunOverflowErrorCompactsAndRetries(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ChunkError: errors.New("request exceeds the context window")},
		testutil.Turn{Text: "done"},
	)
	sink := &noticeSink{}
	a := New(mp, tool.NewRegistry(), overflowTestSession(), Options{ContextWindow: 4096}, sink)
	if err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if mp.CallCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (overflow + retry)", mp.CallCount())
	}
	found := false
	for _, n := range sink.notices {
		if strings.Contains(n, "Context overflow") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no overflow recovery notice, got %v", sink.notices)
	}
}

// TestOverflowRecoverForcesCompaction proves overflowRecover itself drives a
// real compaction (trigger=overflow) when the estimate clears the threshold,
// and only then reports the turn as retryable.
func TestOverflowRecoverForcesCompaction(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{Text: "summary of the folded history"}, // summarizer call
	)
	sink := &noticeSink{}
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: strings.Repeat("y", 15000)})
	a := New(mp, tool.NewRegistry(), s, Options{ContextWindow: 4096}, sink)
	state := &runLoopState{}
	if ok := a.overflowRecover(context.Background(), state, errors.New("request exceeds the context window")); !ok {
		t.Fatal("overflowRecover should allow the turn to retry")
	}
	if mp.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 (summarizer)", mp.CallCount())
	}
	if !state.overflowCompactionDone {
		t.Fatal("overflowCompactionDone must be set after one recovery")
	}
	found := false
	for _, n := range sink.notices {
		if strings.Contains(n, "Context overflow") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no overflow recovery notice, got %v", sink.notices)
	}
}

// TestRunNonOverflowErrorDoesNotRecover proves lookalike transient errors
// (rate limit) are not routed to compaction and the run fails as before.
func TestRunNonOverflowErrorDoesNotRecover(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ChunkError: errors.New("rate limit exceeded, retry later")},
		testutil.Turn{Text: "done"},
	)
	a := New(mp, tool.NewRegistry(), overflowTestSession(), Options{ContextWindow: 4096}, &noticeSink{})
	if err := a.Run(context.Background(), "go"); err == nil {
		t.Fatal("Run should fail on a non-overflow stream error")
	}
	if mp.CallCount() != 1 {
		t.Fatalf("provider calls = %d, want 1 (no recovery)", mp.CallCount())
	}
}

// TestRunOverflowRecoveryIsBounded proves compact-and-retry happens at most
// once per turn: a second overflow fails the run instead of looping.
func TestRunOverflowRecoveryIsBounded(t *testing.T) {
	mp := testutil.NewMock("m",
		testutil.Turn{ChunkError: errors.New("request exceeds the context window")},
		testutil.Turn{ChunkError: errors.New("request exceeds the context window")},
		testutil.Turn{Text: "done"}, // must never be consumed
	)
	a := New(mp, tool.NewRegistry(), overflowTestSession(), Options{ContextWindow: 4096}, &noticeSink{})
	if err := a.Run(context.Background(), "go"); err == nil {
		t.Fatal("Run should fail after the bounded single overflow retry")
	}
	if mp.CallCount() != 2 {
		t.Fatalf("provider calls = %d, want 2 (overflow + one retry)", mp.CallCount())
	}
}
