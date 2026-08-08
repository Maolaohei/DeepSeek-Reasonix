// Package peer implements mailbox-style messaging between Reasonix sessions
// on the same machine: a session address derives from the working directory
// and session ID, letters are files (atomic .tmp+rename deposit, unlink-before-
// parse drain), and a registry tracks presence with heartbeats. No daemon, no
// network: the filesystem is the protocol. Patterns follow pi-peer.
package peer

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// Letter is one peer message: plain text only, never history or files, so a
// letter cannot smuggle state between sessions.
type Letter struct {
	ID         string `json:"id"`
	Sender     string `json:"sender"`
	SenderName string `json:"senderName,omitempty"`
	Text       string `json:"text"`
	CreatedAt  int64  `json:"createdAt"`
}

// Presence is the registry's view of a peer session.
type Presence string

const (
	Live    Presence = "live"
	Stalled Presence = "stalled"
	Offline Presence = "offline"
)

// Record is the persisted presence entry for one session address.
type Record struct {
	Addr        string `json:"addr"`
	CWD         string `json:"cwd"`
	SessionID   string `json:"sessionId"`
	PID         int    `json:"pid,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	BeatAt      int64  `json:"beatAt"`
}

// AddressFor derives the stable mailbox address of a session. The address
// belongs to the session, not the process: resuming a session keeps its
// mailbox and queued letters.
func AddressFor(cwd, sessionID string) string {
	sum := sha256.Sum256([]byte(cwd + "\x00" + sessionID))
	return hex.EncodeToString(sum[:])[:12]
}

// MailboxDir returns the inbox directory for an address under the peers root.
func MailboxDir(peersRoot, addr string) string {
	return filepath.Join(peersRoot, addr)
}

// RegistryDir returns the presence-record directory under the peers root.
func RegistryDir(peersRoot string) string {
	return filepath.Join(peersRoot, "registry")
}
