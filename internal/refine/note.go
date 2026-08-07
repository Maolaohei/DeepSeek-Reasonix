package refine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"reasonix/internal/frontmatter"
)

// Scope mirrors the memory store's project/global split: project (local) is the
// default and is scoped to the current workspace's harness directory; global is
// the user-level harness shared across workspaces.
type Scope string

const (
	ScopeProject Scope = "project"
	ScopeGlobal  Scope = "global"
)

// PromptNote is one editable supplemental prompt entry. Notes load into the
// system prefix (after REASONIX.md/AGENTS.md, before the skill index) and are
// the model-editable behavioral-policy layer of the Continual Harness.
type PromptNote struct {
	ID          string    // stable slug id, also the file stem
	Title       string    // short human label
	Scope       Scope     // where the note lives
	Version     int       // monotonic content revision, starting at 1
	Content     string    // the policy body (Markdown)
	CreatedAt   time.Time // zero when unknown
	UpdatedAt   time.Time // zero when unknown
	Path        string    // absolute file path (filled by Load)
	Description string    // optional one-line hook for the /refine overview
}

type noteFrontmatter struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title,omitempty"`
	Scope       string `yaml:"scope,omitempty"`
	Version     int    `yaml:"version,omitempty"`
	Description string `yaml:"description,omitempty"`
	CreatedAt   string `yaml:"created_at,omitempty"`
	UpdatedAt   string `yaml:"updated_at,omitempty"`
}

func parseNoteTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

func formatNoteTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// renderNote serializes a note to frontmatter + body. Marshaled by yaml.v3 so
// titles or content containing ": " or quotes stay parseable — the same
// rationale as the memory store's render.
func renderNote(n PromptNote) string {
	fm := noteFrontmatter{
		ID:          n.ID,
		Title:       oneLine(n.Title),
		Scope:       string(n.Scope),
		Version:     n.Version,
		Description: oneLine(n.Description),
		CreatedAt:   formatNoteTime(n.CreatedAt),
		UpdatedAt:   formatNoteTime(n.UpdatedAt),
	}
	var b strings.Builder
	b.WriteString("---\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	// Encoding a flat struct of strings cannot fail.
	_ = enc.Encode(fm)
	b.WriteString("---\n")
	b.WriteString(strings.TrimRight(n.Content, "\n"))
	b.WriteByte('\n')
	return b.String()
}

// parseNote decodes a note file. A missing or unparseable frontmatter is not
// fatal: the whole file body becomes the content and the id falls back to the
// file stem. Returns ok=false for a path that is not a readable file.
func parseNote(path string) (PromptNote, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return PromptNote{}, false
	}
	note, ok := parseNoteFromBytes(b)
	if ok {
		// A file without a frontmatter id keeps its own stem as the id, so
		// hand-written files never collide under the generic fallback.
		if note.ID == "note" {
			note.ID = strings.TrimSuffix(filepath.Base(path), ".md")
		}
		note.Path = path
	}
	return note, ok
}

// parseNoteFromBytes decodes a serialized note (file content or a rollback
// snapshot). The id falls back to the "note" stem when no id is present.
func parseNoteFromBytes(b []byte) (PromptNote, bool) {
	raw := string(b)
	stem := "note"
	fm, body := frontmatter.Split(raw)
	scope := Scope(fm["scope"])
	if scope != ScopeProject && scope != ScopeGlobal {
		scope = ScopeProject
	}
	id := strings.TrimSpace(fm["id"])
	if id == "" {
		id = stem
	}
	created := parseNoteTime(fm["created_at"])
	updated := parseNoteTime(fm["updated_at"])
	if updated.IsZero() {
		updated = created
	}
	return PromptNote{
		ID:          id,
		Title:       firstNonEmpty(fm["title"], id),
		Scope:       scope,
		Version:     parsePositiveInt(fm["version"]),
		Content:     strings.TrimRight(body, "\n"),
		CreatedAt:   created,
		UpdatedAt:   updated,
		Description: fm["description"],
	}, true
}

// validNoteID mirrors the memory slug contract: lowercase, dashes allowed,
// bounded length, no path separators.
func validNoteID(id string) bool {
	if id == "" || len(id) > 80 || strings.ContainsAny(id, "/\\") {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// notePathFor returns the file path a note with id lives at under dir. Returns
// "" for an invalid id.
func notePathFor(dir, id string) string {
	if !validNoteID(id) {
		return ""
	}
	return filepath.Join(dir, id+".md")
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parsePositiveInt(value string) int {
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 1<<30 {
			return 0
		}
	}
	return n
}

func (s Scope) String() string { return string(s) }

// errInvalidNoteID is returned by mutations that receive an unusable id.
func errInvalidNoteID(id string) error {
	return fmt.Errorf("invalid prompt note id %q", id)
}
