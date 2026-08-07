package builtin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/config"
)

// maxHintTools caps how many available tools the not-found hint lists.
const maxHintTools = 12

// annotateCommandNotFound appends an available-tools hint to "command not
// found"-class errors. The model frequently reaches for an interpreter that
// does not exist on the host (e.g. python3 on a Windows box that only has
// python) — the first attempt fails and costs a retry round. The hint points
// at the interpreters the boot-time environment probe actually found, so the
// retry uses the right command. Errors of other classes pass through
// unchanged.
func annotateCommandNotFound(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	notFound := strings.Contains(text, "command not found") ||
		strings.Contains(text, "not recognized as the name of a cmdlet") ||
		strings.Contains(text, "not recognized as an internal or external command")
	if !notFound {
		return err
	}
	if hint := availableToolsHint(); hint != "" {
		return fmt.Errorf("%w\n\n%s", err, hint)
	}
	return err
}

// probeSnapshotResult mirrors the persisted environment probe entry (see
// internal/environment): fields match the JSON names the probe writes.
type probeSnapshotResult struct {
	Command string `json:"Command"`
	Found   bool   `json:"Found"`
}

type probeSnapshotFile struct {
	Results []probeSnapshotResult `json:"results"`
}

// availableToolsHint reads the newest environment probe snapshot under the
// shared cache root (written once per boot, reused by CLI/desktop) and lists
// the tools actually found on this host. "" when no snapshot is readable.
func availableToolsHint() string {
	matches, err := filepath.Glob(filepath.Join(config.CacheDir(), "environment", "probes-*.json"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool { return modTime(matches[i]) > modTime(matches[j]) })
	b, err := os.ReadFile(matches[0])
	if err != nil {
		return ""
	}
	var snap probeSnapshotFile
	if err := json.Unmarshal(b, &snap); err != nil {
		return ""
	}
	var names []string
	for _, r := range snap.Results {
		if !r.Found || strings.TrimSpace(r.Command) == "" {
			continue
		}
		names = append(names, r.Command)
		if len(names) >= maxHintTools {
			break
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "The command was not found. Tools available on this host (from the environment probe): " +
		strings.Join(names, ", ") + " — retry with one of these."
}

func modTime(p string) int64 {
	if info, err := os.Stat(p); err == nil {
		return info.ModTime().UnixNano()
	}
	return 0
}
