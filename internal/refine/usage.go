package refine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// usageFileName is the sidecar ledger of note utilization, stored next to the
// notes themselves. It never enters the cache-stable system prefix, so
// recording loads cannot perturb the prompt cache shape.
const usageFileName = "usage.json"

// StaleNotesAfter is how long a note can go without being loaded into any
// session prefix before it is flagged as stale — the lightweight "load
// bearing" signal for Build to Delete: a harness entry no session has used for
// a long time encodes an assumption the model may have outgrown. Kept as a var
// so tests can shrink the horizon.
var StaleNotesAfter = 30 * 24 * time.Hour

// NoteUsage tracks how often a prompt note was loaded into a session prefix
// and when that last happened.
type NoteUsage struct {
	// Loads counts the sessions whose prefix included this note (boot is the
	// only place the prefix is assembled, so this is one increment per session).
	Loads int `json:"loads"`
	// LastLoadedAt is the most recent boot that included the note.
	LastLoadedAt time.Time `json:"last_loaded_at"`
}

// StaleNote is a note flagged by StaleNotes: it exists but has not been loaded
// for the stale horizon.
type StaleNote struct {
	ID    string
	Usage NoteUsage
}

// loadUsage reads the store's usage ledger. A missing or corrupt file is an
// empty ledger (never fatal).
func (s Store) loadUsage() map[string]NoteUsage {
	usage := map[string]NoteUsage{}
	if !s.Available() {
		return usage
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, usageFileName))
	if err != nil {
		return usage
	}
	_ = json.Unmarshal(b, &usage)
	return usage
}

// saveUsage writes the ledger back. Failures are ignored by design: a lost
// counter is harmless, while a write error must never block boot.
func (s Store) saveUsage(usage map[string]NoteUsage) {
	if !s.Available() {
		return
	}
	b, err := json.Marshal(usage)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.Dir, usageFileName), b, 0o644)
}

// RecordNoteLoads marks the given note ids as loaded into a session prefix,
// bumping their load counts and last-loaded timestamps. Callers (boot) invoke
// this once per session after assembling the prefix; ids that do not exist as
// notes (or are invalid) are ignored.
func (s Store) RecordNoteLoads(ids []string) {
	if len(ids) == 0 || !s.Available() {
		return
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	existing := map[string]bool{}
	for _, n := range s.ListNotes() {
		existing[n.ID] = true
	}
	now := time.Now().UTC()
	usage := s.loadUsage()
	changed := false
	for _, id := range ids {
		if !validNoteID(id) || !existing[id] {
			continue
		}
		u := usage[id]
		u.Loads++
		u.LastLoadedAt = now
		usage[id] = u
		changed = true
	}
	if changed {
		s.saveUsage(usage)
	}
}

// NoteUsage returns the ledger entry for one note, ok=false when the note has
// never been recorded (or the store is unavailable).
func (s Store) NoteUsage(id string) (NoteUsage, bool) {
	if !s.Available() {
		return NoteUsage{}, false
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	u, ok := s.loadUsage()[id]
	return u, ok
}

// StaleNotes returns notes that have been recorded but not loaded into any
// session prefix for longer than threshold, oldest-last-loaded first. Notes
// never recorded are not flagged: an unrecorded note may simply predate the
// ledger, and the next boot initializes it.
func (s Store) StaleNotes(threshold time.Duration) []StaleNote {
	if !s.Available() {
		return nil
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	cutoff := time.Now().UTC().Add(-threshold)
	var stale []StaleNote
	for id, u := range s.loadUsage() {
		if u.Loads > 0 && u.LastLoadedAt.Before(cutoff) {
			stale = append(stale, StaleNote{ID: id, Usage: u})
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].Usage.LastLoadedAt.Before(stale[j].Usage.LastLoadedAt) })
	return stale
}
