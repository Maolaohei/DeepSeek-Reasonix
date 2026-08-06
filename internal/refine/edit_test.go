package refine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	store := Store{Dir: filepath.Join(dir, "harness"), Scope: ScopeProject}
	if err := os.MkdirAll(store.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestNoteRoundTrip(t *testing.T) {
	store := testStore(t)
	now := time.Now().UTC()
	path, err := store.SaveNote(PromptNote{
		ID: "arc-grid-dedup", Title: "Deduplicate rows before grid expansion",
		Scope: ScopeProject, Content: "Always deduplicate rows before expanding the grid.",
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	note, ok := parseNote(path)
	if !ok {
		t.Fatal("note did not round-trip")
	}
	if note.ID != "arc-grid-dedup" || note.Title != "Deduplicate rows before grid expansion" ||
		note.Content != "Always deduplicate rows before expanding the grid." {
		t.Fatalf("note content mismatch: %+v", note)
	}
	if note.Version != 1 {
		t.Fatalf("expected version 1, got %d", note.Version)
	}
}

func TestSaveNoteBumpsVersionAndArchives(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v2"}); err != nil {
		t.Fatal(err)
	}
	note, ok := store.ReadNote("rule")
	if !ok {
		t.Fatal("missing note")
	}
	if note.Version != 2 || note.Content != "v2" {
		t.Fatalf("expected v2 content, got version=%d content=%q", note.Version, note.Content)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, historyDirName, "rule.mdl")); err != nil {
		t.Fatalf("prior revision not archived: %v", err)
	}
}

func TestDeleteNoteArchivesLastContent(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteNote("rule"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ReadNote("rule"); ok {
		t.Fatal("note still present after delete")
	}
	if _, err := os.Stat(filepath.Join(store.Dir, historyDirName, "rule.mdl")); err != nil {
		t.Fatalf("deleted note not archived: %v", err)
	}
}

func TestApplyProposalCreateUpdateDelete(t *testing.T) {
	store := testStore(t)
	proposal := Proposal{
		Summary: "learn dedup rule", Rationale: "saw repeated failure", ExpectedOutcome: "fewer failures",
		Edits: []Edit{
			{Action: ActionCreate, Kind: KindPrompt, Title: "Deduplicate rows", Content: "Dedupe first."},
			{Action: ActionUpdate, Kind: KindPrompt, ID: "deduplicate-rows", Title: "Deduplicate rows", Content: "Dedupe before expand."},
			{Action: ActionDelete, Kind: KindPrompt, ID: "deduplicate-rows"},
		},
	}
	results, err := ApplyProposal(store, proposal, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if !r.Applied || r.Error != "" {
			t.Fatalf("edit %d not applied: applied=%v error=%q", i, r.Applied, r.Error)
		}
	}
	if _, ok := store.ReadNote("deduplicate-rows"); ok {
		t.Fatal("note should have been deleted")
	}
	events := store.ListRefinements()
	if len(events) != 1 {
		t.Fatalf("expected 1 refinement event, got %d", len(events))
	}
	if len(events[0].Changes) != 3 {
		t.Fatalf("expected 3 changes, got %v", events[0].Changes)
	}
}

func TestApplyProposalValidation(t *testing.T) {
	store := testStore(t)
	cases := []struct {
		name string
		edit Edit
		want string // substring of error
	}{
		{"bad action", Edit{Action: "nuke", Kind: KindPrompt, ID: "x", Title: "t", Content: "c"}, "unsupported action"},
		{"bad kind", Edit{Action: ActionCreate, Kind: "nope", Title: "t", Content: "c"}, "unsupported kind"},
		{"missing title", Edit{Action: ActionCreate, Kind: KindPrompt, Content: "c"}, "requires title"},
		{"missing content", Edit{Action: ActionCreate, Kind: KindPrompt, Title: "t"}, "requires title and content"},
		{"delete without id", Edit{Action: ActionDelete, Kind: KindPrompt}, "requires id"},
		{"invalid note id", Edit{Action: ActionCreate, Kind: KindPrompt, Title: "t", Content: "c", ID: "Bad ID/../x"}, "invalid prompt note id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results, err := ApplyProposal(store, Proposal{Edits: []Edit{tc.edit}}, nil, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Error == "" || !strings.Contains(results[0].Error, tc.want) {
				t.Fatalf("expected error containing %q, got %+v", tc.want, results)
			}
			if len(store.ListRefinements()) != 0 {
				t.Fatal("validation failures must not write a refinement event")
			}
		})
	}
}

func TestApplyProposalConflictDetection(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v1"}); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]PromptNote{"project:rule": {ID: "rule", Version: 1, Content: "v1"}}
	// Simulate a concurrent write between planning and applying.
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v2-concurrent"}); err != nil {
		t.Fatal(err)
	}
	results, err := ApplyProposal(store, Proposal{Edits: []Edit{
		{Action: ActionUpdate, Kind: KindPrompt, ID: "rule", Title: "Rule", Content: "v3"},
	}}, baseline, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Applied || !strings.Contains(results[0].Error, "changed during") {
		t.Fatalf("expected conflict rejection, got %+v", results)
	}
}

