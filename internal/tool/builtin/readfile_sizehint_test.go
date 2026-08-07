package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileLargeFileSizeHint guards the first-page size hint: reading the
// head of a >64 KB file must tell the model the file is large so it can decide
// whether paging is worth it (the token-blowup pattern from large-novel
// sessions), while small files and later pages stay hint-free.
func TestReadFileLargeFileSizeHint(t *testing.T) {
	dir := t.TempDir()

	big := filepath.Join(dir, "big.txt")
	var sb strings.Builder
	for i := range 3000 {
		sb.WriteString("this is line number " + itoa(i) + " with enough padding to exceed the hint threshold\n")
	}
	if err := os.WriteFile(big, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("tiny file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First page of a large file: size hint present.
	args, _ := json.Marshal(map[string]any{"path": big})
	out, err := readFile{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "file is ") || !strings.Contains(out, "showing the first 2000 lines") {
		t.Fatalf("large first page missing size hint:\n%s", out)
	}

	// A later page: no hint (already known large).
	args, _ = json.Marshal(map[string]any{"path": big, "offset": 2500, "limit": 10})
	out, err = readFile{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "file is ") {
		t.Fatalf("paged read must not repeat the size hint:\n%s", out)
	}

	// Small file: never hints.
	args, _ = json.Marshal(map[string]any{"path": small})
	out, err = readFile{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "file is ") {
		t.Fatalf("small file must not get a size hint:\n%s", out)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{2048, "2 KB"},
		{1 << 20, "1.0 MB"},
		{300 * 1024, "300 KB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
