package refine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordNoteLoadsPersistsUsage(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule-a", Title: "Rule A", Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNote(PromptNote{ID: "rule-b", Title: "Rule B", Content: "b"}); err != nil {
		t.Fatal(err)
	}

	store.RecordNoteLoads([]string{"rule-a", "rule-b"})
	store.RecordNoteLoads([]string{"rule-a"})

	ua, ok := store.NoteUsage("rule-a")
	if !ok {
		t.Fatal("rule-a usage missing after loads")
	}
	if ua.Loads != 2 {
		t.Fatalf("rule-a loads = %d, want 2", ua.Loads)
	}
	ub, ok := store.NoteUsage("rule-b")
	if !ok || ub.Loads != 1 {
		t.Fatalf("rule-b usage = %+v ok=%v, want 1 load", ub, ok)
	}
	if _, ok := store.NoteUsage("never-loaded"); ok {
		t.Fatal("never-recorded note must not report usage")
	}

	// usage.json exists next to the store and survives a fresh Store value.
	if _, err := os.Stat(filepath.Join(store.Dir, usageFileName)); err != nil {
		t.Fatalf("usage ledger file missing: %v", err)
	}
	fresh := Store{Dir: store.Dir, Scope: ScopeProject}
	ua2, ok := fresh.NoteUsage("rule-a")
	if !ok || ua2.Loads != 2 {
		t.Fatalf("usage did not persist across Store values: %+v ok=%v", ua2, ok)
	}
}

func TestStaleNotesFlagsOnlyRecordedAndIdle(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "active", Title: "Active", Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNote(PromptNote{ID: "idle", Title: "Idle", Content: "i"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNote(PromptNote{ID: "unrecorded", Title: "Unrecorded", Content: "u"}); err != nil {
		t.Fatal(err)
	}

	// "active" loaded just now; "idle" loaded long ago; "unrecorded" never.
	store.RecordNoteLoads([]string{"active", "idle"})
	// Rewind the ledger timestamps: idle's last load predates the horizon.
	usage := store.loadUsage()
	old := time.Now().UTC().Add(-2 * StaleNotesAfter)
	idle := usage["idle"]
	idle.LastLoadedAt = old
	usage["idle"] = idle
	store.saveUsage(usage)

	stale := store.StaleNotes(StaleNotesAfter)
	if len(stale) != 1 {
		t.Fatalf("stale = %+v, want exactly the idle note", stale)
	}
	if stale[0].ID != "idle" {
		t.Fatalf("stale id = %q, want idle", stale[0].ID)
	}

	// A fresh load clears the flag.
	store.RecordNoteLoads([]string{"idle"})
	if got := store.StaleNotes(StaleNotesAfter); len(got) != 0 {
		t.Fatalf("stale after reload = %+v, want none", got)
	}
}

func TestRecordNoteLoadsIgnoresInvalidAndMissing(t *testing.T) {
	store := testStore(t)
	store.RecordNoteLoads([]string{"../escape", "", "valid-but-missing"})
	// No file at all: writes of nothing must not create a ledger entry file
	// (saveUsage only runs when something changed).
	if _, err := os.Stat(filepath.Join(store.Dir, usageFileName)); !os.IsNotExist(err) {
		t.Fatalf("ledger created despite no valid ids: %v", err)
	}
}

func TestApplyProposalRecordsAttribution(t *testing.T) {
	store := testStore(t)
	proposal := Proposal{
		Summary: "learn rule", Rationale: "saw it twice", ExpectedOutcome: "fewer repeats",
		Edits: []Edit{{Action: ActionCreate, Kind: KindPrompt, Title: "New rule", Content: "Do the thing."}},
	}
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	_, err := ApplyProposal(store, proposal, nil, now, Attribution{
		SessionID:   "branch-abc",
		SessionPath: "/sessions/branch-abc.jsonl",
		GoalResult:  "complete; the question is fully answered",
	})
	if err != nil {
		t.Fatal(err)
	}
	events := store.ListRefinements()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.SessionID != "branch-abc" || ev.SessionPath != "/sessions/branch-abc.jsonl" ||
		ev.GoalResult != "complete; the question is fully answered" {
		t.Fatalf("attribution not recorded: %+v", ev)
	}

	// Without attribution the fields stay empty (old-caller compatibility).
	store2 := testStore(t)
	_, err = ApplyProposal(store2, proposal, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if ev := store2.ListRefinements()[0]; ev.SessionID != "" || ev.SessionPath != "" || ev.GoalResult != "" {
		t.Fatalf("zero attribution must stay empty, got %+v", ev)
	}
	// And the event JSON carries the fields for future readers.
	raw, err := os.ReadFile(filepath.Join(store.Dir, refinementsLogName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"session_id":"branch-abc"`) {
		t.Fatalf("session_id missing from event log line: %s", raw)
	}
}
