package novel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Fingerprint returns the SHA-256 of the chapter body file.
func Fingerprint(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Stability is the result of checking a chapter body against its recorded
// fingerprint.
type Stability string

const (
	Stable      Stability = "stable"       // body matches the recorded fingerprint
	Stale       Stability = "stale"        // recorded, but the body no longer matches (user edited)
	Unrecorded  Stability = "unrecorded"   // no fingerprint recorded yet
	BodyMissing Stability = "body_missing" // the recorded path does not exist
)

// CheckStability compares the current body of the chapter with the recorded
// fingerprint and reports stability. A mismatch never rewrites anything: the
// protected body wins, and dependent assets are flagged stale by the caller.
func (s *State) CheckStability(ws, chapterID string) (Stability, error) {
	if err := validateChapter(chapterID); err != nil {
		return "", err
	}
	ch, ok := s.Chapters[chapterID]
	if !ok {
		return Unrecorded, nil
	}
	sum, err := Fingerprint(filepath.Join(ws, ch.Path))
	if err != nil {
		if os.IsNotExist(err) {
			return BodyMissing, nil
		}
		return "", err
	}
	if sum != ch.SHA256 {
		return Stale, nil
	}
	return Stable, nil
}

// minStableBodyRunes guards against advancing an effectively empty chapter:
// an accepted chapter should carry a real body, not a placeholder.
const minStableBodyRunes = 50

// MarkStable records the chapter's current fingerprint as stable and advances
// the workspace's next action. It is the mechanism-level "memory entry": call
// it only after the chapter body has been accepted. The recorded rune count is
// returned for the caller's progress reporting.
func (s *State) MarkStable(ws, chapterID, relPath string) (int, error) {
	if err := validateChapter(chapterID); err != nil {
		return 0, err
	}
	abs := filepath.Join(ws, relPath)
	b, err := os.ReadFile(abs)
	if err != nil {
		return 0, err
	}
	n := utf8.RuneCount(b)
	if n < minStableBodyRunes {
		return 0, fmt.Errorf("chapter body too short (%d runes < %d); write the full chapter before advance", n, minStableBodyRunes)
	}
	sum := sha256.Sum256(b)
	if s.Chapters == nil {
		s.Chapters = map[string]*Chapter{}
	}
	s.Chapters[chapterID] = &Chapter{
		Path:   filepath.ToSlash(relPath),
		SHA256: hex.EncodeToString(sum[:]),
		Status: "stable",
	}
	s.LastStableChapter = chapterID
	s.NextAction = fmt.Sprintf("写 %s（%s 的下一章）", nextID(chapterID), chapterID)
	if err := Save(ws, s); err != nil {
		return 0, err
	}
	return n, nil
}

// Rollback forgets a chapter's stability record and points the next action
// back at it, so a wrongly accepted chapter can be redone without hand-editing
// the state file.
func (s *State) Rollback(ws, chapterID string) error {
	if err := validateChapter(chapterID); err != nil {
		return err
	}
	if _, ok := s.Chapters[chapterID]; !ok {
		return fmt.Errorf("chapter %s has no stability record to roll back", chapterID)
	}
	delete(s.Chapters, chapterID)
	if s.LastStableChapter == chapterID {
		s.LastStableChapter = lastStableID(s.Chapters)
	}
	s.NextAction = fmt.Sprintf("重新写并核对 %s（已撤销登记）", chapterID)
	return Save(ws, s)
}

// lastStableID returns the highest-numbered recorded chapter, or "" when none.
func lastStableID(chapters map[string]*Chapter) string {
	best := ""
	bestN := -1
	for id := range chapters {
		n, ok := chapterNum(id)
		if !ok {
			continue
		}
		if n > bestN {
			best, bestN = id, n
		}
	}
	return best
}

// chapterNum parses the numeric part of a chapter id.
func chapterNum(id string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "ch"))
	return n, err == nil && n >= 0
}

// MarkStale flags a chapter and its dependent assets as stale after the body
// changed under the recorded fingerprint. The body is never touched.
func (s *State) MarkStale(ws, chapterID string) error {
	if err := validateChapter(chapterID); err != nil {
		return err
	}
	if ch, ok := s.Chapters[chapterID]; ok {
		ch.Status = "stale"
		s.NextAction = fmt.Sprintf("按 %s 当前正文重新核对，再登记指纹", chapterID)
	}
	return Save(ws, s)
}

// nextID increments a chapter id of the form ch012 (or ch9 → ch10).
func nextID(id string) string {
	trimmed := strings.TrimPrefix(id, "ch")
	n := 0
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return id + "-next"
		}
		n = n*10 + int(r-'0')
	}
	return fmt.Sprintf("ch%03d", n+1)
}
