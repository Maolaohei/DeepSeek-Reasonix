package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/memory"
	"reasonix/internal/nilutil"
	"reasonix/internal/provider"
	"reasonix/internal/refine"
	"reasonix/internal/skill"
)

// refineTrajectoryChars bounds the conversation slice fed to /refine — the
// recent tail, matching how compaction selects its input.
const refineTrajectoryChars = 80_000

// refineSessionFor builds the bounded refine planner from the executor's
// provider. nil when no provider is available (refinement disabled).
func refineSessionFor(opts Options, sink event.Sink) *refine.Session {
	if opts.Executor == nil || nilutil.IsNil(opts.Executor.Provider()) {
		return nil
	}
	modelRef := strings.TrimSpace(opts.ModelRef)
	if modelRef == "" {
		modelRef = opts.Executor.ModelRef()
	}
	return refine.NewSessionWithSink(opts.Executor.Provider(), opts.Executor.Pricing(), modelRef, sink)
}

// Refine runs the /refine continual-harness command asynchronously and reports
// the outcome through notices (the /compact pattern). args may carry --global,
// --rollback <id>, and/or free-form instructions.
func (c *Controller) Refine(args string) {
	scope, rollbackID, instructions := parseRefineArgs(args)
	c.runRefineAsync(context.Background(), scope, rollbackID, instructions)
}

// runRefineAsync executes one refine pass off the event loop and reports via
// notices. Shared by the /refine command, the refine model tool, and the
// auto-refine gate.
func (c *Controller) runRefineAsync(ctx context.Context, scope refine.Scope, rollbackID, instructions string) {
	if c.refiner == nil {
		c.notice("refine: planner unavailable (no provider)")
		return
	}
	go func() {
		text, err := c.refineRun(ctx, scope, rollbackID, instructions)
		if err != nil {
			c.notice("refine: " + err.Error())
			return
		}
		c.notice(text)
	}()
}

// refineRun is the synchronous refine body: gather trajectory + harness state,
// ask the planner, apply the proposal (bridging memory/skill edits), and return
// a report string. Errors are returned; callers choose how to surface them.
func (c *Controller) refineRun(ctx context.Context, scope refine.Scope, rollbackID, instructions string) (string, error) {
	if c.refiner == nil {
		return "", fmt.Errorf("refine planner unavailable (no provider)")
	}
	c.mu.Lock()
	enabled := c.harnessEnabled
	c.mu.Unlock()
	if !enabled {
		return "", fmt.Errorf("Continual Harness is disabled ([harness] enabled = false)")
	}
	store := c.harnessStore(scope)
	if !store.Available() {
		return "", fmt.Errorf("harness store unavailable (no user config dir)")
	}
	if rollbackID != "" {
		return c.applyRollback(ctx, store, rollbackID, scope)
	}

	conversation := c.refineTrajectory()
	notes := append(append([]refine.PromptNote{}, c.harnessStore(refine.ScopeProject).ListNotes()...),
		c.harnessStore(refine.ScopeGlobal).ListNotes()...)
	overview := refine.RenderOverview(notes, c.refineExtras()...)
	history := refine.RenderHistory(c.harnessStore(scope).ListRefinements())

	// Capture a planning baseline for conflict detection: apply re-reads the
	// store, so a concurrent refinement since planning fails closed. Keyed by
	// scope:id so project and global notes never false-conflict.
	baseline := map[string]refine.PromptNote{}
	for _, n := range notes {
		baseline[string(n.Scope)+":"+n.ID] = n
	}

	proposal, err := c.refiner.Plan(ctx, conversation, overview, history, refine.PlanOptions{
		Scope:        scope,
		Instructions: instructions,
	})
	if err != nil {
		return "", err
	}
	results, err := refine.ApplyProposal(store, proposal, baseline, time.Now())
	if err != nil {
		return "", err
	}
	bridged, err := c.bridgeRefinement(proposal, results, scope)
	if err != nil {
		return "", err
	}
	return c.reportRefineResults(store, proposal, results, bridged, rollbackID, scope), nil
}

func (c *Controller) applyRollback(ctx context.Context, store refine.Store, rollbackID string, scope refine.Scope) (string, error) {
	for _, ev := range store.ListRefinements() {
		if ev.ID != rollbackID {
			continue
		}
		proposal := refine.RollbackProposal(ev)
		if len(proposal.Edits) == 0 {
			return "", fmt.Errorf("refine rollback: refinement %s has no rollable prompt edits", rollbackID)
		}
		results, err := refine.ApplyProposal(store, proposal, nil, time.Now())
		if err != nil {
			return "", err
		}
		// Rollback of bridged kinds is out of scope: memory edits already have
		// their own revision/restore machinery (/memory restore).
		return c.reportRefineResults(store, proposal, results, nil, rollbackID, scope), nil
	}
	return "", fmt.Errorf("refine rollback: refinement %s not found in this scope", rollbackID)
}

