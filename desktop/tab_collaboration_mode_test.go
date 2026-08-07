package main

import (
	"encoding/json"
	"testing"

	"reasonix/internal/control"
)

// TestPersistedTabCollaborationMode pins the persistence round-trip for the
// user-selected collaboration flavor: novel must survive a restart via
// desktop-tabs.json while "normal" stays empty (legacy-compatible).
func TestPersistedTabCollaborationMode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"novel", "novel"},
		{"plan", "plan"},
		{"goal", "goal"},
		{"normal", ""},
		{"", ""},
	} {
		if got := persistedTabCollaborationMode(tc.in); got != tc.want {
			t.Fatalf("persistedTabCollaborationMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCurrentTabCollaborationModePrefersPersistedFlavor verifies that the
// persisted marker (novel) wins over controller-derived normal state, and that
// plan/goal derivation still works when no marker is set.
func TestCurrentTabCollaborationModePrefersPersistedFlavor(t *testing.T) {
	tab := &WorkspaceTab{collaborationMode: "novel"}
	if got := currentTabCollaborationMode(tab); got != "novel" {
		t.Fatalf("novel marker lost: got %q", got)
	}

	plain := &WorkspaceTab{mode: "plan"}
	if got := currentTabCollaborationMode(plain); got != "plan" {
		t.Fatalf("plan derivation broken: got %q", got)
	}
	// Live controller state wins over the persisted marker; "normal" only
	// wins when nothing live overrides it.
	plain = &WorkspaceTab{collaborationMode: "normal", mode: "plan"}
	if got := currentTabCollaborationMode(plain); got != "plan" {
		t.Fatalf("live plan must win over persisted normal: got %q", got)
	}
	plain = &WorkspaceTab{collaborationMode: "normal"}
	if got := currentTabCollaborationMode(plain); got != "normal" {
		t.Fatalf("explicit normal with no live state: got %q", got)
	}

	snap := snapshotTabRuntimeLocked(&WorkspaceTab{collaborationMode: "novel"})
	if got := snap.collaborationMode(); got != "novel" {
		t.Fatalf("snapshot lost novel marker: got %q", got)
	}
	snapEmpty := snapshotTabRuntimeLocked(&WorkspaceTab{})
	if got := snapEmpty.collaborationMode(); got != "normal" {
		t.Fatalf("empty snapshot should derive normal: got %q", got)
	}
}

// TestCollaborationModeSurvivesTabsFileRoundTrip pins the JSON shape used by
// desktop-tabs.json: novel is stored, normal is omitted (omitempty), and the
// stored entry restores into a WorkspaceTab.
func TestCollaborationModeSurvivesTabsFileRoundTrip(t *testing.T) {
	entries := []desktopTabEntry{{
		ID: "t1", Scope: "global",
		Mode: "normal", CollaborationMode: "novel",
	}}
	b, err := json.Marshal(desktopTabsFile{Tabs: entries, ActiveTab: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	var f desktopTabsFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Tabs) != 1 || f.Tabs[0].CollaborationMode != "novel" {
		t.Fatalf("round trip lost collaborationMode: %s", b)
	}

	tab := &WorkspaceTab{}
	tab.collaborationMode = persistedTabCollaborationMode(f.Tabs[0].CollaborationMode)
	if tab.collaborationMode != "novel" {
		t.Fatalf("restore failed: %q", tab.collaborationMode)
	}

	// normal entries stay omitted for legacy compatibility.
	normal, err := json.Marshal(desktopTabEntry{ID: "t2", Scope: "global", CollaborationMode: ""})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(normal, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["collaborationMode"]; ok {
		t.Fatalf("normal collaborationMode should be omitted, got %s", normal)
	}
}

// TestClearGoalDropsStaleGoalMarker ensures a cleared goal does not re-surface
// as goal mode after a restart.
func TestClearGoalDropsStaleGoalMarker(t *testing.T) {
	tab := &WorkspaceTab{goal: "write chapter", collaborationMode: "goal", mode: "normal"}
	tab.goal = ""
	if tab.collaborationMode == "goal" {
		// Mirrors SetGoalForTab's cleanup branch.
		tab.collaborationMode = ""
	}
	if got := currentTabCollaborationMode(tab); got != "normal" {
		t.Fatalf("cleared goal should derive normal, got %q", got)
	}
	_ = control.GoalStatusRunning // keep the import used by parity with other desktop tests
}
