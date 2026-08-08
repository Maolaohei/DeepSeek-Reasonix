package peer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddressForStableAndDistinct(t *testing.T) {
	a1 := AddressFor("/work/repo", "sess-1")
	a2 := AddressFor("/work/repo", "sess-1")
	a3 := AddressFor("/work/repo", "sess-2")
	a4 := AddressFor("/work/other", "sess-1")
	if a1 != a2 {
		t.Fatalf("address not stable across calls: %s vs %s", a1, a2)
	}
	if a1 == a3 || a1 == a4 {
		t.Fatalf("addresses collide: %s %s %s", a1, a3, a4)
	}
	if len(a1) != 12 {
		t.Fatalf("address length = %d, want 12", len(a1))
	}
}

func TestDepositDrainRoundTrip(t *testing.T) {
	root := t.TempDir()
	mb, err := NewMailbox(root, "abc")
	if err != nil {
		t.Fatal(err)
	}
	l := Letter{ID: "l1", Sender: "def", SenderName: "peer-b", Text: "hello", CreatedAt: 1}
	if err := mb.Deposit(l); err != nil {
		t.Fatal(err)
	}
	if mb.PendingCount() != 1 {
		t.Fatalf("pending = %d, want 1", mb.PendingCount())
	}
	letters, err := mb.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(letters) != 1 || letters[0].Text != "hello" || letters[0].Sender != "def" {
		t.Fatalf("drained = %+v", letters)
	}
	if mb.PendingCount() != 0 {
		t.Fatal("drain must empty the inbox")
	}
	// Second drain is empty.
	letters, err = mb.Drain()
	if err != nil || len(letters) != 0 {
		t.Fatalf("second drain = %v, %v", letters, err)
	}
}

func TestDepositIsAtomic(t *testing.T) {
	root := t.TempDir()
	mb, err := NewMailbox(root, "abc")
	if err != nil {
		t.Fatal(err)
	}
	// No .tmp file may ever be visible as a letter.
	if err := mb.Deposit(Letter{ID: "l2", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(mb.Dir())
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("stray .tmp file left: %s", e.Name())
		}
	}
}

func TestDrainDropsMalformedWithoutRedelivery(t *testing.T) {
	root := t.TempDir()
	mb, err := NewMailbox(root, "abc")
	if err != nil {
		t.Fatal(err)
	}
	// A malformed letter file must be removed, not retried forever.
	if err := os.WriteFile(filepath.Join(mb.Dir(), "bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	letters, err := mb.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if len(letters) != 0 {
		t.Fatalf("malformed letter surfaced: %+v", letters)
	}
	if mb.PendingCount() != 0 {
		t.Fatal("malformed letter must not be redelivered")
	}
}

func TestDepositRejectsOversizedLetter(t *testing.T) {
	root := t.TempDir()
	mb, _ := NewMailbox(root, "abc")
	big := Letter{ID: "big", Text: strings.Repeat("x", MaxLetterBytes)}
	if err := mb.Deposit(big); err == nil {
		t.Fatal("oversized letter must be rejected")
	}
	if mb.PendingCount() != 0 {
		t.Fatal("rejected letter must not land in the inbox")
	}
}

func TestAwaitReceipt(t *testing.T) {
	root := t.TempDir()
	mb, _ := NewMailbox(root, "abc")
	if err := mb.Deposit(Letter{ID: "r1", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	// Receipt resolves once the letter is drained.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = mb.Drain()
	}()
	if !mb.AwaitReceipt("r1", time.Second) {
		t.Fatal("receipt not observed after drain")
	}
	// Missing letters resolve immediately (already delivered).
	if !mb.AwaitReceipt("r1", time.Second) {
		t.Fatal("drained letter must report delivered")
	}
	// Never-delivered letters time out.
	if err := mb.Deposit(Letter{ID: "r2", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if mb.AwaitReceipt("r2", 100*time.Millisecond) {
		t.Fatal("undrained letter must time out, not report delivered")
	}
}

func TestLetterJSONShape(t *testing.T) {
	raw, err := json.Marshal(Letter{ID: "a", Sender: "b", Text: "c", CreatedAt: 7})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "sender", "text", "createdAt"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("letter JSON missing %q: %s", k, raw)
		}
	}
}
