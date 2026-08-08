package bot

import (
	"testing"
	"time"
)

func msgWith(text, mid string) InboundMessage {
	return InboundMessage{
		Platform:     PlatformFeishu,
		ConnectionID: "c",
		ChatType:     ChatDM,
		ChatID:       "chat",
		UserID:       "u",
		Text:         text,
		MessageID:    mid,
	}
}

// TestInboundGuardDeduplicatesRetryLoops proves a repeated text with a
// different message id is rejected within the dedupe window, while the same
// id (redelivery) passes idempotently. Empty ids are rejected like retries
// because redelivery cannot be proven.
func TestInboundGuardDeduplicatesRetryLoops(t *testing.T) {
	sm := NewSessionManager(0)
	key := "k"
	now := time.Now()
	if sm.inboundGuarded(key, msgWith("please retry", "m1"), now) {
		t.Fatal("first message must pass")
	}
	if !sm.inboundGuarded(key, msgWith("please retry", "m2"), now) {
		t.Fatal("retry loop with a new id must be rejected")
	}
	if sm.inboundGuarded(key, msgWith("please retry", "m1"), now) {
		t.Fatal("same-id redelivery must pass idempotently")
	}
	if !sm.inboundGuarded(key, msgWith("please retry", ""), now) {
		t.Fatal("empty-id repeat must be rejected like a retry")
	}
	if sm.inboundGuarded(key, msgWith("fresh text", ""), now) {
		t.Fatal("different text must pass")
	}
}

// TestInboundGuardRateLimitsFloods proves more than ratePerWindow messages in
// rateWindow are rejected, and the window slides (old entries expire).
func TestInboundGuardRateLimitsFloods(t *testing.T) {
	sm := NewSessionManager(0)
	key := "k"
	now := time.Now()
	for i := 0; i < ratePerWindow; i++ {
		if sm.inboundGuarded(key, msgWith(string(rune('a'+i)), ""), now) {
			t.Fatalf("message %d must pass under the limit", i)
		}
	}
	if !sm.inboundGuarded(key, msgWith("over", ""), now) {
		t.Fatal("flood beyond the window limit must be rejected")
	}
	// After the window slides, messages pass again.
	if sm.inboundGuarded(key, msgWith("after", ""), now.Add(rateWindow+time.Second)) {
		t.Fatal("expired window entries must not block new messages")
	}
}

// TestInboundGuardAllowsBypassCommands proves slash bypasses are never
// structurally rejected.
func TestInboundGuardAllowsBypassCommands(t *testing.T) {
	sm := NewSessionManager(0)
	key := "k"
	cmd := msgWith("/status", "s1")
	if sm.inboundGuarded(key, cmd, time.Now()) {
		t.Fatal("bypass command must pass")
	}
	if sm.inboundGuarded(key, msgWith("/status", "s2"), time.Now()) {
		t.Fatal("repeated bypass command must still pass")
	}
}

// TestTryAcquireWithQueueRejectsDeduplicatedMessage proves the guard is wired
// into the queue entry point end to end.
func TestTryAcquireWithQueueRejectsDeduplicatedMessage(t *testing.T) {
	sm := NewSessionManager(0)
	key := "k"
	first := sm.TryAcquireWithQueue(key, msgWith("run", "1"), QueueOptions{Mode: QueueModeFollowup})
	if !first.Acquired {
		t.Fatalf("first acquire = %+v, want acquired", first)
	}
	dup := sm.TryAcquireWithQueue(key, msgWith("run", "2"), QueueOptions{Mode: QueueModeFollowup})
	if !dup.Rejected {
		t.Fatalf("duplicate acquire = %+v, want rejected", dup)
	}
}
