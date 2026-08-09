package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
)

// TestEnsureTabSessionLeaseForRebuildSurvivesTransientHolder reproduces the
// startup "this session is already open in another Reasonix window" false
// positive: a transient lease holder — CleanupStaleRunning probing a running
// subagent's parent session during a concurrent controller build — holds the
// session lease for a few milliseconds while the tab's own startup bind runs.
// The bind must retry against the genuinely-free lease instead of surfacing a
// spurious ErrSessionLeaseHeld.
func TestEnsureTabSessionLeaseForRebuildSurvivesTransientHolder(t *testing.T) {
	isolateDesktopUserDirs(t)
	dir := config.SessionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	path := filepath.Join(dir, "contended-session.jsonl")

	tab := &WorkspaceTab{ID: "tab", Scope: "global", Ready: true, SessionPath: path}
	app := &App{
		tabs:     map[string]*WorkspaceTab{tab.ID: tab},
		tabOrder: []string{tab.ID},
	}
	t.Cleanup(tab.releaseSessionLease)

	// Simulate CleanupStaleRunning's transient parent-session lease probe:
	// acquire, hold briefly, then release. The probe targets the same runtime
	// key the tab's bind uses (case-folded on Windows), so the contention is
	// real on every platform.
	key := sessionRuntimeKey(path)
	acquired := make(chan struct{})
	releaseProbe := make(chan struct{})
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		lease, err := agent.TryAcquireSessionLease(key)
		if err != nil {
			t.Errorf("probe lease acquire: %v", err)
			close(acquired)
			return
		}
		close(acquired)
		<-releaseProbe
		lease.Release()
	}()

	<-acquired // the probe now holds the lease

	bindErr := make(chan error, 1)
	go func() {
		bindErr <- app.ensureTabSessionLeaseForRebuild(tab, path, "")
	}()

	// Give the first bind attempt time to fail against the held lease, then
	// release the probe: the bind must succeed on a later attempt.
	time.Sleep(50 * time.Millisecond)
	close(releaseProbe)

	select {
	case err := <-bindErr:
		if err != nil {
			t.Fatalf("startup bind failed against a transient holder: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup bind did not complete after the transient holder released")
	}
	<-probeDone

	if key := tab.sessionLeaseRuntimeKey(); key != sessionRuntimeKey(path) {
		t.Fatalf("tab lease key = %q, want %q", key, sessionRuntimeKey(path))
	}
}

// TestSessionLeaseBusyErrorCarriesHolderIdentity pins that the user-facing
// "already open" error renders who holds the session (hostname/writer id,
// pid, acquisition time) so the user can decide whether a real window or a
// leftover process owns it, instead of a bare generic notice.
func TestSessionLeaseBusyErrorCarriesHolderIdentity(t *testing.T) {
	when := time.Date(2026, 8, 9, 10, 30, 0, 0, time.UTC)
	info := &agent.SessionLeaseInfo{
		WriterID:   "writer-abc123",
		PID:        4242,
		Hostname:   "desk-pc",
		AcquiredAt: when,
	}
	err := &sessionLeaseBusyError{err: &agent.SessionLeaseError{Path: "/s/x.jsonl", Info: info}}
	got := err.Error()
	for _, want := range []string{
		"already open in another Reasonix window",
		"held by desk-pc/writer-abc123",
		"pid 4242",
		"2026-08-09 10:30:00",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("error missing %q: %s", want, got)
		}
	}

	// Setting-bearing variant still names the setting and the holder.
	settingErr := &sessionLeaseBusyError{setting: "model", err: &agent.SessionLeaseError{Path: "/s/x.jsonl", Info: info}}
	if got := settingErr.Error(); !strings.Contains(got, "before changing model") || !strings.Contains(got, "writer-abc123") {
		t.Fatalf("setting variant lost context: %s", got)
	}

	// No holder info (missing/cleared lease metadata): fall back to the
	// generic message unchanged, never a half-rendered holder suffix.
	generic := &sessionLeaseBusyError{err: &agent.SessionLeaseError{Path: "/s/x.jsonl"}}
	if got := generic.Error(); got != "this session is already open in another Reasonix window or still running in the background; close the other window or open a copy" {
		t.Fatalf("generic variant changed: %s", got)
	}
	if got := (&sessionLeaseBusyError{}).Error(); got != "this session is already open in another Reasonix window or still running in the background; close the other window or open a copy" {
		t.Fatalf("empty variant changed: %s", got)
	}
}
