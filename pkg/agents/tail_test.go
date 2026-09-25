package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	content := "first line\nsecond line\nthird line\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := TailFile(path, 1024)
	if err != nil || got != content {
		t.Fatalf("small file: got %q, %v; want the whole file", got, err)
	}

	// 20 bytes from the end start inside "second line"; that partial line is dropped.
	got, err = TailFile(path, 20)
	if err != nil || got != "third line\n" {
		t.Fatalf("tail 20: got %q, %v; want %q", got, err, "third line\n")
	}

	if _, err := TailFile(filepath.Join(dir, "missing"), 10); err == nil {
		t.Fatal("missing file: want an error")
	}
}

// The file can grow between Stat and the read (the agent is still writing its
// transcript). The read stays bounded to maxBytes from the size seen at Stat.
func TestTailRead_BoundedWhenFileGrows(t *testing.T) {
	seen := strings.Repeat("a", 90) + "\n" + strings.Repeat("b", 9) + "\n" // 101 bytes at Stat time
	grown := seen + strings.Repeat("c", 1<<20) + "\n"                      // 1 MB appended since

	got, err := tailRead(strings.NewReader(grown), int64(len(seen)), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > 50 {
		t.Fatalf("read %d bytes, want at most 50", len(got))
	}
	if got != strings.Repeat("b", 9)+"\n" {
		t.Errorf("got %q, want the last whole line seen at Stat", got)
	}

	got, err = tailRead(strings.NewReader(grown), int64(len(seen)), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if got != seen {
		t.Errorf("file smaller than maxBytes at Stat: got %d bytes, want the %d seen", len(got), len(seen))
	}
}
