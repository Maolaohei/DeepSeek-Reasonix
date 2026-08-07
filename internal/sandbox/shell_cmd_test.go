package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// onPath returns a lookPath stub resolving only the named commands.
func cmdOnPath(names ...string) func(string) (string, error) {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return `C:\fake\` + name + ".exe", nil
		}
		return "", exec.ErrNotFound
	}
}

func TestResolveShellAutoFallsBackToCmd(t *testing.T) {
	// Windows host without bash or PowerShell: cmd.exe is the last-resort
	// interpreter, so the shell tool still works.
	got := resolveShell("", "", nil, "windows",
		cmdOnPath("cmd"), func(string) bool { return false }, nil, nil,
		func(string) bool { return false }, func(string) bool { return false })
	if got.Kind != ShellCmd {
		t.Fatalf("kind = %s, want cmd (path=%s)", got.Kind, got.Path)
	}
	if got.Path != `C:\fake\cmd.exe` {
		t.Fatalf("path = %q, want C:\\fake\\cmd.exe", got.Path)
	}

	// On Linux there is no cmd fallback; the generic bash default wins.
	got = resolveShell("", "", nil, "linux",
		cmdOnPath("cmd"), func(string) bool { return false }, nil, nil,
		func(string) bool { return false }, func(string) bool { return false })
	if got.Kind != ShellBash {
		t.Fatalf("linux kind = %s, want bash", got.Kind)
	}
}

func TestResolveShellCmdPrefer(t *testing.T) {
	exists := func(p string) bool { return p == `C:\custom\cmd.exe` }
	// prefer=cmd with an explicit path that exists.
	got := resolveShell("cmd", `C:\custom\cmd.exe`, nil, "windows",
		cmdOnPath(), exists, nil, nil, func(string) bool { return false }, func(string) bool { return false })
	if got.Kind != ShellCmd || got.Path != `C:\custom\cmd.exe` {
		t.Fatalf("prefer=cmd path = %+v, want cmd at C:\\custom\\cmd.exe", got)
	}
	// prefer=cmd without a path resolves via the decision table.
	got = resolveShell("cmd", "", nil, "windows",
		cmdOnPath("cmd"), func(string) bool { return false }, nil, nil,
		func(string) bool { return false }, func(string) bool { return false })
	if got.Kind != ShellCmd {
		t.Fatalf("prefer=cmd lookup = %s, want cmd", got.Kind)
	}
	// prefer=cmd with SystemRoot present (real env on Windows) still consults
	// the injected exists first; a missing system cmd falls back to auto.
	t.Setenv("SystemRoot", `C:\Windows`)
	got = resolveShell("cmd", "", nil, "windows",
		cmdOnPath(), func(string) bool { return false }, nil, nil,
		func(string) bool { return false }, func(string) bool { return false })
	if got.Kind != ShellBash {
		t.Fatalf("cmd missing everywhere = %s, want auto fallback to bash", got.Kind)
	}
}

func TestShellCmdArgv(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	sh := Shell{Kind: ShellCmd, Path: `C:\Windows\System32\cmd.exe`}
	argv := sh.argv("dir /b")
	want := []string{
		`C:\Windows\System32\cmd.exe`, "/d", "/s", "/c",
		"chcp 65001 >nul & dir /b",
	}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
	// Null-device aliases from other shells map onto cmd's nul.
	got := sh.argv("type a.txt > /dev/null && echo ok 2> $null")
	if !strings.Contains(got[4], ">nul") || strings.Contains(got[4], "$null") || strings.Contains(got[4], "/dev/null") {
		t.Fatalf("nul rewriting failed: %q", got[4])
	}
	if !sh.SupportsChaining() {
		t.Fatal("cmd must support && chaining")
	}
}

func TestShellCmdPathDefaultsToKindName(t *testing.T) {
	sh := Shell{Kind: ShellCmd}
	argv := sh.argv("echo hi")
	if argv[0] != "cmd" {
		t.Fatalf("argv[0] = %q, want cmd (kind name fallback)", argv[0])
	}
}

// TestFindCmdPrefersSystemRoot verifies the System32 candidate is checked
// before PATH — on every real Windows install cmd.exe lives there.
func TestFindCmdPrefersSystemRoot(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("windir", `C:\Windows`)
	got := resolveShell("cmd", "", nil, "windows",
		cmdOnPath(), func(p string) bool { return p == filepath.Join(`C:\Windows`, "System32", "cmd.exe") },
		nil, nil, func(string) bool { return false }, func(string) bool { return false })
	if got.Kind != ShellCmd || got.Path != filepath.Join(`C:\Windows`, "System32", "cmd.exe") {
		t.Fatalf("system32 cmd = %+v, want %s", got, filepath.Join(`C:\Windows`, "System32", "cmd.exe"))
	}
	// windir env must not leak into unrelated tests.
	_ = os.Getenv("windir")
}
