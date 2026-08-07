package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNovelToolLifecycle drives the tool through the same lifecycle the model
// would: init a workspace, status, write a chapter body, advance, then detect
// an external edit via check. The tool is bound to a workDir (the desktop
// per-tab project root), not the process cwd.
func TestNovelToolLifecycle(t *testing.T) {
	dir := t.TempDir()

	tool := novelTool{workDir: dir}
	ctx := context.Background()
	run := func(args string) string {
		t.Helper()
		out, err := tool.Execute(ctx, json.RawMessage(args))
		if err != nil {
			t.Fatalf("Execute(%s): %v", args, err)
		}
		return out
	}

	out := run(`{"op":"init","title":"测试书"}`)
	if !strings.Contains(out, "created") {
		t.Fatalf("init: %q", out)
	}
	if !strings.Contains(out, dir) {
		t.Fatalf("workspace must live under the bound workDir, got %q", out)
	}
	out = run(`{"op":"status","title":"测试书"}`)
	if !strings.Contains(out, "下一步") || !strings.Contains(out, "ch001") {
		t.Fatalf("status should point at first step, got %q", out)
	}

	body := filepath.Join(dir, "novels", "测试书", "chapters", "ch001", "draft.md")
	if err := os.MkdirAll(filepath.Dir(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(body, []byte("第一章正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = run(`{"op":"advance","title":"测试书","chapter":"ch001"}`)
	if !strings.Contains(out, "stable") {
		t.Fatalf("advance: %q", out)
	}
	if err := os.WriteFile(body, []byte("用户手动改过的正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = run(`{"op":"check","title":"测试书","chapter":"ch001"}`)
	if !strings.Contains(out, "stale") {
		t.Fatalf("check should report stale after external edit, got %q", out)
	}
}

func TestNovelToolRejectsBadArgs(t *testing.T) {
	tool := novelTool{}
	ctx := context.Background()
	if _, err := tool.Execute(ctx, json.RawMessage(`{"op":""}`)); err == nil {
		t.Fatal("empty op should fail")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"op":"init"}`)); err == nil {
		t.Fatal("init without title should fail")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"op":"check","title":"x"}`)); err == nil {
		t.Fatal("check without chapter should fail")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"op":"bogus"}`)); err == nil {
		t.Fatal("unknown op should fail")
	}
	if tool.ReadOnly() {
		t.Fatal("novel tool writes state; ReadOnly must be false")
	}
}
