package refine

import (
	"fmt"
	"sort"
	"strings"
)

// Prompt-injection budgets for the cache-stable system prefix. Like Prime
// Agent, only compact summaries enter the prefix — full note content is
// available on disk and to /refine — so the prefix stays light and stable.
const (
	// maxSummaryContentChars caps one note's content preview in the prefix.
	maxSummaryContentChars = 180
	// maxPrefixNotes caps how many notes render into the prefix per scope.
	maxPrefixNotes = 24
)

// GuidanceBlock is the fixed Continual Harness usage guide injected into the
// cache-stable system prefix whenever the harness is enabled — even before any
// notes exist. It is the Reasonix analogue of Prime Agent's "when to call
// refine.run()" guidance in the system prompt: the model learns the mechanism
// exists and when to use it, with no user configuration or REASONIX.md needed.
// Byte-stable: it never varies, so the prefix only changes when notes do.
const GuidanceBlock = `# Continual Harness

Reasonix keeps a Continual Harness: a durable, editable supplemental state layer
where you can persist reusable lessons. The base system prompt, REASONIX.md, and
AGENTS.md are immutable — never rewrite them. Harness entries are the only
editable supplement, and they load into your context automatically on later
turns and sessions.

Call the refine tool when:
- a repeated failure shows a pattern worth avoiding;
- a reusable tactic, procedure, or delegation role emerges;
- the user corrects your behavior in a way that should persist;
- a long task reaches a checkpoint with lessons worth keeping.

Keep edits small and evidence-backed. Default to project scope (this workspace);
use scope=global only for stable cross-workspace lessons.

## Continual Harness State

Supplemental prompt notes refined in past sessions. They are compact summaries,
not full descriptions — use them as behavioral-policy hints.`

// Compose appends the supplemental prompt-notes summary to a system prompt.
// Notes from every store (project first, then global) fold in once, sorted by
// id, as part of the durable cache-stable prefix. The caller (boot) injects
// GuidanceBlock separately so the guidance appears even when no notes exist.
// An empty harness contributes nothing, so the prefix is byte-identical to a
// harness-free session.
func Compose(sysPrompt string, stores ...Store) string {
	var notes []PromptNote
	for _, st := range stores {
		notes = append(notes, st.ListNotes()...)
	}
	if len(notes) == 0 {
		return sysPrompt
	}
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].Scope != notes[j].Scope {
			return notes[i].Scope < notes[j].Scope
		}
		return notes[i].ID < notes[j].ID
	})
	var b strings.Builder
	b.WriteString("\n")
	rendered := 0
	for _, n := range notes {
		if rendered >= maxPrefixNotes {
			b.WriteString(fmt.Sprintf("- +%d more notes (see /refine overview)\n", len(notes)-rendered))
			break
		}
		title := oneLine(n.Title)
		if title == "" {
			title = n.ID
		}
		preview := compactText(n.Content, maxSummaryContentChars)
		fmt.Fprintf(&b, "- [%s:%s] %s (v%d): %s\n", n.Scope, n.ID, title, n.Version, preview)
		rendered++
	}
	sysPrompt += strings.TrimRight(b.String(), "\n")
	return sysPrompt
}

// compactText collapses whitespace and truncates to a bound, keeping the last
// word intact when possible.
func compactText(text string, max int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if len(flat) <= max {
		return flat
	}
	cut := flat[:max]
	if at := strings.LastIndexByte(cut, ' '); at > max/2 {
		cut = cut[:at]
	}
	return strings.TrimSpace(cut) + "…"
}
