package novel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// longBody returns a body safely above the minimum accepted length.
func longBody(prefix string) string { return strings.Repeat(prefix+"·", 30) }

// writeChapter writes a chapter body and records it stable, returning the
// rune count reported by MarkStable.
func writeChapter(t *testing.T, ws, id string) int {
	t.Helper()
	body := filepath.Join(ws, "chapters", id, "draft.md")
	if err := os.MkdirAll(filepath.Dir(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body, []byte(longBody(id)), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(ws)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.MarkStable(ws, id, filepath.Join("chapters", id, "draft.md"))
	if err != nil {
		t.Fatalf("MarkStable(%s): %v", id, err)
	}
	return n
}

func TestMarkStableRejectsShortBody(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "short")
	s, _ := Load(ws)
	body := filepath.Join(ws, "chapters", "ch001", "draft.md")
	os.MkdirAll(filepath.Dir(body), 0o755)
	os.WriteFile(body, []byte("空"), 0o644)
	if _, err := s.MarkStable(ws, "ch001", filepath.Join("chapters", "ch001", "draft.md")); err == nil {
		t.Fatal("expected short body to be rejected")
	}
	st, err := s.CheckStability(ws, "ch001")
	if err != nil || st != Unrecorded {
		t.Fatalf("rejected chapter must stay unrecorded, got %q err %v", st, err)
	}
}

func TestRollbackForgetsRecord(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "rb")
	n := writeChapter(t, ws, "ch001")
	if n < minStableBodyRunes {
		t.Fatalf("reported rune count %d below floor %d", n, minStableBodyRunes)
	}
	s, _ := Load(ws)
	if err := s.Rollback(ws, "ch001"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	s, _ = Load(ws)
	if _, ok := s.Chapters["ch001"]; ok {
		t.Fatal("chapter record must be gone after rollback")
	}
	if s.LastStableChapter != "" || !strings.Contains(s.NextAction, "ch001") {
		t.Fatalf("state should point back at ch001, got %+v", s)
	}
	st, err := s.CheckStability(ws, "ch001")
	if err != nil || st != Unrecorded {
		t.Fatalf("after rollback the chapter is unrecorded again, got %q err %v", st, err)
	}
	// Rolling back an unknown chapter is an error.
	if err := s.Rollback(ws, "ch002"); err == nil {
		t.Fatal("rollback of an unrecorded chapter must fail")
	}
}

func TestExportMergesRecordedChapters(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "exp")
	writeChapter(t, ws, "ch001")
	writeChapter(t, ws, "ch002")
	out, err := Export(ws)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, longBody("ch001")) || !strings.Contains(text, longBody("ch002")) {
		t.Fatal("export must contain both chapter bodies")
	}
	if !strings.Contains(text, "ch002") {
		t.Fatal("export should carry chapter separators")
	}
	if strings.Index(text, longBody("ch001")) > strings.Index(text, longBody("ch002")) {
		t.Fatal("export must be in chapter order")
	}
}

func TestStatusShowsTableAndCounts(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "st")
	writeChapter(t, ws, "ch001")
	s, _ := Load(ws)
	text := Status(ws, s)
	for _, want := range []string{"ch001", "stable", "字", "进度：1 章稳定"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q in:\n%s", want, text)
		}
	}
	// A recorded chapter without a summary.md is flagged ✗; with one, ✓.
	if !strings.Contains(text, "摘要✗") {
		t.Fatalf("status should flag missing summary:\n%s", text)
	}
	os.MkdirAll(filepath.Join(ws, "chapters", "ch001"), 0o755)
	os.WriteFile(filepath.Join(ws, "chapters", "ch001", "summary.md"), []byte("摘要"), 0o644)
	if !strings.Contains(Status(ws, s), "摘要✓") {
		t.Fatal("status should mark an existing summary ✓")
	}
}
