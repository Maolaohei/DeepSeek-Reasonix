package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecentSessionFilesNewestFirstExcludesCurrent(t *testing.T) {
	dir := t.TempDir()
	// Create sibling sessions with distinct mtimes.
	old := filepath.Join(dir, "old.jsonl")
	mid := filepath.Join(dir, "mid.jsonl")
	newest := filepath.Join(dir, "newest.jsonl")
	current := filepath.Join(dir, "current.jsonl")
	for _, p := range []string{old, mid, newest, current} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, base, base)
	_ = os.Chtimes(mid, base.Add(time.Minute), base.Add(time.Minute))
	_ = os.Chtimes(newest, base.Add(2*time.Minute), base.Add(2*time.Minute))
	_ = os.Chtimes(current, base.Add(3*time.Minute), base.Add(3*time.Minute))

	got := recentSessionFiles(dir, current, 3)
	want := []string{newest, mid, old}
	if len(got) != len(want) {
		t.Fatalf("recent = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recent[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	// Non-jsonl files are ignored; empty dir yields nothing.
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	if got := recentSessionFiles(dir, current, 3); len(got) != 3 {
		t.Fatalf("non-jsonl leaked into recent sessions: %v", got)
	}
	if got := recentSessionFiles(t.TempDir(), "", 3); len(got) != 0 {
		t.Fatalf("empty dir yielded sessions: %v", got)
	}
}

func TestRefineSessionRecordsRendersReferences(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.jsonl")
	if err := os.WriteFile(current, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "older.jsonl"), []byte("{}"), 0o644)

	c := &Controller{sessionPath: current, sessionDir: dir}
	recs := c.refineSessionRecords()
	if !strings.Contains(recs, "current session: "+current) {
		t.Fatalf("current session missing from records:\n%s", recs)
	}
	if !strings.Contains(recs, "older.jsonl") {
		t.Fatalf("recent session missing from records:\n%s", recs)
	}
	// No goal state: no evaluation line.
	if strings.Contains(recs, "goal evaluation") {
		t.Fatalf("goal line rendered without goal state:\n%s", recs)
	}

	// No session path: no records at all.
	empty := &Controller{}
	if got := empty.refineSessionRecords(); got != "" {
		t.Fatalf("records without session path = %q, want empty", got)
	}
}

func TestRefineSessionRecordsIncludesGoalResult(t *testing.T) {
	c := &Controller{}
	c.goals.lastEvaluatorReason = "blocked; missing API key"
	if got := c.recentGoalResult(); !strings.Contains(got, "missing API key") {
		t.Fatalf("recentGoalResult = %q, want evaluator reason", got)
	}
	if recs := c.refineSessionRecords(); recs != "" {
		t.Fatalf("records must be empty without a session path, got %q", recs)
	}
}
