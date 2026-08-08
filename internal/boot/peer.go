package boot

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/peer"
	"reasonix/internal/tool"
	"reasonix/internal/tool/peertool"
)

// PeerPollInterval is how often the inbound mailbox is drained.
const PeerPollInterval = 2 * time.Second

// PeerSweepInterval is how often dead peer records and expired mail are
// swept; the registry's mail-first rules keep undelivered letters alive.
const PeerSweepInterval = time.Hour

// registerPeerTools adds list_peers and message_peer to the registry; the
// self address derives from the workspace root and the build's session id.
func registerPeerTools(reg *tool.Registry, root, sessionID string) {
	peersRoot := filepath.Join(config.SupportDir(), "peers")
	reg.Add(peertool.NewListPeersTool(peersRoot, peer.AddressFor(root, sessionID)))
	reg.Add(peertool.NewMessagePeerTool(peersRoot, peer.AddressFor(root, sessionID), filepath.Base(root)))
}

// peerSessionEnabled reports whether the build participates in peer
// messaging: off only in token-economy mode (the lean surface).
func peerSessionEnabled(opts Options, tokenEconomy bool) bool {
	return !tokenEconomy
}

// startPeerSessionForBuild registers the session as a live peer and starts
// heartbeat + inbound-letter injection when the build participates.
func startPeerSessionForBuild(ctx context.Context, opts Options, tokenEconomy bool, root, sessionID string, ctrl *control.Controller) {
	if !peerSessionEnabled(opts, tokenEconomy) {
		return
	}
	startPeerSession(ctx, filepath.Join(config.SupportDir(), "peers"), peer.AddressFor(root, sessionID), filepath.Base(root), root, sessionID, ctrl)
}

// startPeerSession registers this session in the peer registry, heartbeats
// it while the build context lives, and forwards inbound letters into the
// controller as steer guidance. The sweep rules retire records later; there
// is no shutdown hook because a boot context ending is not a process exit.
func startPeerSession(ctx context.Context, peersRoot, addr, display, cwd, sessionID string, ctrl *control.Controller) {
	reg, err := peer.NewRegistry(peersRoot)
	if err != nil {
		slog.Warn("peer: registry unavailable", "err", err)
		return
	}
	rec := peer.Record{
		Addr:        addr,
		CWD:         cwd,
		SessionID:   sessionID,
		PID:         os.Getpid(),
		DisplayName: display,
	}
	if err := reg.RecordSession(rec); err != nil {
		slog.Warn("peer: record session", "err", err)
		return
	}
	mb, err := peer.NewMailbox(peersRoot, addr)
	if err != nil {
		slog.Warn("peer: mailbox unavailable", "err", err)
		return
	}
	guard := newPeerInjectionGuard()
	go func() {
		heartbeat := time.NewTicker(peer.HeartbeatInterval)
		poll := time.NewTicker(PeerPollInterval)
		sweep := time.NewTicker(PeerSweepInterval)
		defer heartbeat.Stop()
		defer poll.Stop()
		defer sweep.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				if err := reg.Heartbeat(addr); err != nil {
					slog.Debug("peer: heartbeat", "err", err)
				}
			case <-poll.C:
				injectPeerMail(mb, ctrl, guard)
			case <-sweep.C:
				if err := reg.Sweep(); err != nil {
					slog.Debug("peer: sweep", "err", err)
				}
			}
		}
	}()
}

// peerInjectionGuard dedupes inbound letters per sender so an A→B→A loop
// cannot re-inject the same guidance forever; the boundary declaration is
// the semantic guard, this is the structural one.
type peerInjectionGuard struct {
	mu     sync.Mutex
	recent map[string]map[string]time.Time // sender -> text -> last injected
}

func newPeerInjectionGuard() *peerInjectionGuard {
	return &peerInjectionGuard{recent: make(map[string]map[string]time.Time)}
}

func (g *peerInjectionGuard) allow(sender, text string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	seen := g.recent[sender]
	if seen == nil {
		seen = make(map[string]time.Time)
		g.recent[sender] = seen
	}
	if last, ok := seen[text]; ok && now.Sub(last) < peerInjectionDedupeWindow {
		return false
	}
	cutoff := now.Add(-peerInjectionDedupeWindow)
	for t, at := range seen {
		if at.Before(cutoff) {
			delete(seen, t)
		}
	}
	seen[text] = now
	return true
}

// peerInjectionDedupeWindow is the per-sender repeat window for inbound mail.
const peerInjectionDedupeWindow = 10 * time.Second

// injectPeerMail drains one batch of letters and steers them into the running
// turn. A rejected steer (no active turn) is queued as an unapplied steer and
// surfaces on the next real user message, so the letter is never lost.
func injectPeerMail(mb *peer.Mailbox, ctrl *control.Controller, guard *peerInjectionGuard) {
	letters, err := mb.Drain()
	if err != nil || len(letters) == 0 {
		return
	}
	now := time.Now()
	for _, l := range letters {
		if !guard.allow(l.Sender, l.Text, now) {
			continue
		}
		text := peer.FormatInbound(l.SenderName, l.Text)
		if !ctrl.TrySteer(text) {
			// RecordUnappliedSteer: kept for the next real user turn, not
			// injected as a synthetic user message.
			ctrl.Steer(text)
		}
	}
}
