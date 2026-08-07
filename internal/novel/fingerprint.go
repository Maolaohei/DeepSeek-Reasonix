package novel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// MarkStable records the chapter's current fingerprint as stable and advances
// the workspace's next action. It is the mechanism-level "memory entry": call
// it only after the chapter body has been accepted.
func (s *State) MarkStable(ws, chapterID, relPath string) error {
	if err := validateChapter(chapterID); err != nil {
		return err
	}
	abs := filepath.Join(ws, relPath)
	sum, err := Fingerprint(abs)
	if err != nil {
		return err
	}
	if s.Chapters == nil {
		s.Chapters = map[string]*Chapter{}
	}
	s.Chapters[chapterID] = &Chapter{
		Path:   filepath.ToSlash(relPath),
		SHA256: sum,
		Status: "stable",
	}
	s.LastStableChapter = chapterID
	s.NextAction = fmt.Sprintf("写 %s（%s 的下一章）", nextID(chapterID), chapterID)
	return Save(ws, s)
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
