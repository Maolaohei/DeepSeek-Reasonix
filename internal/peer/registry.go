package peer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// HeartbeatInterval is how often a live session refreshes its record.
const HeartbeatInterval = 10 * time.Second

// StaleAfter is how long without a heartbeat a session is considered stalled.
const StaleAfter = 45 * time.Second

// MailRetention is how long undelivered letters survive an offline session.
const MailRetention = 30 * 24 * time.Hour

// Registry tracks session presence records under the peers root.
type Registry struct {
	root string
	now  func() time.Time
}

// NewRegistry opens (creating) the registry directory.
func NewRegistry(peersRoot string) (*Registry, error) {
	dir := RegistryDir(peersRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Registry{root: dir, now: time.Now}, nil
}

func (r *Registry) path(addr string) string {
	return filepath.Join(r.root, addr+".json")
}

// RecordSession upserts the presence record for a session, keeping any
// existing mail. Resuming a session restores its previous record.
func (r *Registry) RecordSession(rec Record) error {
	if rec.Addr == "" {
		return errors.New("peer: record addr is required")
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = r.now().UnixMilli()
	}
	rec.BeatAt = r.now().UnixMilli()
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(rec.Addr), raw, 0o600)
}

// Heartbeat refreshes the beat timestamp of a live session.
func (r *Registry) Heartbeat(addr string) error {
	rec, err := r.Load(addr)
	if err != nil {
		return err
	}
	rec.BeatAt = r.now().UnixMilli()
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(addr), raw, 0o600)
}

// Load returns the record for an address, or os.ErrNotExist.
func (r *Registry) Load(addr string) (Record, error) {
	var rec Record
	raw, err := os.ReadFile(r.path(addr))
	if err != nil {
		return rec, err
	}
	err = json.Unmarshal(raw, &rec)
	return rec, err
}

// Presence classifies a session as live, stalled, or offline.
func (r *Registry) Presence(addr string, pidAlive func(int) bool) Presence {
	rec, err := r.Load(addr)
	if err != nil {
		return Offline
	}
	if rec.PID == 0 || (pidAlive != nil && !pidAlive(rec.PID)) {
		return Offline
	}
	age := r.now().Sub(time.UnixMilli(rec.BeatAt))
	if age > StaleAfter {
		return Stalled
	}
	return Live
}

// MarkOffline keeps the record and any mail but clears the pid (A.3: a
// session with persistent files resumes into the same address later). The
// beat timestamp is preserved so the sweep grace period counts from the last
// heartbeat instead of instantly expiring the record.
func (r *Registry) MarkOffline(addr string) error {
	rec, err := r.Load(addr)
	if err != nil {
		return err
	}
	rec.PID = 0
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(r.path(addr), raw, 0o600)
}

// List returns all presence records, newest first.
func (r *Registry) List() []Record {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return nil
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !jsonSuffix(e.Name()) {
			continue
		}
		var rec Record
		raw, err := os.ReadFile(filepath.Join(r.root, e.Name()))
		if err == nil && json.Unmarshal(raw, &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// Sweep removes dead records, mail-first: a mailbox with undelivered letters
// survives until MailRetention passes; an empty mailbox survives a grace
// period after the last heartbeat.
func (r *Registry) Sweep() error {
	now := r.now()
	for _, rec := range r.List() {
		age := now.Sub(time.UnixMilli(rec.BeatAt))
		mb, err := NewMailbox(filepath.Dir(r.root), rec.Addr)
		if err != nil {
			continue
		}
		if mb.PendingCount() > 0 {
			if age < MailRetention {
				continue // undelivered mail outlives the session
			}
		} else if age < SweepGrace {
			continue
		}
		if err := mb.Remove(); err != nil {
			return err
		}
		if err := os.Remove(r.path(rec.Addr)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SweepGrace is how long an offline, empty session record is kept.
const SweepGrace = 24 * time.Hour

func jsonSuffix(name string) bool {
	return len(name) > 5 && name[len(name)-5:] == ".json"
}
