package refine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
)

// Defaults for the harness store. Kept as package vars so tests can shrink the
// budget without touching config.
var (
	// MaxPromptNotes caps the number of active prompt notes per store scope.
	MaxPromptNotes = 32
	// MaxPromptNoteBytes caps one note's serialized file size.
	MaxPromptNoteBytes = 4 * 1024
	// MaxRefinementsLogBytes caps refinements.jsonl before it rolls.
	MaxRefinementsLogBytes = 1 << 20
)

const (
	promptsDirName     = "prompts"
	historyDirName     = ".history"
	refinementsLogName = "refinements.jsonl"
)

// Store is the on-disk Continual Harness store for one scope. It owns the
// prompt-notes directory and the refinements event log; memory/skill/subagent
// edits are bridged by callers through their own stores.
type Store struct {
	// Dir is the harness root for this store scope (project or global). "" means
	// the store is unavailable (no user dir).
	Dir string
	// Scope is the store's owning scope.
	Scope Scope
}

// StoreFor returns the harness store for a workspace, mirroring memory.StoreFor:
// project notes live under <userDir>/projects/<workspace-slug>/harness and
// global notes under <userDir>/harness/global. A zero userDir yields an
// unavailable store.
func StoreFor(userDir, cwd string) Store {
	if strings.TrimSpace(userDir) == "" {
		return Store{}
	}
	return Store{
		Dir:   filepath.Join(userDir, "projects", config.WorkspaceSlug(absOf(cwd)), "harness"),
		Scope: ScopeProject,
	}
}

// GlobalStoreFor returns the user-level harness store.
func GlobalStoreFor(userDir string) Store {
	if strings.TrimSpace(userDir) == "" {
		return Store{}
	}
	return Store{
		Dir:   filepath.Join(userDir, "harness", "global"),
		Scope: ScopeGlobal,
	}
}

