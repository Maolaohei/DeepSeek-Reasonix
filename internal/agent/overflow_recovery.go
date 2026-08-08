package agent

import (
	"context"
	"errors"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// overflowRecover recognizes a context-overflow provider error, forces one
// compaction, and reports whether the turn may be retried. Bounded: the
// runLoopState flag guarantees a single compact-and-retry per turn, and a
// failed compaction (still over the limit) falls through to the normal error
// path instead of looping.
func (a *Agent) overflowRecover(ctx context.Context, state *runLoopState, err error) bool {
	if state.overflowCompactionDone || !provider.IsContextOverflowError(err) {
		return false
	}
	state.overflowCompactionDone = true
	if perr := a.contextPreflight(ctx, CompactionTriggerOverflow); perr != nil {
		if !errors.Is(perr, ErrCompactionRequired) {
			a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "Context overflow recovery compaction failed.", Detail: perr.Error()})
		}
		return false
	}
	a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: "Context overflow detected; compacted and retrying once."})
	return true
}
