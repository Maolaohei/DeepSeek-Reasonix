package novel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Export merges every recorded chapter body (stable or stale) into a single
// manuscript file, in chapter order, with a page-break separator. The result
// is written to <ws>/export/<base>.txt next to the workspace so the model can
// hand it to the user for review or submission.
func Export(ws string) (string, error) {
	s, err := Load(ws)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(s.Chapters))
	for id := range s.Chapters {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ni, oki := chapterNum(ids[i])
		nj, okj := chapterNum(ids[j])
		if oki && okj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})

	var b strings.Builder
	for _, id := range ids {
		ch := s.Chapters[id]
		body, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(ch.Path)))
		if err != nil {
			continue // body_missing chapters are skipped, not fatal
		}
		if b.Len() > 0 {
			b.WriteString("\n\n--- 第 " + id + " 话 ---\n\n")
		}
		b.Write(body)
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("no chapter bodies found to export (advance at least one chapter first)")
	}

	title := filepath.Base(ws)
	out := filepath.Join(ws, "export", title+".txt")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return out, nil
}
