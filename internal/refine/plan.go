package refine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/boundedllm"
	"reasonix/internal/event"
	"reasonix/internal/nilutil"
	"reasonix/internal/provider"
)

// Budgets for the /refine LLM pass. The proposal pass needs a large output
// budget (multi-edit JSON), while the auto-refine review gate stays small.
const (
	// DefaultTimeout bounds one refine call.
	DefaultTimeout = 120 * time.Second
	// DefaultMaxTokens caps the proposal completion. Proposals are a JSON edit
	// list (typically 1-3K tokens), so 8K is a generous ceiling that keeps the
	// in-turn refine tool call snappy on long tasks.
	DefaultMaxTokens = 8_000
	// DefaultMaxOutputBytes aborts the stream if the provider ignores MaxTokens.
	DefaultMaxOutputBytes = 256 * 1024
	// DefaultMaxSystemBytes covers the refine policy prompt.
	DefaultMaxSystemBytes = 16 * 1024
	// DefaultMaxTotalBytes covers system + conversation + overview + history.
	DefaultMaxTotalBytes = 512 * 1024
	// reviewGateMaxTokens caps the auto-refine review gate output.
	reviewGateMaxTokens = 4_096
	// reviewGateMaxOutputBytes aborts a runaway review-gate stream.
	reviewGateMaxOutputBytes = 16 * 1024
)

// Session runs the /refine model passes through boundedllm, isolated from the
// main conversation (no tools, no session history) — the same pattern as the
// Goal evaluator.
type Session struct {
	prov     provider.Provider
	pricing  *provider.Pricing
	modelRef string
	sink     event.Sink
	timeout  time.Duration
}

// NewSession returns a refine Session without usage accounting.
func NewSession(prov provider.Provider, pricing *provider.Pricing, modelRef string) *Session {
	return &Session{prov: prov, pricing: pricing, modelRef: strings.TrimSpace(modelRef)}
}

// NewSessionWithSink records usage under event.UsageSourceRefine.
func NewSessionWithSink(prov provider.Provider, pricing *provider.Pricing, modelRef string, sink event.Sink) *Session {
	return &Session{prov: prov, pricing: pricing, modelRef: strings.TrimSpace(modelRef), sink: sink}
}

// PlanOptions carries one refinement request's scope and instructions.
type PlanOptions struct {
	// Scope selects the target store (project by default).
	Scope Scope
	// Instructions are optional user/model guidance for the refinement.
	Instructions string
}

// Plan asks the model to derive a Proposal from the current trajectory and
// harness state. It never mutates any store; callers apply the result with
// ApplyProposal after re-reading the store.
func (s *Session) Plan(ctx context.Context, conversation string, overview string, history string, opts PlanOptions) (Proposal, error) {
	if s == nil || nilutil.IsNil(s.prov) {
		return Proposal{}, fmt.Errorf("refine planner unavailable")
	}
	scopeInstruction := scopePolicy(opts.Scope)
	userPrompt := []string{
		"<current_harness_state>\n" + overview + "\n</current_harness_state>",
		"<refinement_history>\n" + history + "\n</refinement_history>",
		"<conversation>\n" + conversation + "\n</conversation>",
		"<scope_policy>\n" + scopeInstruction + "\n</scope_policy>",
	}
	if instructions := strings.TrimSpace(opts.Instructions); instructions != "" {
		userPrompt = append(userPrompt, "<user_refine_instructions>\n"+instructions+"\n</user_refine_instructions>")
	}
	userPrompt = append(userPrompt, "Return only JSON edits. If no useful edit is justified, return an empty edits array with a rationale.")
	text, err := boundedllm.Call(ctx, boundedllm.Config{
		Provider:       s.prov,
		Pricing:        s.pricing,
		ModelRef:       s.modelRef,
		Sink:           s.sink,
		UsageSource:    event.UsageSourceRefine,
		Timeout:        s.timeoutOrDefault(),
		MaxTokens:      DefaultMaxTokens,
		MaxOutputBytes: DefaultMaxOutputBytes,
		MaxSystemBytes: DefaultMaxSystemBytes,
		MaxTotalBytes:  DefaultMaxTotalBytes,
	}, RefinementSystemPrompt, strings.Join(userPrompt, "\n\n"))
	if err != nil {
		return Proposal{}, err
	}
	proposal, perr := parseProposal(text)
	if perr != nil {
		return Proposal{}, fmt.Errorf("refine proposal parse: %w", perr)
	}
	return proposal, nil
}

