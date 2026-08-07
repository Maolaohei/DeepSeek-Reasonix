package refine

import (
	"fmt"
	"strings"
	"time"
)

// Kind identifies which harness component an edit targets.
type Kind string

// Harness kinds. Only prompt has its own storage in this package; memory and
// skill edits are bridged by callers through memory.Store / skill.Store.
const (
	KindPrompt   Kind = "prompt"
	KindMemory   Kind = "memory"
	KindSkill    Kind = "skill"
	KindSubagent Kind = "subagent"
)

// Action is one of create/update/delete.
type Action string

const (
	ActionCreate Action = "create"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
)

// Edit is one model-proposed harness mutation.
type Edit struct {
	Action  Action `json:"action"`
	Kind    Kind   `json:"kind"`
	ID      string `json:"id,omitempty"` // stable id for update/delete
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Proposal is the parsed /refine LLM output.
type Proposal struct {
	Summary         string `json:"summary"`
	Rationale       string `json:"rationale"`
	ExpectedOutcome string `json:"expectedOutcome"`
	Edits           []Edit `json:"edits"`
}

// AppliedEditResult is the outcome of applying one edit.
type AppliedEditResult struct {
	Edit    Edit
	ID      string // resolved id (computed for creates)
	Before  string // serialized note for prompt edits; "" otherwise
	After   string
	Applied bool
	Error   string
}

// validateEdit mirrors Prime Agent's validation rules, adapted to Reasonix:
// actions/kinds are enumerated, create/update need title+content, delete needs
// an id, and the base prompt / user instruction files are never editable.
func validateEdit(e Edit, computedID string) string {
	if e.Action != ActionCreate && e.Action != ActionUpdate && e.Action != ActionDelete {
		return fmt.Sprintf("unsupported action %q", e.Action)
	}
	if e.Kind != KindPrompt && e.Kind != KindMemory && e.Kind != KindSkill && e.Kind != KindSubagent {
		return fmt.Sprintf("unsupported kind %q", e.Kind)
	}
	id := firstNonEmpty(e.ID, computedID)
	if e.Action != ActionCreate && id == "" {
		return fmt.Sprintf("%s requires id", e.Action)
	}
	if e.Action != ActionDelete && (strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Content) == "") {
		return fmt.Sprintf("%s requires title and content", e.Action)
	}
	if e.Action != ActionDelete && e.Kind == KindPrompt && !validNoteID(id) {
		return "invalid prompt note id"
	}
	return ""
}

// Attribution identifies the trajectory context a refinement was applied in.
// It is recorded into the refinement event so later passes can attribute
// harness changes to concrete sessions. All fields are optional; zero values
// are omitted from the event log.
type Attribution struct {
	// SessionID is the branch id of the session that triggered the refinement.
	SessionID string
	// SessionPath is the session file the refinement ran in.
	SessionPath string
	// GoalResult is the latest goal evaluator/continuation reason text, when
	// the session ran under an active goal.
	GoalResult string
}

