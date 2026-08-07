package agent

import (
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func TestLooksLikeToolFailure(t *testing.T) {
	cases := map[string]bool{
		"error: command not found: foo":    true,
		"blocked: write outside workspace": true,
		"  ERROR: build failed":            true, // trims + lowercases onto the error: prefix
		"go test failed to build package":  true,
		"exit status 1":                    true,
		"fatal: not a git repository":      true,
		"all tests passed":                 false,
		"":                                 false,
	}
	for content, want := range cases {
		if got := looksLikeToolFailure(content); got != want {
			t.Errorf("looksLikeToolFailure(%q) = %v, want %v", content, got, want)
		}
	}
}

func TestExtractErrorSamples(t *testing.T) {
	fold := []provider.Message{
		{Role: provider.RoleTool, Name: "bash", Content: "error: command not found: make"},
		{Role: provider.RoleTool, Name: "bash", Content: "all green"},
		{Role: provider.RoleTool, Name: "go", Content: "exit status 1\n./main.go:12: undefined: Foo"},
		{Role: provider.RoleTool, Name: "git", Content: "fatal: not a git repository"},
		{Role: provider.RoleTool, Name: "bash", Content: "failed to connect to registry"},
	}
	got := extractErrorSamples(fold)
	if !strings.HasPrefix(got, errorSamplesOpen) || !strings.HasSuffix(got, errorSamplesClose) {
		t.Fatalf("samples not wrapped: %q", got)
	}
	for _, want := range []string{"[bash] error: command not found: make", "[go] exit status 1", "[git] fatal: not a git repository"} {
		if !strings.Contains(got, want) {
			t.Errorf("sample %q missing from:\n%s", want, got)
		}
	}
	// Capped at maxErrorSamples: the "failed to" sample must not appear.
	if strings.Contains(got, "failed to connect") {
		t.Errorf("sample cap exceeded: %s", got)
	}
}

func TestExtractErrorSamplesEmptyWithoutFailures(t *testing.T) {
	fold := []provider.Message{
		{Role: provider.RoleTool, Name: "bash", Content: "ok"},
		{Role: provider.RoleTool, Name: "go", Content: "PASS"},
	}
	if got := extractErrorSamples(fold); got != "" {
		t.Fatalf("expected no samples, got %q", got)
	}
}

func TestClipErrorSampleBoundsLength(t *testing.T) {
	long := strings.Repeat("x", maxErrorSampleChars+50)
	got := clipErrorSample(long)
	if len(got) != maxErrorSampleChars+len("…") {
		t.Fatalf("clipped length = %d, want %d", len(got), maxErrorSampleChars+len("…"))
	}
	short := "tiny"
	if got := clipErrorSample(short); got != short {
		t.Fatalf("short sample changed: %q", got)
	}
}