func absOf(path string) string {
	if path == "" {
		return "."
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// Available reports whether the store can be written.
func (s Store) Available() bool { return strings.TrimSpace(s.Dir) != "" }

// promptsDir returns the notes directory, creating it on demand.
func (s Store) promptsDir() (string, error) {
	if !s.Available() {
		return "", fmt.Errorf("harness store unavailable")
	}
	dir := filepath.Join(s.Dir, promptsDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// historyDir returns the per-note revision archive directory.
func (s Store) historyDir() (string, error) {
	if !s.Available() {
		return "", fmt.Errorf("harness store unavailable")
	}
	dir := filepath.Join(s.Dir, historyDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ListNotes returns all active prompt notes in the store, sorted by id.
func (s Store) ListNotes() []PromptNote {
	if !s.Available() {
		return nil
	}
	dir := filepath.Join(s.Dir, promptsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var notes []PromptNote
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		if note, ok := parseNote(filepath.Join(dir, e.Name())); ok && note.ID != "" {
			notes = append(notes, note)
		}
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
	return notes
}

// ReadNote returns one note by id, or ok=false when absent.
func (s Store) ReadNote(id string) (PromptNote, bool) {
	if !validNoteID(id) || !s.Available() {
		return PromptNote{}, false
	}
	path := filepath.Join(s.Dir, promptsDirName, id+".md")
	return parseNote(path)
}

// SaveNote creates or updates a note. An existing note's version is bumped and
// its prior content is archived under .history for rollback. Returns the path
// written. The note's Scope is forced to the store's scope. Creates are
// rejected when the store already holds MaxPromptNotes notes.
func (s Store) SaveNote(n PromptNote) (string, error) {
	if err := s.validateNote(n); err != nil {
		return "", err
	}
	dir, err := s.promptsDir()
	if err != nil {
		return "", err
	}
	path := notePathFor(dir, n.ID)
	if path == "" {
		return "", errInvalidNoteID(n.ID)
	}
	n.Scope = s.Scope
	now := time.Now().UTC()
	if before, ok := parseNote(path); ok {
		// Archive the prior revision for rollback, then bump.
		if hist, herr := s.historyDir(); herr == nil {
			_ = os.WriteFile(filepath.Join(hist, n.ID+".mdl"), []byte(renderNote(before)), 0o644)
		}
		n.Version = before.Version + 1
		n.CreatedAt = before.CreatedAt
	} else {
		if len(s.ListNotes()) >= MaxPromptNotes {
			return "", fmt.Errorf("prompt note limit reached (%d notes); delete a note before creating more", MaxPromptNotes)
		}
		n.Version = 1
		n.CreatedAt = now
	}
	if n.Version <= 0 {
		n.Version = 1
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	rendered := renderNote(n)
	if len(rendered) > MaxPromptNoteBytes {
		return "", fmt.Errorf("prompt note %q exceeds %d bytes", n.ID, MaxPromptNoteBytes)
	}
	if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// DeleteNote removes a note, archiving its last content first. Missing notes
// are not an error; the goal state (absent) already holds.
func (s Store) DeleteNote(id string) (string, error) {
	if !validNoteID(id) || !s.Available() {
		return "", nil
	}
	path := filepath.Join(s.Dir, promptsDirName, id+".md")
	before, ok := parseNote(path)
	if ok {
		if hist, herr := s.historyDir(); herr == nil {
			_ = os.WriteFile(filepath.Join(hist, id+".mdl"), []byte(renderNote(before)), 0o644)
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return path, nil
}

func (s Store) validateNote(n PromptNote) error {
	if !s.Available() {
		return fmt.Errorf("harness store unavailable")
	}
	if !validNoteID(n.ID) {
		return errInvalidNoteID(n.ID)
	}
	if strings.TrimSpace(n.Title) == "" {
		return fmt.Errorf("prompt note requires a title")
	}
	if strings.TrimSpace(n.Content) == "" {
		return fmt.Errorf("prompt note requires content")
	}
	return nil
}

// RefinementEvent is one recorded /refine application, including enough state to
// roll it back (per-edit before/after snapshots).
type RefinementEvent struct {
	ID         string    `json:"id"`
	Trigger    string    `json:"trigger"`
	Changes    []string  `json:"changes"`
	Evidence   string    `json:"evidence"`
	Outcome    string    `json:"outcome"`
	RollbackOf string    `json:"rollback_of,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// Attribution identifies the trajectory context the refinement was applied
	// in, so harness changes stay attributable to concrete sessions (the
	// lightweight analogue of "fixed target environment" in harness evaluation).
	SessionID   string `json:"session_id,omitempty"`
	SessionPath string `json:"session_path,omitempty"`
	GoalResult  string `json:"goal_result,omitempty"`
	// Applied carries the before/after prompt-note snapshots for the prompt
	// edits in this event, keyed by note id.
	Applied []AppliedEdit `json:"applied,omitempty"`
}

// AppliedEdit is the rollback-relevant snapshot of one applied edit.
type AppliedEdit struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Action string `json:"action"`
	Before string `json:"before,omitempty"` // full serialized note for prompt edits
	After  string `json:"after,omitempty"`
}

// AppendRefinement appends one event to the store's refinements.jsonl, rolling
// the file when it exceeds MaxRefinementsLogBytes (keeps the tail).
func (s Store) AppendRefinement(ev RefinementEvent) error {
	if !s.Available() {
		return fmt.Errorf("harness store unavailable")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, refinementsLogName)
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	line := append(b, '\n')
	if info, err := os.Stat(path); err == nil && info.Size() > int64(MaxRefinementsLogBytes) {
		if err := rollRefinementsLog(path); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

// ListRefinements returns the recorded events, oldest first.
func (s Store) ListRefinements() []RefinementEvent {
	if !s.Available() {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, refinementsLogName))
	if err != nil {
		return nil
	}
	var events []RefinementEvent
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev RefinementEvent
		if json.Unmarshal([]byte(line), &ev) == nil && ev.ID != "" {
			events = append(events, ev)
		}
	}
	return events
}

// rollRefinementsLog drops the first half of the log when it outgrows the cap.
func rollRefinementsLog(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	keep := lines[len(lines)/2:]
	// Keep the oldest remaining event's before/after even if it splits mid-line:
	// the roll boundary is always at a line start since we split on newlines.
	trimmed := strings.TrimPrefix(strings.Join(keep, "\n"), "\n")
	if err := os.WriteFile(path, []byte(trimmed), 0o644); err != nil {
		return err
	}
	return nil
}

// ErrConflict is returned by ApplyProposal when the on-disk state changed
// between planning and applying.
var ErrConflict = errors.New("harness state changed during refinement planning")

// mutex serializes harness store mutations process-wide (the store has no
// instance-level lock and /refine may run from multiple sessions).
var storeMu sync.Mutex
