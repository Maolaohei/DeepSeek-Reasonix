package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"reasonix/internal/novel"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(novelTool{}) }

// novelTool is the mechanism entry point for the novel writing mode: the
// model drives the workspace state machine directly (status → write →
// advance), so continuity bookkeeping does not depend on the user running
// CLI commands. The craft lives in the /novel skill; this tool provides the
// discipline.
type novelTool struct {
	// workDir is the agent-bound project directory (per-tab workspace root in
	// the desktop host). Empty falls back to the process cwd, matching the
	// compile-time built-ins.
	workDir string
}

func (novelTool) Name() string { return "novel" }

func (novelTool) Description() string {
	return "Manage a novel writing workspace (create, continue, or rewrite chapters). Operations: status (show last stable chapter and next step), init (scaffold a new book workspace), check (verify a chapter body against its recorded fingerprint), advance (record a chapter as stable after acceptance). Call status before writing, and advance only after the chapter body has been accepted."
}

func (novelTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "op":{"type":"string","enum":["status","init","check","advance"],"description":"Operation to run"},
  "title":{"type":"string","description":"Novel title / workspace name (required for init and check/advance with a named workspace; optional for status)"},
  "chapter":{"type":"string","description":"Chapter id like ch001 (required for check and advance)"}
},
"required":["op"]
}`)
}

type novelInput struct {
	Op      string `json:"op"`
	Title   string `json:"title"`
	Chapter string `json:"chapter"`
}

func (t novelTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in novelInput
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	if in.Op == "" {
		return "", fmt.Errorf("op is required: status, init, check, advance")
	}
	wd := t.workDir
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	switch in.Op {
	case "status":
		ws, err := novel.Locate(wd, in.Title)
		if err != nil {
			return "", err
		}
		s, err := novel.Load(ws)
		if err != nil {
			return "", err
		}
		return novel.Status(ws, s), nil
	case "init":
		if in.Title == "" {
			return "", fmt.Errorf("title is required for init")
		}
		ws, err := novel.Init(wd, in.Title)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("created %s", ws), nil
	case "check":
		if in.Chapter == "" {
			return "", fmt.Errorf("chapter is required for check")
		}
		ws, err := novel.Locate(wd, in.Title)
		if err != nil {
			return "", err
		}
		s, err := novel.Load(ws)
		if err != nil {
			return "", err
		}
		st, err := s.CheckStability(ws, in.Chapter)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s", in.Chapter, st), nil
	case "advance":
		if in.Chapter == "" {
			return "", fmt.Errorf("chapter is required for advance")
		}
		ws, err := novel.Locate(wd, in.Title)
		if err != nil {
			return "", err
		}
		s, err := novel.Load(ws)
		if err != nil {
			return "", err
		}
		if err := s.MarkStable(ws, in.Chapter, "chapters/"+in.Chapter+"/draft.md"); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s recorded as stable; %s", in.Chapter, s.NextAction), nil
	default:
		return "", fmt.Errorf("unknown op %q", in.Op)
	}
}

// ReadOnly is false: advance/init write workspace state.
func (novelTool) ReadOnly() bool { return false }
