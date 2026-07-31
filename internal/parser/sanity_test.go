package parser

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// TestParseTitle_AllLibraryDirs runs the parser over every top-level directory
// name in media.txt and asserts invariants: no panic, a non-empty title, and a
// plausible year when one is present. This is a smoke test against the real
// library, not an exact-decomposition test.
func TestParseTitle_AllLibraryDirs(t *testing.T) {
	f, err := os.Open("../../media.txt")
	if err != nil {
		t.Skipf("media.txt not available: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	count := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		var name string
		switch {
		case strings.Contains(line, "├── "):
			name = strings.TrimPrefix(line, "├── ")
		case strings.Contains(line, "└── "):
			name = strings.TrimPrefix(line, "└── ")
		default:
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		count++

		m := ParseTitle(name)
		if m.Title == "" {
			t.Errorf("empty title for %q -> %+v", name, m)
		}
		if m.Year != 0 && (m.Year < 1910 || m.Year > 2099) {
			t.Errorf("implausible year %d for %q", m.Year, name)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan error: %v", err)
	}
	t.Logf("parsed %d top-level directory names without panic", count)
}
