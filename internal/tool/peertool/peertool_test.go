package peertool

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/peer"
)

func testPeers(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	// Register two peers: "peer-a" (self) and "peer-b".
	reg, err := peer.NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	a := peer.AddressFor("/w", "sess-a")
	b := peer.AddressFor("/w", "sess-b")
	if err := reg.RecordSession(peer.Record{Addr: a, CWD: "/w", SessionID: "sess-a", PID: 1, DisplayName: "peer-a"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordSession(peer.Record{Addr: b, CWD: "/w", SessionID: "sess-b", PID: 2, DisplayName: "peer-b"}); err != nil {
		t.Fatal(err)
	}
	return root, a, b
}

func TestListPeersShowsSessionsAndMarksSelf(t *testing.T) {
	root, a, _ := testPeers(t)
	got, err := (NewListPeersTool(root, a)).Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "peer-a") || !strings.Contains(got, "peer-b") {
		t.Fatalf("list missing peers: %q", got)
	}
	if !strings.Contains(got, "*peer-a") {
		t.Fatalf("self marker missing: %q", got)
	}
	if !strings.Contains(got, "(cwd: /w") {
		t.Fatalf("presence/cwd line missing: %q", got)
	}
}

func TestMessagePeerDeliversAndReceipt(t *testing.T) {
	root, _, b := testPeers(t)
	msg := NewMessagePeerTool(root, "self", "self-name")
	out, err := msg.Execute(context.Background(), json.RawMessage(`{"peer":"peer-b","text":"hi there"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "queued") {
		t.Fatalf("output = %q, want queued for offline peer", out)
	}
	mb, err := peer.NewMailbox(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if mb.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", mb.PendingCount())
	}
	letters, err := mb.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(letters) != 1 || letters[0].Text != "hi there" || letters[0].SenderName != "self-name" {
		t.Fatalf("letter = %+v", letters)
	}
	if letters[0].CreatedAt == 0 {
		t.Fatalf("letter createdAt missing: %+v", letters[0])
	}
}

func TestMessagePeerResolvesByAddressPrefix(t *testing.T) {
	root, a, b := testPeers(t)
	// Self-messaging via the display name is refused (addr matches selfAddr).
	msg := NewMessagePeerTool(root, a, "self")
	out, _ := msg.Execute(context.Background(), json.RawMessage(`{"peer":"peer-a","text":"x"}`))
	if !strings.Contains(out, "yourself") {
		t.Fatalf("self-message not refused: %q", out)
	}
	out, err := msg.Execute(context.Background(), json.RawMessage(`{"peer":"`+b[:6]+`","text":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "queued") {
		t.Fatalf("prefix resolve failed: %q", out)
	}
	// Ambiguous display name is refused.
	reg, _ := peer.NewRegistry(root)
	_ = reg.RecordSession(peer.Record{Addr: peer.AddressFor("/w", "sess-c"), CWD: "/w", SessionID: "sess-c", PID: 3, DisplayName: "peer-b"})
	out, _ = msg.Execute(context.Background(), json.RawMessage(`{"peer":"peer-b","text":"x"}`))
	if !strings.Contains(out, "ambiguous") {
		t.Fatalf("ambiguous display name not refused: %q", out)
	}
	// Unknown peer is refused.
	out, _ = msg.Execute(context.Background(), json.RawMessage(`{"peer":"nobody","text":"x"}`))
	if !strings.Contains(out, "no peer") {
		t.Fatalf("unknown peer not refused: %q", out)
	}
}

func TestMessagePeerRequireAckAgainstLivePeer(t *testing.T) {
	root, _, b := testPeers(t)
	// Make peer-b live by pointing ProcessAlive at it and keeping the beat fresh.
	reg, err := peer.NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Heartbeat(b); err != nil {
		t.Fatal(err)
	}
	orig := ProcessAlive
	ProcessAlive = func(pid int) bool { return pid == 2 }
	defer func() { ProcessAlive = orig }()

	msg := NewMessagePeerTool(root, "self", "self")
	// With ack requested but nobody draining, the timeout path reports queued.
	start := time.Now()
	out, err := msg.Execute(context.Background(), json.RawMessage(`{"peer":"peer-b","text":"ping","requireAck":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ack wait took %v, want ~1.5s timeout", elapsed)
	}
	if !strings.Contains(out, "queued") {
		t.Fatalf("timeout path = %q, want queued notice", out)
	}
}

func TestMessagePeerRejectsOversized(t *testing.T) {
	root, _, _ := testPeers(t)
	msg := NewMessagePeerTool(root, "self", "self")
	out, err := msg.Execute(context.Background(), json.RawMessage(`{"peer":"peer-b","text":"`+strings.Repeat("x", 40000)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "error") {
		t.Fatalf("oversized letter not rejected: %.40q", out)
	}
}

func TestProcessAliveSelf(t *testing.T) {
	if ProcessAlive(-1) {
		t.Fatal("negative pid reported alive")
	}
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("own process reported dead")
	}
}