// AutoRefineReview is the review gate's decision.
type AutoRefineReview struct {
	ShouldRefine bool
	Rationale    string
	Instructions string
}

// ReviewGate decides whether a checkpoint (e.g. after compaction) should run a
// local /refine. Output stays small and JSON-only.
func (s *Session) ReviewGate(ctx context.Context, conversation string, overview string, history string, reason string, turnsSinceLastReview int) (AutoRefineReview, error) {
	if s == nil || nilutil.IsNil(s.prov) {
		return AutoRefineReview{}, fmt.Errorf("refine reviewer unavailable")
	}
	userPrompt := strings.Join([]string{
		"<trigger>\n" + reason + "; " + fmt.Sprintf("%d", turnsSinceLastReview) + " assistant turns since the last auto-refine review\n</trigger>",
		"<current_harness_state>\n" + overview + "\n</current_harness_state>",
		"<refinement_history>\n" + history + "\n</refinement_history>",
		"<conversation>\n" + conversation + "\n</conversation>",
		"Return shouldRefine=true when the trajectory contains evidence useful to this session's future turns. Prefer local harness edits. Ask for global refinement only for durable cross-session lessons.",
	}, "\n\n")
	text, err := boundedllm.Call(ctx, boundedllm.Config{
		Provider:       s.prov,
		Pricing:        s.pricing,
		ModelRef:       s.modelRef,
		Sink:           s.sink,
		UsageSource:    event.UsageSourceRefine,
		Timeout:        s.timeoutOrDefault(),
		MaxTokens:      reviewGateMaxTokens,
		MaxOutputBytes: reviewGateMaxOutputBytes,
		MaxSystemBytes: DefaultMaxSystemBytes,
		MaxTotalBytes:  DefaultMaxTotalBytes,
	}, AutoRefineReviewSystemPrompt, userPrompt)
	if err != nil {
		return AutoRefineReview{}, err
	}
	return parseAutoRefineReview(text)
}

func (s *Session) timeoutOrDefault() time.Duration {
	if s.timeout > 0 {
		return s.timeout
	}
	return DefaultTimeout
}

// RefinementSystemPrompt is the fixed policy for the /refine proposal pass. It
// mirrors Prime Agent's continual-harness prompt, adapted to Reasonix's storage
// (markdown prompt notes, memory.Store, SKILL.md skills, profile subagents).
const RefinementSystemPrompt = `You are Reasonix's /refine continual harness subsystem.

Your job is to improve the editable continual harness state from the current trajectory.
Instead of summarizing the conversation, you emit precise Create, Update, or Delete edits
to reusable state: the persistent, editable set of prompt notes, memories, skills, and
subagent specs that lets the agent improve reusable behavior outside the token history.

Continual harness components:
- prompt: supplemental prompt notes only (markdown policy addendums). The base system
  prompt, REASONIX.md, and AGENTS.md are immutable and MUST NOT be rewritten.
- memory: durable facts, decisions, failures, preferences, and outcomes.
- skill: reusable procedures as SKILL.md playbooks.
- subagent: reusable delegation specs (purpose, instructions, when to invoke) that the
  agent can spawn with its task/subagent tooling.

Scope and persistence policy:
- The default scope is project (local to this workspace). Use it for task progress,
  active-task state, current-run coordination, temporary blockers, and project facts
  that should not affect other workspaces.
- A caller may explicitly request global scope. Global edits must be stable
  cross-session lessons, durable user preferences, reusable skills/subagents, or
  tool/environment facts that should affect future sessions everywhere.
- Use memory for declarative facts and preferences, skill for repeatable procedures,
  prompt for narrow behavioral policy addendums, and subagent for reusable delegation.
- Create or update the smallest relevant component. Prefer small evidence-backed edits.
  If prior refinements caused issues, propose updates or deletions for the faulty entries.
- Never edit source files directly.
- This build applies prompt edits. Memory, skill, and subagent edits are validated but
  reported as skipped; prefer prompt edits unless a memory/skill/subagent edit is clearly
  more appropriate, in which case still propose it and the skipped reason will surface.

Output JSON only with this exact shape:
{"summary":"one sentence","rationale":"why these edits are justified by trajectory evidence",
"expectedOutcome":"what should improve and how to validate it",
"edits":[{"action":"create|update|delete","kind":"prompt|memory|skill|subagent",
"id":"stable id for update/delete, optional for create","title":"required for create/update",
"content":"required for create/update","reason":"why this edit is useful"}]}`

