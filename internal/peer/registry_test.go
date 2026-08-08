package peer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock(t *testing.T, start time.Time) (*Registry, func(d time.Duration)) {
	t.Helper()
	r, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := start
	r.now = func() time.Time { return now }
	return r, func(d time.Duration) { now = now.Add(d) }
}

func TestPresenceThreeStates(t *testing.T) {
	r, advance := fixedClock(t, time.UnixMilli(1_000_000))
	rec := Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 42, DisplayName: "peer-a"}
	if err := r.RecordSession(rec); err != nil {
		t.Fatal(err)
	}
	alive := func(pid int) bool { return pid == 42 }

	if got := r.Presence("abc", alive); got != Live {
		t.Fatalf("fresh record = %s, want live", got)
	}
	advance(StaleAfter + time.Second)
	if got := r.Presence("abc", alive); got != Stalled {
		t.Fatalf("stale record = %s, want stalled", got)
	}
	advance(-StaleAfter)
	if got := r.Presence("abc", func(int) bool { return false }); got != Offline {
		t.Fatalf("dead pid = %s, want offline", got)
	}
	if got := r.Presence("missing", alive); got != Offline {
		t.Fatalf("unknown addr = %s, want offline", got)
	}
}

func TestHeartbeatRefreshesPresence(t *testing.T) {
	r, advance := fixedClock(t, time.UnixMilli(1_000_000))
	if err := r.RecordSession(Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 7}); err != nil {
		t.Fatal(err)
	}
	alive := func(int) bool { return true }
	advance(StaleAfter + time.Second)
	if got := r.Presence("abc", alive); got != Stalled {
		t.Fatalf("want stalled, got %s", got)
	}
	if err := r.Heartbeat("abc"); err != nil {
		t.Fatal(err)
	}
	if got := r.Presence("abc", alive); got != Live {
		t.Fatalf("after heartbeat = %s, want live", got)
	}
}

func TestSweepKeepsUndeliveredMail(t *testing.T) {
	root := t.TempDir()
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(2_000_000)
	r.now = func() time.Time { return now }
	rec := Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 1, BeatAt: now.Add(-7 * 24 * time.Hour).UnixMilli()}
	if err := r.RecordSession(rec); err != nil {
		t.Fatal(err)
	}
	// Undelivered mail in the inbox: sweep must not touch it.
	mb, err := NewMailbox(root, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Deposit(Letter{ID: "m1", Text: "important"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mb.Dir(), "m1.json")); err != nil {
		t.Fatal("sweep destroyed undelivered mail")
	}
}

func TestSweepRemovesEmptyDeadRecord(t *testing.T) {
	root := t.TempDir()
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	start := time.UnixMilli(3_000_000)
	r.now = func() time.Time { return start }
	if err := r.RecordSession(Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMailbox(root, "abc"); err != nil {
		t.Fatal(err)
	}
	// Age the record beyond SweepGrace; RecordSession refreshes BeatAt.
	r.now = func() time.Time { return start.Add(2 * 24 * time.Hour) }
	if err := r.Sweep(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Load("abc"); !os.IsNotExist(err) {
		t.Fatalf("dead empty record not swept: %v", err)
	}
}

func TestSweepExpiresOldMail(t *testing.T) {
	root := t.TempDir()
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	start := time.UnixMilli(4_000_000)
	r.now = func() time.Time { return start }
	if err := r.RecordSession(Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 1}); err != nil {
		t.Fatal(err)
	}
	mb, err := NewMailbox(root, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := mb.Deposit(Letter{ID: "old", Text: "stale"}); err != nil {
		t.Fatal(err)
	}
	// Age the record beyond MailRetention; RecordSession refreshes BeatAt.
	r.now = func() time.Time { return start.Add(31 * 24 * time.Hour) }
	if err := r.Sweep(); err != nil {
		t.Fatal(err)
	}
	if mb.PendingCount() != 0 {
		t.Fatal("mail older than retention must be swept")
	}
}

func TestMarkOfflineKeepsRecordAndMail(t *testing.T) {
	root := t.TempDir()
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordSession(Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 9}); err != nil {
		t.Fatal(err)
	}
	mb, _ := NewMailbox(root, "abc")
	if err := mb.Deposit(Letter{ID: "q1", Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkOffline("abc"); err != nil {
		t.Fatal(err)
	}
	rec, err := r.Load("abc")
	if err != nil {
		t.Fatalf("record lost after MarkOffline: %v", err)
	}
	if rec.PID != 0 {
		t.Fatalf("pid = %d, want cleared", rec.PID)
	}
	if mb.PendingCount() != 1 {
		t.Fatal("mail must survive MarkOffline")
	}
}

func TestListSortsAndFormats(t *testing.T) {
	root := t.TempDir()
	r, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordSession(Record{Addr: "abc", CWD: "/w", SessionID: "s", PID: 1, DisplayName: "peer-a"}); err != nil {
		t.Fatal(err)
	}
	recs := r.List()
	if len(recs) != 1 || recs[0].DisplayName != "peer-a" {
		t.Fatalf("list = %+v", recs)
	}
}

func TestBoundaryDeclaresNoAuthority(t *testing.T) {
	got := FormatInbound("peer-a", "hello")
	if !strings.Contains(got, "not from the user") || !strings.Contains(got, "carries no authority") {
		t.Fatalf("boundary missing authority disclaimer: %q", got)
	}
	if !strings.Contains(got, "From peer session peer-a") {
		t.Fatalf("sender attribution missing: %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("body missing: %q", got)
	}
	if !strings.Contains(Boundary, "slash command") {
		t.Fatalf("slash-command disclaimer missing: %q", Boundary)
	}
}