// ApplyProposal applies the prompt edits of a proposal to the target scope's
// store. Memory/skill/subagent edits are returned unresolved so callers can
// bridge them (this package intentionally has no dependency on those stores).
//
// baselineNotes is the note state captured at planning time; an edit whose
// note changed on disk since then is rejected (concurrent-refinement guard).
// Edits within one proposal are applied in order and may depend on each other.
// attrs optionally carries the session attribution for the event log.
func ApplyProposal(st Store, proposal Proposal, baselineNotes map[string]PromptNote, now time.Time, attrs ...Attribution) ([]AppliedEditResult, error) {
	if !st.Available() {
		return nil, fmt.Errorf("harness store unavailable")
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var results []AppliedEditResult
	var changes []string
	var applied []AppliedEdit
	seen := map[string]bool{} // kind:id touched by this proposal
	for _, edit := range proposal.Edits {
		if edit.Kind != KindPrompt {
			// Bridged kinds are not applied here; report as skipped so the
			// caller can decide. They are still validated.
			if msg := validateEdit(edit, edit.ID); msg != "" {
				results = append(results, AppliedEditResult{Edit: edit, ID: edit.ID, Error: msg})
				continue
			}
			results = append(results, AppliedEditResult{Edit: edit, ID: edit.ID, Applied: false})
			continue
		}
		id := edit.ID
		if id == "" && edit.Action == ActionCreate {
			id = slugFromTitle(edit.Title)
		}
		if msg := validateEdit(edit, id); msg != "" {
			results = append(results, AppliedEditResult{Edit: edit, ID: id, Error: msg})
			continue
		}
		key := "prompt:" + id
		before, beforeOK := st.ReadNote(id)
		// Concurrent-change guard: only the first edit for a key in this
		// proposal is checked against the planning baseline. The baseline is
		// keyed by scope:id so project and global notes with the same id never
		// false-conflict.
		if !seen[key] {
			if baseline, ok := baselineNotes[st.Scope.String()+":"+id]; ok {
				changed := beforeOK && (before.Version != baseline.Version || before.Content != baseline.Content)
				// update/delete on a note created after planning, or create on
				// a note deleted after planning, is a concurrent write too.
				vanished := beforeOK != (baseline.Version > 0)
				if changed || vanished {
					results = append(results, AppliedEditResult{Edit: edit, ID: id, Before: beforeSnapshot(before, beforeOK), Error: ErrConflict.Error()})
					continue
				}
			}
		}
		seen[key] = true
		switch edit.Action {
		case ActionDelete:
			if !beforeOK {
				results = append(results, AppliedEditResult{Edit: edit, ID: id, Error: "entry not found"})
				continue
			}
			path, err := st.DeleteNote(id)
			if err != nil {
				results = append(results, AppliedEditResult{Edit: edit, ID: id, Before: beforeSnapshot(before, beforeOK), Error: err.Error()})
				continue
			}
			_ = path
			results = append(results, AppliedEditResult{Edit: edit, ID: id, Before: beforeSnapshot(before, beforeOK), Applied: true})
			changes = append(changes, fmt.Sprintf("delete prompt:%s", id))
			applied = append(applied, AppliedEdit{Kind: "prompt", ID: id, Action: "delete", Before: beforeSnapshot(before, beforeOK)})
		case ActionCreate, ActionUpdate:
			if edit.Action == ActionCreate && beforeOK {
				results = append(results, AppliedEditResult{Edit: edit, ID: id, Before: beforeSnapshot(before, beforeOK), Error: "entry already exists"})
				continue
			}
			if edit.Action == ActionUpdate && !beforeOK {
				results = append(results, AppliedEditResult{Edit: edit, ID: id, Error: "entry not found"})
				continue
			}
			note := PromptNote{
				ID:        id,
				Title:     edit.Title,
				Scope:     st.Scope,
				Content:   edit.Content,
				CreatedAt: before.CreatedAt,
			}
			path, err := st.SaveNote(note)
			if err != nil {
				results = append(results, AppliedEditResult{Edit: edit, ID: id, Before: beforeSnapshot(before, beforeOK), Error: err.Error()})
				continue
			}
			after, _ := st.ReadNote(id)
			_ = path
			results = append(results, AppliedEditResult{
				Edit: edit, ID: id, Before: beforeSnapshot(before, beforeOK), After: renderNote(after), Applied: true,
			})
			changes = append(changes, fmt.Sprintf("%s prompt:%s", edit.Action, id))
			applied = append(applied, AppliedEdit{
				Kind: "prompt", ID: id, Action: string(edit.Action), Before: beforeSnapshot(before, beforeOK), After: renderNote(after),
			})
		}
	}
	if len(changes) > 0 {
		var attr Attribution
		if len(attrs) > 0 {
			attr = attrs[0]
		}
		ev := RefinementEvent{
			ID:          refinementID(now),
			Trigger:     proposal.Summary,
			Changes:     changes,
			Evidence:    proposal.Rationale,
			Outcome:     proposal.ExpectedOutcome,
			SessionID:   attr.SessionID,
			SessionPath: attr.SessionPath,
			GoalResult:  attr.GoalResult,
			CreatedAt:   now,
			Applied:     applied,
		}
		if err := st.AppendRefinement(ev); err != nil {
			return results, err
		}
	}
	return results, nil
}

// beforeSnapshot serializes a note for rollback snapshots. Creates record an
// empty before (the note did not exist); updates and deletes record the full
// prior content so rollback can restore it.
func beforeSnapshot(before PromptNote, beforeOK bool) string {
	if !beforeOK {
		return ""
	}
	return renderNote(before)
}

func refinementID(now time.Time) string {
	return "refine_" + now.UTC().Format("20060102T150405.000000000")
}

// slugFromTitle derives a note id from a title, mirroring the memory slug rules.
func slugFromTitle(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '_' || r == '-' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	id := strings.Trim(b.String(), "-")
	if id == "" {
		return "note"
	}
	if len(id) > 80 {
		id = id[:80]
	}
	return strings.Trim(id, "-")
}

// RollbackProposal builds the inverse edits for a recorded event, restoring the
// prompt notes it changed. Edits are emitted in reverse application order so
// each restore sees the state its predecessor produced (a create then an update
// rolls back as delete-then-restore, not restore-then-delete). Returns an empty
// proposal when nothing is rollable.
func RollbackProposal(ev RefinementEvent) Proposal {
	var edits []Edit
	for i := len(ev.Applied) - 1; i >= 0; i-- {
		applied := ev.Applied[i]
		if applied.Kind != "prompt" {
			continue
		}
		switch applied.Action {
		case "delete":
			if applied.Before != "" {
				note, ok := parseSerializedNote(applied.Before)
				if ok {
					edits = append(edits, Edit{Action: ActionCreate, Kind: KindPrompt, ID: note.ID, Title: note.Title, Content: note.Content, Reason: "Rollback " + ev.ID})
				}
			}
		case "create", "update":
			if applied.Before != "" {
				note, ok := parseSerializedNote(applied.Before)
				if ok {
					edits = append(edits, Edit{Action: ActionUpdate, Kind: KindPrompt, ID: note.ID, Title: note.Title, Content: note.Content, Reason: "Rollback " + ev.ID})
				}
			} else {
				edits = append(edits, Edit{Action: ActionDelete, Kind: KindPrompt, ID: applied.ID, Reason: "Rollback " + ev.ID})
			}
		}
	}
	return Proposal{
		Summary:         "Rollback refinement " + ev.ID,
		Rationale:       "Restores prompt notes to their state before refinement " + ev.ID + ".",
		ExpectedOutcome: "Faulty refinement edits are reverted.",
		Edits:           edits,
	}
}

// parseSerializedNote re-reads a note from a serialized snapshot (the Before
// payload of an applied edit). A note whose file was deleted is restored via
// SaveNote through the normal create path.
func parseSerializedNote(serialized string) (PromptNote, bool) {
	if strings.TrimSpace(serialized) == "" {
		return PromptNote{}, false
	}
	// parseNote reads from disk; reuse its parser by splitting the serialized
	// frontmatter here. renderNote emits exactly the shape parseNote reads.
	note, ok := parseNoteFromBytes([]byte(serialized))
	return note, ok
}
