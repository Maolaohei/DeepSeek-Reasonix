package peer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxLetterBytes caps a letter's JSON size; the envelope counts toward it.
const MaxLetterBytes = 32 * 1024

// ReceiptPollInterval is how often AwaitReceipt re-checks the mailbox.
const ReceiptPollInterval = 25 * time.Millisecond

// Mailbox is one session's inbox directory. Deposit is atomic (tmp+rename);
// Drain unlinks before parsing so a letter that crashes delivery is never
// redelivered and a malformed letter is dropped, not retried.
type Mailbox struct {
	dir string
}

// NewMailbox opens (creating) the inbox for addr under peersRoot.
func NewMailbox(peersRoot, addr string) (*Mailbox, error) {
	dir := MailboxDir(peersRoot, addr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Mailbox{dir: dir}, nil
}

// Dir returns the inbox directory path.
func (m *Mailbox) Dir() string { return m.dir }

// Deposit writes one letter atomically: readers only ever look at .json files
// and cannot observe a partial write.
func (m *Mailbox) Deposit(l Letter) error {
	if l.ID == "" {
		return errors.New("peer: letter id is required")
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if len(raw) > MaxLetterBytes {
		return fmt.Errorf("peer: letter exceeds %d bytes", MaxLetterBytes)
	}
	tmp := filepath.Join(m.dir, l.ID+".tmp")
	final := filepath.Join(m.dir, l.ID+".json")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Drain removes and returns all letters. Unlink happens before parse: a
// letter that crashes delivery must not be redelivered, and a malformed one
// is dropped the same way.
func (m *Mailbox) Drain() ([]Letter, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	var out []Letter
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(m.dir, e.Name())
		// Read into memory first, then unlink: a letter that crashes delivery
		// must not be redelivered, and a malformed one is dropped the same way.
		raw, err := os.ReadFile(path)
		_ = os.Remove(path)
		if err != nil {
			continue
		}
		var l Letter
		if err := json.Unmarshal(raw, &l); err != nil {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// PendingCount returns the number of letters waiting in the inbox.
func (m *Mailbox) PendingCount() int {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// HasLetter reports whether a specific letter id is still waiting.
func (m *Mailbox) HasLetter(id string) bool {
	_, err := os.Stat(filepath.Join(m.dir, id+".json"))
	return err == nil
}

// AwaitReceipt polls until the letter file disappears (the receiver drained
// it) or the timeout elapses. The caller decides whether to wait at all:
// only a live peer is worth blocking on.
func (m *Mailbox) AwaitReceipt(id string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !m.HasLetter(id) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(ReceiptPollInterval)
	}
}

// Remove deletes the whole inbox directory.
func (m *Mailbox) Remove() error {
	return os.RemoveAll(m.dir)
}