// reportRefineResults renders the applied edits and queues the turn-tail
// harness update so the changes apply this session (never touching the cached
// system prefix). Returns the report text; the caller surfaces it.
func (c *Controller) reportRefineResults(store refine.Store, proposal refine.Proposal, results []refine.AppliedEditResult, bridged []string, rollbackID string, scope refine.Scope) string {
	var applied, failed []string
	for _, r := range results {
		if r.Error != "" {
			failed = append(failed, fmt.Sprintf("%s %s:%s (%s)", r.Edit.Action, r.Edit.Kind, r.ID, r.Error))
			continue
		}
		if !r.Applied {
			failed = append(failed, fmt.Sprintf("%s %s:%s (unhandled kind)", r.Edit.Action, r.Edit.Kind, r.ID))
			continue
		}
		applied = append(applied, fmt.Sprintf("%s %s:%s", r.Edit.Action, r.Edit.Kind, r.ID))
	}
	applied = append(applied, bridged...)
	var b strings.Builder
	if rollbackID != "" {
		fmt.Fprintf(&b, "refine: rolled back %s\n", rollbackID)
	} else {
		fmt.Fprintf(&b, "refine: %s\n", oneLineRefine(proposal.Summary))
	}
	if len(applied) > 0 {
		b.WriteString("  applied: " + strings.Join(applied, ", ") + "\n")
	}
	if len(failed) > 0 {
		b.WriteString("  skipped: " + strings.Join(failed, ", ") + "\n")
	}
	if len(applied) == 0 && len(failed) == 0 {
		b.WriteString("  no edits proposed\n")
	}
	if len(applied) > 0 {
		// Queue a turn-tail note so the refined notes apply on the next turn
		// without touching the cache-stable system prefix.
		c.queueHarnessUpdate("Continual harness updated (" + string(scope) + "): " + strings.Join(applied, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// bridgeRefinement applies memory/skill/subagent edits proposed by the planner
// through their existing stores (the Continual Harness mapping). Prompt edits
// were already applied by refine.ApplyProposal. Returns the human-readable
// list of bridged edits, or an error when a bridge fails.
func (c *Controller) bridgeRefinement(proposal refine.Proposal, results []refine.AppliedEditResult, scope refine.Scope) ([]string, error) {
	var bridged []string
	for _, r := range results {
		if r.Applied || r.Error != "" {
			continue // prompt edits (applied) and failures are already reported
		}
		switch r.Edit.Kind {
		case refine.KindMemory:
			outcome, err := c.bridgeMemoryEdit(r.Edit, scope)
			if err != nil {
				return bridged, fmt.Errorf("memory bridge %s:%s: %w", r.Edit.Action, r.ID, err)
			}
			bridged = append(bridged, outcome)
		case refine.KindSkill, refine.KindSubagent:
			outcome, err := c.bridgeSkillEdit(r.Edit, scope)
			if err != nil {
				return bridged, fmt.Errorf("skill bridge %s:%s: %w", r.Edit.Action, r.ID, err)
			}
			bridged = append(bridged, outcome)
		}
	}
	return bridged, nil
}

// bridgeMemoryEdit maps a memory edit onto the memoryManager (the same write
// path as the remember/forget tools, so the in-session snapshot refreshes and a
// turn-tail note makes the change visible this session). Scope follows the
// refinement scope (project by default).
func (c *Controller) bridgeMemoryEdit(e refine.Edit, scope refine.Scope) (string, error) {
	name := strings.TrimSpace(e.ID)
	if name == "" {
		name = slugForMemory(e.Title)
	}
	switch e.Action {
	case refine.ActionDelete:
		// Guard: only delete an entry that actually exists (project first, then
		// global — same resolution order as the forget tool).
		if set := c.Memory(); set != nil {
			exists := false
			for _, f := range set.Store.ListAll() {
				if f.Name == name {
					exists = true
					break
				}
			}
			if !exists {
				return "", fmt.Errorf("memory %q not found", name)
			}
		}
		if err := c.ForgetMemory(name); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s memory:%s", e.Action, name), nil
	default:
		factScope := memory.FactScopeProject
		if scope == refine.ScopeGlobal {
			factScope = memory.FactScopeGlobal
		}
		if _, err := c.SaveMemory(memory.Memory{
			Name:        name,
			Title:       firstNonEmptyString(e.Title, name),
			Description: firstNonEmptyString(e.Reason, e.Title, name),
			Type:        memory.TypeProject,
			Scope:       factScope,
			Body:        e.Content,
		}); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s memory:%s", e.Action, name), nil
	}
}

// bridgeSkillEdit maps a skill/subagent edit onto skill.Store: create writes a
// SKILL.md (runAs: subagent for subagent specs), update rewrites it, delete
// removes it. The skill store re-discovers on every List, so the change is
// visible to management surfaces immediately and to the prompt index on the
// next session.
func (c *Controller) bridgeSkillEdit(e refine.Edit, scope refine.Scope) (string, error) {
	store := c.skills.writer()
	if store == nil {
		return "", fmt.Errorf("skill store unavailable")
	}
	name := strings.TrimSpace(e.ID)
	if name == "" {
		name = slugForMemory(e.Title)
	}
	skillScope := skill.ScopeProject
	if scope == refine.ScopeGlobal {
		skillScope = skill.ScopeGlobal
	}
	switch e.Action {
	case refine.ActionDelete:
		if err := store.Delete(name, skillScope); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s:%s", e.Action, e.Kind, name), nil
	default:
		body := e.Content
		if e.Kind == refine.KindSubagent && !strings.Contains(body, "runAs:") {
			body = "---\nrunAs: subagent\n---\n" + body
		}
		if _, err := store.CreateWithContent(name, skillScope, body); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s:%s", e.Action, e.Kind, name), nil
	}
}

// slugForMemory derives a legal memory/skill name from a title.
func slugForMemory(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '_' || r == '-' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "note"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	return strings.Trim(name, "-")
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func oneLineRefine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// harnessStore resolves the store for a scope. The user dir mirrors memory's
// config.MemoryUserDir().
func (c *Controller) harnessStore(scope refine.Scope) refine.Store {
	userDir := c.memoryUserDir()
	if scope == refine.ScopeGlobal {
		return refine.GlobalStoreFor(userDir)
	}
	return refine.StoreFor(userDir, c.workspaceRootForStore())
}

// memoryUserDir resolves the state root used by the memory store.
func (c *Controller) memoryUserDir() string {
	return config.MemoryUserDir()
}

// workspaceRootForStore returns the workspace root used for harness store
// scoping: the session's workspace root when known, else the CWD.
func (c *Controller) workspaceRootForStore() string {
	if c.workspaceRoot != "" {
		return c.workspaceRoot
	}
	return "."
}

// refineTrajectory serializes the recent session messages into a bounded text
// slice for the planner.
func (c *Controller) refineTrajectory() string {
	if c.executor == nil {
		return ""
	}
	sess := c.executor.Session()
	if sess == nil {
		return ""
	}
	return serializeTrajectory(sess.Snapshot(), refineTrajectoryChars)
}

// serializeTrajectory renders messages as a bounded plain-text transcript.
func serializeTrajectory(msgs []provider.Message, maxChars int) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.LocalOnly {
			continue
		}
		switch m.Role {
		case provider.RoleUser:
			fmt.Fprintf(&b, "\n## user\n%s\n", m.Content)
		case provider.RoleAssistant:
			text := strings.TrimSpace(m.Content)
			if text != "" {
				fmt.Fprintf(&b, "\n## assistant\n%s\n", text)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "\n## tool_call %s\n%s\n", tc.Name, tc.Arguments)
			}
		case provider.RoleTool:
			fmt.Fprintf(&b, "\n## tool_result %s\n%s\n", m.Name, clipToolResult(m.Content))
		}
		if b.Len() > maxChars {
			b.Reset()
			b.WriteString("…(earlier context truncated)…")
		}
	}
	return b.String()
}

func clipToolResult(content string) string {
	const maxToolResult = 2_000
	if len(content) <= maxToolResult {
		return content
	}
	return content[:maxToolResult] + "…"
}

// refineExtras renders the caller-supplied harness context lines for the
// planner overview: memory index and skill names.
func (c *Controller) refineExtras() []string {
	var extras []string
	if set := c.Memory(); set != nil {
		facts := set.Store.ListAll()
		if len(facts) == 0 {
			extras = append(extras, "memories: (none)")
		} else {
			var lines []string
			for _, f := range facts {
				desc := oneLineRefine(f.Description)
				if desc == "" {
					desc = f.Name
				}
				lines = append(lines, fmt.Sprintf("- [%s:%s] %s", f.Scope, f.Name, desc))
			}
			extras = append(extras, "memories:\n"+strings.Join(lines, "\n"))
		}
	}
	return extras
}

// parseRefineArgs splits /refine arguments: --global toggles the global store,
// --rollback <id> selects a recorded refinement to invert, and everything else
// is free-form instructions.
func parseRefineArgs(args string) (scope refine.Scope, rollbackID, instructions string) {
	scope = refine.ScopeProject
	fields := strings.Fields(args)
	var rest []string
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--global", "-g":
			scope = refine.ScopeGlobal
		case "--rollback", "-r":
			if i+1 < len(fields) {
				rollbackID = fields[i+1]
				i++
			}
		default:
			rest = append(rest, fields[i])
		}
	}
	instructions = strings.TrimSpace(strings.Join(rest, " "))
	return scope, rollbackID, instructions
}

func (c *Controller) queueHarnessUpdate(note string) {
	c.mu.Lock()
	c.harnessNotes = append(c.harnessNotes, note)
	c.mu.Unlock()
}
