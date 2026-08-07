package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

func TestAnnotateCommandNotFoundDetection(t *testing.T) {
	cases := map[string]bool{
		"bash: python3: command not found":                                     true,
		"powershell: The term 'foo' is not recognized as the name of a cmdlet": true,
		"'foo' is not recognized as an internal or external command":           true,
		"command exited: exit status 1":                                        false,
		"invalid args: unexpected token":                                       false,
		"":                                                                     false,
	}
	for text, want := range cases {
		var err error
		if text != "" {
			err = &textErr{text}
		}
		got := annotateCommandNotFound(err)
		// With no probe snapshot on disk the hint is empty, so annotation
		// leaves the error text unchanged but never nils it.
		if want && got == nil {
			t.Errorf("%q: want annotated error, got nil", text)
		}
		if !want && got != err && !(err == nil && got == nil) {
			t.Errorf("%q: want passthrough, got %+v", text, got)
		}
	}
}

type textErr struct{ s string }

func (e *textErr) Error() string { return e.s }

func TestAvailableToolsHintReadsNewestSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REASONIX_CACHE_HOME", dir)
	envDir := filepath.Join(dir, "environment")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(envDir, "probes-old.json")
	_ = os.WriteFile(older, []byte(`{"version":1,"fingerprint":"old","results":[{"Command":"python3","Found":false},{"Command":"git","Found":true}]}`), 0o644)
	newer := filepath.Join(envDir, "probes-new.json")
	_ = os.WriteFile(newer, []byte(`{"version":1,"fingerprint":"new","results":[{"Command":"python","Found":true},{"Command":"python3","Found":false},{"Command":"node","Found":true},{"Command":"go","Found":true}]}`), 0o644)

	hint := availableToolsHint()
	if !strings.Contains(hint, "python") || !strings.Contains(hint, "node") || !strings.Contains(hint, "go") {
		t.Fatalf("hint missing found tools: %q", hint)
	}
	if strings.Contains(hint, "python3") {
		t.Fatalf("hint must not list unfound python3: %q", hint)
	}
	if strings.Contains(hint, "git") {
		t.Fatalf("hint must read the newest snapshot (git is only in the old one): %q", hint)
	}

	// No snapshots: empty hint.
	empty := t.TempDir()
	t.Setenv("REASONIX_CACHE_HOME", empty)
	if got := availableToolsHint(); got != "" {
		t.Fatalf("empty cache hint = %q, want \"\"", got)
	}
}

// fakeGoalRecorder records reports so Execute's contract path is testable.
type fakeGoalRecorder struct {
	reports []tool.GoalReport
}

func (f *fakeGoalRecorder) RecordGoalReport(r tool.GoalReport) (string, error) {
	f.reports = append(f.reports, r)
	return "recorded " + r.Status, nil
}

func TestUpdateGoalStatusSynonyms(t *testing.T) {
	cases := map[string]string{
		"done":          "complete",
		"finished":      "complete",
		"success":       "complete",
		"yes":           "complete",
		"in_progress":   "continue",
		"in-progress":   "continue",
		"working":       "continue",
		"running":       "continue",
		"stuck":         "blocked",
		"failed":        "blocked",
		"cannot":        "blocked",
		"Complete":      "complete",
		"DONE":          "complete",
		"continue":      "continue",
		"complete":      "complete",
		"blocked":       "blocked",
		"anything-else": "anything-else",
	}
	for in, want := range cases {
		if got := normalizeGoalStatus(in); got != want {
			t.Errorf("normalizeGoalStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpdateGoalReasonBorrowsNextAction(t *testing.T) {
	rec := &fakeGoalRecorder{}
	ctx := tool.WithGoalTurnRecorder(context.Background(), rec)

	// continue without reason: next_action stands in for the reason.
	out, err := (updateGoal{}).Execute(ctx, json.RawMessage(`{"status":"working","next_action":"run go test"}`))
	if err != nil {
		t.Fatalf("synonym+borrow should pass: %v", err)
	}
	if !strings.Contains(out, "continue") {
		t.Fatalf("normalized status should be continue, got %q", out)
	}
	if len(rec.reports) != 1 || rec.reports[0].Status != "continue" {
		t.Fatalf("report = %+v, want continue", rec.reports)
	}
	if rec.reports[0].Reason != "run go test" {
		t.Fatalf("reason should borrow next_action, got %q", rec.reports[0].Reason)
	}

	// blocked without reason and without next_action still fails closed.
	if _, err := (updateGoal{}).Execute(ctx, json.RawMessage(`{"status":"stuck"}`)); err == nil {
		t.Fatal("blocked without reason must fail closed")
	}
}
