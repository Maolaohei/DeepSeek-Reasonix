package boot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/refine"
)

// recordingSink collects emitted events for assertions.
type recordingSink struct{ events []event.Event }

func (s *recordingSink) Emit(ev event.Event) { s.events = append(s.events, ev) }

func TestRecordHarnessLoadsLedgerAndStaleNotice(t *testing.T) {
	userDir := t.TempDir()
	store := refine.Store{Dir: filepath.Join(userDir, "projects", "demo", "harness"), Scope: refine.ScopeProject}
	global := refine.Store{Dir: filepath.Join(userDir, "harness", "global"), Scope: refine.ScopeGlobal}
	if _, err := store.SaveNote(refine.PromptNote{ID: "active", Title: "Active", Content: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNote(refine.PromptNote{ID: "idle", Title: "Idle", Content: "i"}); err != nil {
		t.Fatal(err)
	}
	if _, err := global.SaveNote(refine.PromptNote{ID: "g-note", Title: "Global", Content: "g"}); err != nil {
		t.Fatal(err)
	}

	// First boot: everything loads; nothing is stale yet.
	sink := &recordingSink{}
	recordHarnessLoads(sink, store, global)
	if len(sink.events) != 0 {
		t.Fatalf("first boot emitted notices: %+v", sink.events)
	}
	for _, id := range []string{"active", "idle", "g-note"} {
		u, ok := store.NoteUsage(id)
		if !ok && id != "g-note" {
			t.Fatalf("no usage recorded for %s", id)
		}
		if id == "g-note" {
			u, ok = global.NoteUsage(id)
			if !ok {
				t.Fatalf("no usage recorded for %s", id)
			}
		}
		if u.Loads != 1 {
			t.Fatalf("%s loads = %d, want 1", id, u.Loads)
		}
	}

	// Rewind the ledger so "idle" looks unloaded past the horizon, then boot
	// again: the ledger updates and the stale note surfaces. The horizon is an
	// hour and the rewind two hours back, so the other (just-loaded) notes can
	// never cross it within the test's runtime.
	usagePath := filepath.Join(store.Dir, "usage.json")
	raw, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatal(err)
	}
	var usage map[string]refine.NoteUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		t.Fatal(err)
	}
	idle := usage["idle"]
	idle.LastLoadedAt = time.Now().UTC().Add(-2 * time.Hour)
	usage["idle"] = idle
	rewound, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(usagePath, rewound, 0o644); err != nil {
		t.Fatal(err)
	}

	refine.StaleNotesAfter = time.Hour
	defer func() { refine.StaleNotesAfter = 30 * 24 * time.Hour }()

	sink = &recordingSink{}
	recordHarnessLoads(sink, store, global)
	if len(sink.events) != 1 {
		t.Fatalf("stale boot emitted %d notices, want 1: %+v", len(sink.events), sink.events)
	}
	notice := sink.events[0]
	if !strings.Contains(notice.Text, "stale prompt note") || !strings.Contains(notice.Text, "idle") {
		t.Fatalf("stale notice text wrong: %q", notice.Text)
	}

	// The second boot refreshed "idle", so a third boot is quiet again.
	sink = &recordingSink{}
	recordHarnessLoads(sink, store, global)
	if len(sink.events) != 0 {
		t.Fatalf("third boot emitted notices: %+v", sink.events)
	}

	// Ledger file lives outside the prompts dir and is plain JSON.
	if _, err := os.Stat(filepath.Join(store.Dir, "usage.json")); err != nil {
		t.Fatalf("usage ledger missing: %v", err)
	}
}
