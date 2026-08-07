// Package novel provides the mechanism layer for the novel writing mode:
// workspace state, chapter fingerprints, and next-step routing. The skill
// layer (skills/novel) supplies the craft; this package supplies the
// discipline — continuity bookkeeping that does not depend on the model
// remembering to do it.
package novel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"reasonix/internal/fileutil"
)

// State is the machine-readable progress index of one novel workspace. It is
// the single authority for "where the book stands"; every other continuity
// asset is derived from it and the stable chapter bodies it fingerprints.
type State struct {
	Title             string              `yaml:"title"`
	CreatedAt         time.Time           `yaml:"created_at"`
	LastStableChapter string              `yaml:"last_stable_chapter"` // e.g. ch012
	NextAction        string              `yaml:"next_action"`
	Chapters          map[string]*Chapter `yaml:"chapters"`
}

// Chapter records one chapter's stability fingerprint.
type Chapter struct {
	Path      string    `yaml:"path"`
	SHA256    string    `yaml:"sha256"`
	Status    string    `yaml:"status"` // stable | in_progress | stale
	UpdatedAt time.Time `yaml:"updated_at"`
}

const stateFile = "state.yaml"

// Locate finds the novel workspace: the given name (novels/<name> in cwd),
// or the cwd itself when it looks like a novel workspace, or <cwd>/novels.
func Locate(dir, name string) (string, error) {
	if name != "" {
		ws := filepath.Join(dir, "novels", name)
		if _, err := os.Stat(filepath.Join(ws, stateFile)); err == nil {
			return ws, nil
		}
		return ws, nil // not yet initialized; Init will create it
	}
	for _, cand := range []string{dir, filepath.Join(dir, "novels")} {
		if _, err := os.Stat(filepath.Join(cand, stateFile)); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("no novel workspace found (missing %s); run 'reasonix novel init <title>'", stateFile)
}

// Load reads and parses the workspace state file.
func Load(ws string) (*State, error) {
	b, err := os.ReadFile(filepath.Join(ws, stateFile))
	if err != nil {
		return nil, err
	}
	var s State
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", stateFile, err)
	}
	if s.Chapters == nil {
		s.Chapters = map[string]*Chapter{}
	}
	return &s, nil
}

// Save writes the state file atomically.
func Save(ws string, s *State) error {
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(filepath.Join(ws, stateFile), b, 0o644)
}

// validateTitle rejects titles that could escape the novels/ directory.
func validateTitle(title string) error {
	if title == "" {
		return fmt.Errorf("title is required")
	}
	if strings.ContainsAny(title, `/\`) || title == "." || title == ".." {
		return fmt.Errorf("title %q must not contain path separators", title)
	}
	return nil
}

// validateChapter accepts only chapter ids of the form ch<digits>.
func validateChapter(id string) error {
	if len(id) < 3 || !strings.HasPrefix(id, "ch") {
		return fmt.Errorf("chapter id must look like ch001, got %q", id)
	}
	for _, r := range id[2:] {
		if r < '0' || r > '9' {
			return fmt.Errorf("chapter id must look like ch001, got %q", id)
		}
	}
	return nil
}
