package novel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitScaffoldAndMarkStable(t *testing.T) {
	dir := t.TempDir()
	ws, err := Init(dir, "testbook")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, f := range []string{"brief.md", "bible.md", "characters.md", "outline.md", "foreshadowing.md", "style.md", "state.yaml"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
			t.Fatalf("missing scaffold %s: %v", f, err)
		}
	}
	// The style template must lead with original-author excerpts for mimicry;
	// AI-written samples are only a fallback.
	style, err := os.ReadFile(filepath.Join(ws, "style.md"))
	if err != nil {
		t.Fatalf("read style.md: %v", err)
	}
	if !strings.Contains(string(style), "优先放原文片段") {
		t.Fatalf("style.md must instruct original-first mimicry, got %q", string(style))
	}
	s, err := Load(ws)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.NextAction == "" || !strings.Contains(s.NextAction, "ch001") {
		t.Fatalf("NextAction should point at ch001, got %q", s.NextAction)
	}

	// Simulate an accepted chapter and mark it stable.
	body := filepath.Join(ws, "chapters", "ch001", "draft.md")
	if err := os.MkdirAll(filepath.Dir(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body, []byte("第一章正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStable(ws, "ch001", filepath.Join("chapters", "ch001", "draft.md")); err != nil {
		t.Fatalf("MarkStable: %v", err)
	}
	st, err := s.CheckStability(ws, "ch001")
	if err != nil || st != Stable {
		t.Fatalf("expected stable after mark, got %q err %v", st, err)
	}
	if s.LastStableChapter != "ch001" || s.NextAction == "" {
		t.Fatalf("state not advanced: %+v", s)
	}
}

func TestStaleDetectionOnUserEdit(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "b")
	s, _ := Load(ws)
	body := filepath.Join(ws, "chapters", "ch001", "draft.md")
	os.MkdirAll(filepath.Dir(body), 0o755)
	os.WriteFile(body, []byte("v1"), 0o644)
	if err := s.MarkStable(ws, "ch001", filepath.Join("chapters", "ch001", "draft.md")); err != nil {
		t.Fatal(err)
	}
	// User edits the body without going through MarkStable.
	os.WriteFile(body, []byte("v2——用户改过"), 0o644)
	st, err := s.CheckStability(ws, "ch001")
	if err != nil {
		t.Fatal(err)
	}
	if st != Stale {
		t.Fatalf("expected stale after external edit, got %q", st)
	}
	// The body must be untouched by the check.
	b, _ := os.ReadFile(body)
	if string(b) != "v2——用户改过" {
		t.Fatalf("check must not rewrite the body, got %q", b)
	}
	// MarkStale records the inconsistency without touching the body.
	if err := s.MarkStale(ws, "ch001"); err != nil {
		t.Fatal(err)
	}
	if s.Chapters["ch001"].Status != "stale" {
		t.Fatalf("chapter not flagged stale: %+v", s.Chapters["ch001"])
	}
}

func TestNextID(t *testing.T) {
	cases := map[string]string{"ch001": "ch002", "ch009": "ch010", "ch12": "ch013", "ch99": "ch100"}
	for in, want := range cases {
		if got := nextID(in); got != want {
			t.Errorf("nextID(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestStatusRendersProgress(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "c")
	s, _ := Load(ws)
	line := Status(ws, s)
	if !strings.Contains(line, "下一步") || !strings.Contains(line, "ch001") {
		t.Fatalf("status should point at first step, got %q", line)
	}
}

func TestUnrecordedAndBodyMissing(t *testing.T) {
	dir := t.TempDir()
	ws, _ := Init(dir, "d")
	s, _ := Load(ws)
	if st, err := s.CheckStability(ws, "ch001"); err != nil || st != Unrecorded {
		t.Fatalf("untouched chapter should be unrecorded, got %q err %v", st, err)
	}
	// A recorded chapter whose body file is gone reports body_missing.
	body := filepath.Join(ws, "chapters", "ch001", "draft.md")
	os.MkdirAll(filepath.Dir(body), 0o755)
	os.WriteFile(body, []byte("x"), 0o644)
	if err := s.MarkStable(ws, "ch001", filepath.Join("chapters", "ch001", "draft.md")); err != nil {
		t.Fatal(err)
	}
	os.Remove(body)
	st, err := s.CheckStability(ws, "ch001")
	if err != nil || st != BodyMissing {
		t.Fatalf("missing body should report body_missing, got %q err %v", st, err)
	}
}

func TestValidateRejectsPathEscape(t *testing.T) {
	if err := validateTitle("../evil"); err == nil {
		t.Fatal("title with path separator must be rejected")
	}
	if err := validateTitle(""); err == nil {
		t.Fatal("empty title must be rejected")
	}
	if err := validateChapter("ch001"); err != nil {
		t.Fatalf("valid chapter rejected: %v", err)
	}
	for _, bad := range []string{"ch00x", "chapter1", "ch001/../ch002", "ch"} {
		if err := validateChapter(bad); err == nil {
			t.Fatalf("chapter %q must be rejected", bad)
		}
	}
}