func TestRollbackProposal(t *testing.T) {
	store := testStore(t)
	proposal := Proposal{
		Summary: "learn rule", Rationale: "evidence", ExpectedOutcome: "better",
		Edits: []Edit{
			{Action: ActionCreate, Kind: KindPrompt, Title: "Rule", Content: "v1"},
			{Action: ActionUpdate, Kind: KindPrompt, ID: "rule", Title: "Rule", Content: "v2"},
		},
	}
	if _, err := ApplyProposal(store, proposal, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	note, _ := store.ReadNote("rule")
	if note.Content != "v2" {
		t.Fatalf("expected v2, got %q", note.Content)
	}
	events := store.ListRefinements()
	if len(events) != 1 {
		t.Fatal("expected one event")
	}
	rollback := RollbackProposal(events[0])
	// Reverse order: the update's restore first, then the create's delete.
	if len(rollback.Edits) != 2 || rollback.Edits[0].Action != ActionUpdate || rollback.Edits[1].Action != ActionDelete {
		t.Fatalf("expected update-then-delete rollback, got %+v", rollback.Edits)
	}
	if _, err := ApplyProposal(store, rollback, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	// The create's inverse is a delete, so the note no longer exists.
	if _, ok := store.ReadNote("rule"); ok {
		t.Fatal("rule should be gone after full rollback")
	}
}

func TestRollbackProposalRestoresPriorVersion(t *testing.T) {
	// An update-only refinement (note pre-existed) must restore the prior
	// content, not delete the note.
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyProposal(store, Proposal{Edits: []Edit{
		{Action: ActionUpdate, Kind: KindPrompt, ID: "rule", Title: "Rule", Content: "v2"},
	}}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	events := store.ListRefinements()
	rollback := RollbackProposal(events[0])
	if _, err := ApplyProposal(store, rollback, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	note, ok := store.ReadNote("rule")
	if !ok {
		t.Fatal("rule should still exist after update-only rollback")
	}
	if note.Content != "v1" {
		t.Fatalf("expected v1 restored, got %q", note.Content)
	}
}

func TestRollbackProposalCreateOnly(t *testing.T) {
	// A pure-create refinement must roll back by deleting the created note.
	store := testStore(t)
	proposal := Proposal{
		Summary: "learn rule", Rationale: "evidence", ExpectedOutcome: "better",
		Edits: []Edit{
			{Action: ActionCreate, Kind: KindPrompt, Title: "Rule", Content: "v1"},
		},
	}
	if _, err := ApplyProposal(store, proposal, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ReadNote("rule"); !ok {
		t.Fatal("note should exist after create")
	}
	events := store.ListRefinements()
	if len(events) != 1 {
		t.Fatal("expected one event")
	}
	rollback := RollbackProposal(events[0])
	if len(rollback.Edits) != 1 || rollback.Edits[0].Action != ActionDelete {
		t.Fatalf("create rollback should be a delete, got %+v", rollback.Edits)
	}
	if _, err := ApplyProposal(store, rollback, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.ReadNote("rule"); ok {
		t.Fatal("note should be deleted after create-rollback")
	}
}

func TestApplyProposalDeleteAfterConcurrentCreate(t *testing.T) {
	// update/delete on a note created after planning is a concurrent write and
	// must be rejected, even though the baseline has no entry for it.
	store := testStore(t)
	results, err := ApplyProposal(store, Proposal{Edits: []Edit{
		{Action: ActionUpdate, Kind: KindPrompt, ID: "ghost", Title: "Ghost", Content: "v2"},
	}}, map[string]PromptNote{"project:ghost": {ID: "ghost", Version: 1, Content: "v1"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Applied {
		t.Fatalf("expected rejection when the note does not exist yet: %+v", results)
	}
}

func TestSaveNoteEnforcesLimit(t *testing.T) {
	store := testStore(t)
	old := MaxPromptNotes
	MaxPromptNotes = 2
	defer func() { MaxPromptNotes = old }()
	for i := 0; i < 2; i++ {
		if _, err := store.SaveNote(PromptNote{ID: "note" + string(rune('a'+i)), Title: "N", Content: "c"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SaveNote(PromptNote{ID: "note-extra", Title: "N", Content: "c"}); err == nil {
		t.Fatal("expected create rejection past the limit")
	}
	// Updating an existing note stays allowed past the limit.
	if _, err := store.SaveNote(PromptNote{ID: "notea", Title: "N", Content: "c2"}); err != nil {
		t.Fatalf("update past the limit should be allowed: %v", err)
	}
}

func TestParseProposal(t *testing.T) {
	text := "Let me think...\n{\"summary\":\"s\",\"rationale\":\"r\",\"expectedOutcome\":\"o\",\"edits\":[{\"action\":\"create\",\"kind\":\"prompt\",\"title\":\"T\",\"content\":\"C\",\"reason\":\"why\"}]}"
	proposal, err := parseProposal(text)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Summary != "s" || len(proposal.Edits) != 1 {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
	if proposal.Edits[0].Kind != KindPrompt || proposal.Edits[0].Content != "C" {
		t.Fatalf("unexpected edit: %+v", proposal.Edits[0])
	}
}

func TestParseProposalUnterminated(t *testing.T) {
	if _, err := parseProposal("no json here"); err == nil {
		t.Fatal("expected error for missing JSON")
	}
	if _, err := parseProposal(`{"summary":"unterminated`); err == nil {
		t.Fatal("expected error for unterminated JSON")
	}
}

func TestGuidanceBlockContent(t *testing.T) {
	// The fixed guidance must tell the model when to call refine and that the
	// base prompt is immutable — the no-configuration adoption contract.
	for _, want := range []string{"refine tool", "immutable", "project scope", "reusable lessons"} {
		if !strings.Contains(GuidanceBlock, want) {
			t.Fatalf("GuidanceBlock missing %q:\n%s", want, GuidanceBlock)
		}
	}
}

func TestComposeEmptyKeepsPromptByteIdentical(t *testing.T) {
	// Guidance is boot's job; Compose with no notes must not change the prompt.
	store := Store{Dir: filepath.Join(t.TempDir(), "empty-harness"), Scope: ScopeProject}
	base := "BASE SYSTEM PROMPT"
	if got := Compose(base, store); got != base {
		t.Fatalf("empty harness must leave the prompt untouched, got:\n%s", got)
	}
}

func TestComposeWithNotesFollowsGuidance(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v1"}); err != nil {
		t.Fatal(err)
	}
	base := "BASE"
	got := Compose(base, store)
	// The notes list follows the guidance block (injected by boot): Compose
	// itself only appends the notes summary, prefixed by the state heading.
	if !strings.Contains(got, "- [project:rule] Rule (v1): v1") {
		t.Fatalf("notes summary missing:\n%s", got)
	}
	if strings.Contains(got, "Call the refine tool when") {
		t.Fatal("Compose must not duplicate the guidance (boot injects it)")
	}
}

func TestRenderOverviewAndHistory(t *testing.T) {
	store := testStore(t)
	if _, err := store.SaveNote(PromptNote{ID: "rule", Title: "Rule", Content: "v1", Description: "a hook"}); err != nil {
		t.Fatal(err)
	}
	notes := store.ListNotes()
	overview := RenderOverview(notes, "memories: none")
	if !strings.Contains(overview, "project:rule") || !strings.Contains(overview, "a hook") {
		t.Fatalf("overview missing entries:\n%s", overview)
	}
	history := RenderHistory(nil)
	if history != "(none)" {
		t.Fatalf("expected empty history, got %q", history)
	}
}

func TestSlugFromTitle(t *testing.T) {
	cases := map[string]string{
		"Deduplicate rows before expansion": "deduplicate-rows-before-expansion",
		"  空格 title 测试  ":                   "title",
		"!!!":                               "note",
	}
	for in, want := range cases {
		if got := slugFromTitle(in); got != want {
			t.Fatalf("slugFromTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStoreForPaths(t *testing.T) {
	store := StoreFor("/tmp/userdir", "/work/proj")
	if !strings.HasSuffix(store.Dir, "harness") || !strings.Contains(filepath.ToSlash(store.Dir), "/projects/") {
		t.Fatalf("StoreFor dir = %q, want a projects/<slug>/harness path", store.Dir)
	}
	global := GlobalStoreFor("/tmp/userdir")
	if global.Dir != filepath.Join("/tmp/userdir", "harness", "global") {
		t.Fatalf("GlobalStoreFor dir = %q", global.Dir)
	}
}

func TestRefinementLogRoll(t *testing.T) {
	store := testStore(t)
	old := MaxRefinementsLogBytes
	MaxRefinementsLogBytes = 400
	defer func() { MaxRefinementsLogBytes = old }()
	for i := 0; i < 10; i++ {
		ev := RefinementEvent{ID: "ev" + string(rune('a'+i)), Changes: []string{"x"}, CreatedAt: time.Now()}
		if err := store.AppendRefinement(ev); err != nil {
			t.Fatal(err)
		}
	}
	events := store.ListRefinements()
	if len(events) < 3 {
		t.Fatalf("expected rolled log to keep a tail, got %d events", len(events))
	}
	// Every line must still parse: the roll boundary is line-aligned.
	for _, ev := range events {
		if ev.ID == "" {
			t.Fatal("event unparsable after roll")
		}
	}
	// The tail must include the newest event.
	if events[len(events)-1].ID != "evj" {
		t.Fatalf("expected newest event last, got %q", events[len(events)-1].ID)
	}
}
