package rlm

import (
	"context"
	"fmt"
	"sync"
)

// runNode executes one tree node: guards first, then planner decide, then
// either solve directly or decompose into concurrent children and synthesize.
// A failing child never cascades; the parent fails only when every child
// failed (pi-rlm engine.ts:266-298).
func (e *Engine) runNode(ctx context.Context, task Task, lineage []string, depth int, st *runState) Result {
	st.nodes.Add(1)
	if err := ctx.Err(); err != nil {
		return Result{Task: task, Status: StatusCancelled, Reason: ErrCancelled.Error()}
	}
	e.emit(Event{Kind: "node_start", TaskID: task.ID, Depth: depth, Text: task.Text})

	if reason := forcedSolveReason(task.Text, lineage, depth, e.opts.MaxDepth, st); reason != "" {
		return e.solveNode(ctx, task, reason)
	}
	if e.opts.Mode == ModeSolve {
		return e.solveNode(ctx, task, "mode=solve")
	}

	pc := PlanContext{
		Depth:          depth,
		MaxDepth:       e.opts.MaxDepth,
		RemainingNodes: int(st.budget - st.nodes.Load()),
		Lineage:        lineage,
		Mode:           e.opts.Mode,
	}
	dec, err := e.opts.Planner.Decide(ctx, task, pc)
	if err != nil {
		return Result{Task: task, Status: StatusFailed, Reason: "planner error: " + err.Error()}
	}
	if dec.Action != "decompose" {
		return e.solveNode(ctx, task, "planner chose solve")
	}
	return e.decomposeNode(ctx, task, dec, lineage, depth, st)
}

func (e *Engine) solveNode(ctx context.Context, task Task, reason string) Result {
	out, err := e.opts.Solver.Solve(ctx, task)
	if err != nil {
		e.emit(Event{Kind: "node_failed", TaskID: task.ID, Detail: err.Error()})
		return Result{Task: task, Status: StatusFailed, Reason: err.Error()}
	}
	e.emit(Event{Kind: "node_end", TaskID: task.ID, Detail: "solved"})
	return Result{Task: task, Status: StatusCompleted, Output: out, Reason: reason}
}

func (e *Engine) decomposeNode(ctx context.Context, task Task, dec Decision, lineage []string, depth int, st *runState) Result {
	subtasks := sanitizeSubtasks(dec.Subtasks, task.Text)
	subtasks = truncateSubtasks(subtasks, e.opts.MaxBranching, int(st.budget-st.nodes.Load()))
	if len(subtasks) < 2 {
		if e.opts.Mode == ModeDecompose {
			return Result{Task: task, Status: StatusFailed, Reason: "decompose mode requires at least 2 valid subtasks"}
		}
		return e.solveNode(ctx, task, "fewer than 2 valid subtasks")
	}
	e.emit(Event{Kind: "node_decompose", TaskID: task.ID, Detail: fmt.Sprintf("%d subtasks", len(subtasks))})

	childLineage := append(append([]string(nil), lineage...), normalizeTask(task.Text))
	children := make([]Result, len(subtasks))
	var wg sync.WaitGroup
	sem := make(chan struct{}, e.opts.Concurrency)
	for i, text := range subtasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, text string) {
			defer wg.Done()
			defer func() { <-sem }()
			children[i] = e.runNode(ctx, Task{ID: fmt.Sprintf("%s/%d", task.ID, i+1), Text: text}, childLineage, depth+1, st)
		}(i, text)
	}
	wg.Wait()

	completed := 0
	childResults := make([]ChildResult, 0, len(children))
	for _, c := range children {
		childResults = append(childResults, ChildResult{Task: c.Task, Status: c.Status, Output: c.Output, Reason: c.Reason})
		if c.Status == StatusCompleted {
			completed++
		}
	}
	if completed == 0 {
		return Result{Task: task, Status: StatusFailed, Reason: "all children failed", Children: children}
	}
	out, err := e.opts.Synthesizer.Synthesize(ctx, task, childResults)
	if err != nil {
		return Result{Task: task, Status: StatusFailed, Reason: "synthesizer error: " + err.Error(), Children: children}
	}
	return Result{Task: task, Status: StatusCompleted, Output: out, Children: children}
}
