// Package peertool exposes peer-to-peer session messaging to the model:
// list_peers shows other sessions with their presence, message_peer sends a
// plain-text letter to one of them. Mirrors pi-peer's two tools.
package peertool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"reasonix/internal/peer"
)

const receiptTimeout = 1500 * time.Millisecond

// listPeersTool implements list_peers.
type listPeersTool struct {
	peersRoot string
	selfAddr  string
}

// NewListPeersTool creates a tool listing registered peer sessions.
func NewListPeersTool(peersRoot, selfAddr string) *listPeersTool {
	return &listPeersTool{peersRoot: peersRoot, selfAddr: selfAddr}
}

func (t *listPeersTool) Name() string   { return "list_peers" }
func (t *listPeersTool) ReadOnly() bool { return true }
func (t *listPeersTool) Description() string {
	return "List other Reasonix sessions on this machine with their status (live/stalled/offline), working directory, and pending message count. Use it to discover which peer to message."
}
func (t *listPeersTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func (t *listPeersTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	reg, err := peer.NewRegistry(t.peersRoot)
	if err != nil {
		return "", err
	}
	recs := reg.List()
	sort.Slice(recs, func(i, j int) bool { return recs[i].DisplayName < recs[j].DisplayName })
	var b strings.Builder
	fmt.Fprintf(&b, "# Peer Sessions (%d)\n\n", len(recs))
	for _, rec := range recs {
		marker := " "
		if rec.Addr == t.selfAddr {
			marker = "*"
		}
		pres := reg.Presence(rec.Addr, ProcessAlive)
		name := rec.DisplayName
		if name == "" {
			name = rec.Addr
		}
		mail, err := peer.NewMailbox(t.peersRoot, rec.Addr)
		pend := 0
		if err == nil {
			pend = mail.PendingCount()
		}
		fmt.Fprintf(&b, "- %s%s: %s (cwd: %s, mail: %d)\n", marker, name, pres, rec.CWD, pend)
	}
	return b.String(), nil
}

// messagePeerTool implements message_peer.
type messagePeerTool struct {
	peersRoot string
	selfAddr  string
	selfName  string
}

// NewMessagePeerTool creates a tool sending letters to a peer session.
func NewMessagePeerTool(peersRoot, selfAddr, selfName string) *messagePeerTool {
	return &messagePeerTool{peersRoot: peersRoot, selfAddr: selfAddr, selfName: selfName}
}

func (t *messagePeerTool) Name() string   { return "message_peer" }
func (t *messagePeerTool) ReadOnly() bool { return false }
func (t *messagePeerTool) Description() string {
	return "Send a plain-text message to another Reasonix session on this machine. The peer's name or address prefix from list_peers goes in `peer`; the message is delivered as guidance only, with no authority over the peer."
}
func (t *messagePeerTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"peer":{"type":"string","description":"Display name or address prefix of the target session (see list_peers)"},"text":{"type":"string","description":"Plain text message"},"requireAck":{"type":"boolean","description":"Wait for the peer to read the message (only when it is live)"}},"required":["peer","text"],"additionalProperties":false}`)
}

func (t *messagePeerTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Peer       string `json:"peer"`
		Text       string `json:"text"`
		RequireAck bool   `json:"requireAck"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Peer) == "" || strings.TrimSpace(in.Text) == "" {
		return "error: peer and text are required.", nil
	}
	reg, err := peer.NewRegistry(t.peersRoot)
	if err != nil {
		return "", err
	}
	addr, name, err := resolvePeer(reg, in.Peer)
	if err != nil {
		return "error: " + err.Error(), nil
	}
	if addr == t.selfAddr {
		return "error: refusing to message yourself; the message would loop.", nil
	}
	mb, err := peer.NewMailbox(t.peersRoot, addr)
	if err != nil {
		return "", err
	}
	letter := peer.Letter{
		ID:         fmt.Sprintf("%d-%s", time.Now().UnixMilli(), randSuffix()),
		Sender:     t.selfAddr,
		SenderName: t.selfName,
		Text:       in.Text,
		CreatedAt:  time.Now().UnixMilli(),
	}
	if err := mb.Deposit(letter); err != nil {
		return "error: " + err.Error(), nil
	}
	// Receipts are only worth waiting for against a live peer; offline and
	// stalled peers get a queued answer immediately (pi-peer registry.ts).
	if in.RequireAck && reg.Presence(addr, ProcessAlive) == peer.Live {
		if mb.AwaitReceipt(letter.ID, receiptTimeout) {
			return "delivered to " + name + ".", nil
		}
		return "queued for " + name + " (not read yet); the peer will see it on its next turn.", nil
	}
	return "queued for " + name + ".", nil
}

// resolvePeer maps a display name or unique address prefix to an address.
func resolvePeer(reg *peer.Registry, ref string) (addr, name string, err error) {
	recs := reg.List()
	ref = strings.ToLower(strings.TrimSpace(ref))
	var matches []peer.Record
	for _, rec := range recs {
		if strings.EqualFold(rec.DisplayName, ref) || strings.HasPrefix(rec.Addr, ref) || strings.HasPrefix(rec.Addr, strings.ToLower(ref)) {
			matches = append(matches, rec)
		}
	}
	switch len(matches) {
	case 0:
		return "", "", fmt.Errorf("no peer session matches %q; run list_peers to see them", ref)
	case 1:
		display := matches[0].DisplayName
		if display == "" {
			display = matches[0].Addr
		}
		return matches[0].Addr, display, nil
	default:
		return "", "", fmt.Errorf("%q is ambiguous (%d matches); use a longer address prefix", ref, len(matches))
	}
}

// ProcessAlive reports whether a pid is currently running, best-effort. It is
// a variable so tests can script liveness.
var ProcessAlive = processAlive

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		// Signal 0 is unsupported; FindProcess always succeeds there. Assume
		// alive and let the heartbeat age bound presence.
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes existence without delivering. EPERM means the process
	// exists under another user.
	if err := proc.Signal(syscall.Signal(0)); err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "not supported") || strings.Contains(msg, "not implemented")
}

var randSuffix = func() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xffffff)
}
