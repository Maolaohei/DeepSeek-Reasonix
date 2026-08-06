package control

import (
	"context"
	"log/slog"
	"time"

	"reasonix/internal/refine"
)

// onAgentTurnDone is the turn-completion hook: every N completed user turns
// (Prime's turn_interval trigger, default 25) it runs the same review gate, so
// short sessions and task-boundary-heavy workloads learn automatically even
// when compaction never fires.
func (c *Controller) onAgentTurnDone(turnCount int64) {
	if c == nil || !c.harnessAutoRefine || c.refiner == nil {
		return
	}
	if c.autoRefineIntervalTurns <= 0 || turnCount%int64(c.autoRefineIntervalTurns) != 0 {
		return
	}
	c.maybeAutoRefineGate("turn_interval; turns=" + itoa64(turnCount))
}

// maybeAutoRefine is the compaction-done hook: after each successful compaction
// (auto or manual) it runs the review gate — a small independent LLM pass that
// decides whether the trajectory contains reusable lessons — and, when it
// approves, applies a project-scoped refinement automatically. This is the
// "integrated into the long-task machinery" path: the harness learns without a
// manual /refine. Throttled to at most one automatic refinement per
// autoRefineInterval.
func (c *Controller) maybeAutoRefine(trigger string) {
	c.maybeAutoRefineGate("compaction; trigger=" + trigger)
}

// maybeAutoRefineGate is the shared review-gate entry: throttle check, then an
// async ReviewGate + project-scoped refine. Compact and turn_interval triggers
// share the throttle so a burst of both cannot double-run.
func (c *Controller) maybeAutoRefineGate(reason string) {
	if c == nil || !c.harnessAutoRefine || c.refiner == nil {
		return
	}
	c.mu.Lock()
	now := time.Now()
	if !c.lastAutoRefine.IsZero() && now.Sub(c.lastAutoRefine) < c.harnessAutoRefineInterval {
		c.mu.Unlock()
		return
	}
	c.lastAutoRefine = now // reserve the slot; a failure still waits for the next interval
	c.mu.Unlock()
	go func() {
		// Never run after the controller is torn down: the review+apply pass
		// may outlive the session, and harness edits after close would be
		// zombie mutations.
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		ctx := context.Background()
		conversation := c.refineTrajectoryBounded(refineReviewChars)
		notes := append(append([]refine.PromptNote{}, c.harnessStore(refine.ScopeProject).ListNotes()...),
			c.harnessStore(refine.ScopeGlobal).ListNotes()...)
		overview := refine.RenderOverview(notes, c.refineExtras()...)
		history := refine.RenderHistory(c.harnessStore(refine.ScopeProject).ListRefinements())
		review, err := c.refiner.ReviewGate(ctx, conversation, overview, history, reason, 0)
		if err != nil {
			// Automatic background refinement must never interrupt the user:
			// log the failure, leave the harness untouched, and let the next
			// interval/compaction retry.
			slog.Warn("auto-refine review failed", "err", err)
			return
		}
		if !review.ShouldRefine {
			return
		}
		text, err := c.refineRun(ctx, refine.ScopeProject, "", review.Instructions)
		if err != nil {
			slog.Warn("auto-refine apply failed", "err", err)
			return
		}
		// Success is worth surfacing quietly as a notice (the harness learned
		// something), but failures stay in the log.
		c.notice(text)
	}()
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
