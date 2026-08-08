package agent

import (
	"context"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestClampMaxTokensToContext(t *testing.T) {
	// Small input, explicit budget: untouched.
	if got := clampMaxTokensToContext(8192, 65536, 1000); got != 8192 {
		t.Fatalf("spacious context: got %d, want 8192", got)
	}
	// Near-full context: capped to the headroom.
	if got := clampMaxTokensToContext(8192, 65536, 60000); got != 65536-60000-4096 {
		t.Fatalf("near-full context: got %d, want %d", got, 65536-60000-4096)
	}
	// Zero budget (inherit provider default) is left untouched: clamping
	// could amplify a small inherited budget beyond the provider's cap.
	if got := clampMaxTokensToContext(0, 65536, 60000); got != 0 {
		t.Fatalf("inherited budget: got %d, want 0 untouched", got)
	}
	// Saturated window (no room to answer): left alone, provider truncation
	// and the truncated-tool-call guard handle the turn instead.
	if got := clampMaxTokensToContext(8192, 65536, 65000); got != 8192 {
		t.Fatalf("saturated window: got %d, want 8192 untouched", got)
	}
	// Unknown window: never touched.
	if got := clampMaxTokensToContext(8192, 0, 60000); got != 8192 {
		t.Fatalf("unknown window: got %d, want 8192", got)
	}
}

// TestRunClampsMaxTokensNearFullContext proves the wire request caps its
// output budget when the context is close to the window, and keeps the
// configured budget untouched when there is headroom.
func TestRunClampsMaxTokensNearFullContext(t *testing.T) {
	// ASCII text estimates at one token per rune, so 5000 runes ≈ 5000 input
	// tokens: under the 0.8*16384 preflight trigger (no summarizer turn), but
	// over the clamp threshold (16384-4096-8192 = 4096).
	bigContext := strings.Repeat("hello world ", 450)

	t.Run("near-full context clamps", func(t *testing.T) {
		mp := testutil.NewMock("m", testutil.Turn{Text: "done"})
		s := NewSession("sys")
		s.Add(provider.Message{Role: provider.RoleUser, Content: bigContext})
		a := New(mp, tool.NewRegistry(), s, Options{ContextWindow: 16384, MaxOutputTokens: 8192}, event.Discard)
		if err := a.Run(context.Background(), "go"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		req := mp.LastRequest()
		if req == nil {
			t.Fatal("no request recorded")
		}
		if req.MaxTokens >= 8192 {
			t.Fatalf("max_tokens = %d, want clamped below the configured 8192", req.MaxTokens)
		}
		if req.MaxTokens < 4096 {
			t.Fatalf("max_tokens = %d, clamped too aggressively", req.MaxTokens)
		}
	})

	t.Run("headroom keeps configured budget", func(t *testing.T) {
		mp := testutil.NewMock("m", testutil.Turn{Text: "done"})
		a := New(mp, tool.NewRegistry(), NewSession("sys"), Options{ContextWindow: 16384, MaxOutputTokens: 8192}, event.Discard)
		if err := a.Run(context.Background(), "go"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if req := mp.LastRequest(); req == nil || req.MaxTokens != 8192 {
			t.Fatalf("max_tokens = %+v, want stable 8192", req)
		}
	})

	t.Run("inherited budget stays untouched", func(t *testing.T) {
		mp := testutil.NewMock("m", testutil.Turn{Text: "done"})
		s := NewSession("sys")
		s.Add(provider.Message{Role: provider.RoleUser, Content: bigContext})
		a := New(mp, tool.NewRegistry(), s, Options{ContextWindow: 16384}, event.Discard)
		if err := a.Run(context.Background(), "go"); err != nil {
			t.Fatalf("Run: %v", err)
		}
		req := mp.LastRequest()
		if req == nil || req.MaxTokens != 0 {
			t.Fatalf("max_tokens = %+v, want 0 (inherit provider default untouched)", req)
		}
	})
}
