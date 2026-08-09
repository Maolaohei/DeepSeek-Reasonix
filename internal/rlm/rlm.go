// Package rlm implements a guarded recursive task-decomposition engine:
// a planner decides whether a task is solved directly or split into
// subtasks, subtasks run concurrently, and a synthesizer merges their
// outputs into the parent answer. Guards (depth, node budget, branching,
// lineage cycle detection) keep recursion bounded; a failing child never
// cascades. Execution is injected (Planner/Solver/Synthesizer interfaces),
// so tests drive the engine with mocks and the agent wires real providers.
// Patterns follow pi-rlm.
package rlm

import (
	"context"
	"fmt"
	"sync/atomic"
)

// Mode controls how much freedom the planner has.
type Mode string

const (
	ModeAuto      Mode = "auto"
	ModeDecompose Mode = "decompose"
	ModeSolve     Mode = "solve"
)

// Status is a node's outcome.
type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusSkipped   Status = "skipped"
)

// Task is one node in the decomposition tree.
type Task struct {
	ID   string
	Text string
}

// Result is one node's outcome, including its decomposed children.
type Result struct {
	Task     Task
	Status   Status
	Output   string   // solver or synthesizer output
	Reason   string   // forced-solve reason or failure detail
	Children []Result // non-empty only when the node decomposed
}

// Decision is the planner's verdict for one node.
type Decision struct {
	Action   string   // "solve" or "decompose"
	Subtasks []string // decompose only
	Reason   string
}

// PlanContext carries the guards the planner must respect.
type PlanContext struct {
	Depth          int
	MaxDepth       int
	RemainingNodes int
	Lineage        []string // normalized ancestor task texts
	Mode           Mode
}

// Planner decides solve vs decompose for a node.
type Planner interface {
	Decide(ctx context.Context, task Task, pc PlanContext) (Decision, error)
}

// Solver executes one task and returns its answer text.
type Solver interface {
	Solve(ctx context.Context, task Task) (string, error)
}

// ChildResult is one child's output fed to the synthesizer.
type ChildResult struct {
	Task   Task
	Status Status
	Output string
	Reason string
}

// Synthesizer merges child outputs into the parent answer.
type Synthesizer interface {
	Synthesize(ctx context.Context, task Task, children []ChildResult) (string, error)
}

// Event is an engine lifecycle notification for logging or live views.
type Event struct {
	Kind   string // run_start, node_start, node_decompose, node_end, node_failed, node_cancelled, node_skipped
	TaskID string
	Depth  int
	Text   string
	Detail string
}

// Defaults for the guard rails when Options leaves them zero.
const (
	DefaultMaxDepth     = 3
	DefaultMaxNodes     = 8
	DefaultMaxBranching = 4
	DefaultConcurrency  = 4
)

// Options configures one engine run.
type Options struct {
	Mode         Mode
	MaxDepth     int
	MaxNodes     int
	MaxBranching int
	Concurrency  int
	Planner      Planner
	Solver       Solver
	Synthesizer  Synthesizer
	OnEvent      func(Event)
}

// Engine runs one decomposition tree.
type Engine struct {
	opts Options
}

// New validates and freezes the options.
func New(opts Options) (*Engine, error) {
	if opts.Planner == nil {
		return nil, fmt.Errorf("rlm: planner is required")
	}
	if opts.Solver == nil {
		return nil, fmt.Errorf("rlm: solver is required")
	}
	if opts.Synthesizer == nil {
		return nil, fmt.Errorf("rlm: synthesizer is required")
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = DefaultMaxNodes
	}
	if opts.MaxBranching <= 0 {
		opts.MaxBranching = DefaultMaxBranching
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = DefaultConcurrency
	}
	return &Engine{opts: opts}, nil
}

// Run decomposes and solves the root task, returning the tree root result.
func (e *Engine) Run(ctx context.Context, root Task) (Result, error) {
	e.emit(Event{Kind: "run_start", TaskID: root.ID, Text: root.Text})
	st := &runState{budget: int64(e.opts.MaxNodes)}
	res := e.runNode(ctx, root, nil, 0, st)
	e.emit(Event{Kind: "node_end", TaskID: root.ID, Detail: string(res.Status)})
	return res, nil
}

// runState tracks the node budget across the whole tree. Nodes are counted
// atomically because sibling subtrees run concurrently.
type runState struct {
	nodes  atomic.Int64
	budget int64
}

// ErrCancelled reports a context cancellation that aborted the tree.
var ErrCancelled = fmt.Errorf("rlm: cancelled")

func (e *Engine) emit(ev Event) {
	if e.opts.OnEvent != nil {
		e.opts.OnEvent(ev)
	}
}
