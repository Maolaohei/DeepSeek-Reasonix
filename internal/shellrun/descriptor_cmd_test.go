package shellrun

import (
	"testing"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

func TestDescriptorFromShellCmd(t *testing.T) {
	ex := DescriptorFromShell(sandbox.Shell{Kind: sandbox.ShellCmd, Path: `C:\Windows\System32\cmd.exe`})
	if ex.Shell != tool.ShellNameCmd {
		t.Fatalf("Shell = %q, want %q", ex.Shell, tool.ShellNameCmd)
	}
	if got := DisplayName(ex); got != "cmd.exe" {
		t.Fatalf("DisplayName = %q, want cmd.exe", got)
	}
	// cmd supports && and || natively.
	if !(sandbox.Shell{Kind: sandbox.ShellCmd}.SupportsChaining()) {
		t.Fatal("cmd must support chaining")
	}
}
