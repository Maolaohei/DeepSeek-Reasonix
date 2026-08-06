// Package refine implements the Continual Harness: a durable, model-editable
// supplemental state layer inspired by Prime Agent's /refine. It lets the agent
// persist small, evidence-backed lessons (behavioral prompt notes, memories,
// skills, subagent specs) outside the token history, without ever rewriting the
// cache-stable base system prompt. Edits are recorded with before/after
// snapshots so they can be rolled back.
//
// The four harness kinds map onto existing Reasonix systems: prompt notes live
// in a new lightweight store under the state root (this package), memory edits
// reuse memory.Store, and skill/subagent edits reuse skill.Store. Only the
// prompt kind has its own storage here; the others are bridges.
package refine
