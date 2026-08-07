package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"reasonix/internal/novel"
)

// novelCommand implements `reasonix novel <init|status|check|advance> [...]`.
// It is the mechanism layer of the novel writing mode: workspace state,
// chapter fingerprints and next-step routing live here, so continuity does not
// depend on the model remembering to keep books consistent. The craft lives in
// the novel skill; this command provides the discipline.
func novelCommand(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: reasonix novel <init|status|check|advance> [title] [chapter]")
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "usage: reasonix novel init <title>")
			return 2
		}
		ws, err := novel.Init(cwd(), rest[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "novel init: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "created %s\n", ws)
		return 0
	case "status":
		ws, s, err := loadNovel(rest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "novel status: %v\n", err)
			return 1
		}
		fmt.Fprintln(os.Stdout, novel.Status(ws, s))
		return 0
	case "check":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: reasonix novel check <title> <chapter>")
			return 2
		}
		ws, s, err := loadNovel(rest[:1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "novel check: %v\n", err)
			return 1
		}
		st, err := s.CheckStability(ws, rest[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "novel check: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "%s: %s\n", rest[1], st)
		return 0
	case "advance":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: reasonix novel advance <title> <chapter>")
			return 2
		}
		ws, s, err := loadNovel(rest[:1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "novel advance: %v\n", err)
			return 1
		}
		rel := filepath.Join("chapters", rest[1], "draft.md")
		if err := s.MarkStable(ws, rest[1], rel); err != nil {
			fmt.Fprintf(os.Stderr, "novel advance: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "%s recorded as stable; %s\n", rest[1], s.NextAction)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown novel subcommand %q\n", cmd)
		return 2
	}
}

func loadNovel(rest []string) (string, *novel.State, error) {
	dir := cwd()
	title := ""
	if len(rest) > 0 {
		title = rest[0]
	}
	ws, err := novel.Locate(dir, title)
	if err != nil {
		return "", nil, err
	}
	s, err := novel.Load(ws)
	if err != nil {
		return "", nil, err
	}
	return ws, s, nil
}

// cwd returns the process working directory, kept as a helper so tests can
// override it.
func cwd() string {
	wd, _ := os.Getwd()
	return wd
}