// AutoRefineReviewSystemPrompt is the fixed policy for the review gate.
const AutoRefineReviewSystemPrompt = `You are Reasonix's automatic /refine review gate.

Decide whether this checkpoint should run /refine. Auto /refine writes project-level
continual harness state by default, so approve when the trajectory contains evidence
useful to this session's future turns.
Reject one-off noise, unsupported hypotheses, and transient tool outputs. Ask for global
refinement only for durable cross-session lessons or explicitly project-qualified lessons
likely to be reused in future sessions.

Return JSON only:
{"shouldRefine":true|false,"rationale":"short reason","instructions":"optional concise instructions for /refine if shouldRefine is true"}`

func scopePolicy(scope Scope) string {
	if scope == ScopeGlobal {
		return "Requested refinement scope: global. Only propose stable cross-session continual harness edits, durable user preferences, reusable skills/subagents, or explicitly project-qualified facts that should affect future sessions. Do not persist session-only progress, temporary blockers, or current-run coordination globally."
	}
	return "Requested refinement scope: project (local to this workspace). Prefer project harness edits for current task progress, temporary blockers, current-run coordination, and project facts. Global entries in the overview are read-only context: do not propose update or delete edits for them."
}

// RenderOverview renders the current harness state for the model: prompt notes
// (project + global), plus caller-supplied context lines (memory index, skills,
// profiles). Entries are listed with a scope prefix; bare ids must be used in
// edits.
func RenderOverview(notes []PromptNote, extras ...string) string {
	var b strings.Builder
	b.WriteString("prompt notes\n")
	if len(notes) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, n := range notes {
		title := oneLine(n.Title)
		if title == "" {
			title = n.ID
		}
		desc := oneLine(n.Description)
		if desc != "" {
			desc = " — " + desc
		}
		fmt.Fprintf(&b, "  [%s:%s](%s) v%d%s\n", n.Scope, n.ID, title, n.Version, desc)
	}
	for _, extra := range extras {
		if strings.TrimSpace(extra) == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(extra, "\n"))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderHistory renders recent refinement events for the model.
func RenderHistory(events []RefinementEvent) string {
	if len(events) == 0 {
		return "(none)"
	}
	var b strings.Builder
	start := 0
	if len(events) > 8 {
		start = len(events) - 8
	}
	for _, ev := range events[start:] {
		fmt.Fprintf(&b, "- %s %s changes=[%s] outcome=%s\n",
			ev.ID, ev.CreatedAt.UTC().Format(time.RFC3339), strings.Join(ev.Changes, ", "), oneLine(ev.Outcome))
	}
	return strings.TrimRight(b.String(), "\n")
}

// extractJSONObject pulls the first balanced {...} object out of a response,
// tolerating a reasoning preamble before it.
func extractJSONObject(text string) (string, error) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", fmt.Errorf("no JSON object in response")
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unterminated JSON object")
}

// parseProposal parses the model's JSON response into a Proposal.
func parseProposal(text string) (Proposal, error) {
	obj, err := extractJSONObject(text)
	if err != nil {
		return Proposal{}, err
	}
	var proposal Proposal
	if err := json.Unmarshal([]byte(obj), &proposal); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

// parseAutoRefineReview parses the review gate's JSON response.
func parseAutoRefineReview(text string) (AutoRefineReview, error) {
	obj, err := extractJSONObject(text)
	if err != nil {
		return AutoRefineReview{}, err
	}
	var raw struct {
		ShouldRefine bool   `json:"shouldRefine"`
		Rationale    string `json:"rationale"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(obj), &raw); err != nil {
		return AutoRefineReview{}, err
	}
	return AutoRefineReview{
		ShouldRefine: raw.ShouldRefine,
		Rationale:    strings.TrimSpace(raw.Rationale),
		Instructions: strings.TrimSpace(raw.Instructions),
	}, nil
}
