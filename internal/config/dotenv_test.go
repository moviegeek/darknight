package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEnv writes the given content to a .env file in a temp dir and returns
// the path.
func writeEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	return path
}

func TestLoadDotEnv_parsesAndSets(t *testing.T) {
	// use a key nothing else touches so t.Setenv's locking can't interfere
	unset := []string{"DN_TEST_QUOTED", "DN_TEST_SINGLE", "DN_TEST_ADDR", "DN_TEST_LEVEL"}
	for _, k := range unset {
		os.Unsetenv(k)
	}

	path := writeEnv(t, `# a comment
DN_TEST_ADDR=:9999
export DN_TEST_LEVEL=debug
DN_TEST_QUOTED="hello world"
DN_TEST_SINGLE='no expand $X'
`)

	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	for key, want := range map[string]string{
		"DN_TEST_ADDR":   ":9999",
		"DN_TEST_LEVEL":  "debug",
		"DN_TEST_QUOTED": "hello world",
		"DN_TEST_SINGLE": "no expand $X",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("env %s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadDotEnv_doesNotClobberExisting(t *testing.T) {
	// t.Setenv marks the var as managed; a plain os.Unsetenv later in the
	// same test won't fully reset it, so use a dedicated key here too.
	t.Setenv("DN_TEST_CLOBBER", "fromshell")

	path := writeEnv(t, "DN_TEST_CLOBBER=fromfile\n")
	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	if got := os.Getenv("DN_TEST_CLOBBER"); got != "fromshell" {
		t.Errorf("DN_TEST_CLOBBER = %q, want %q (env should win over .env)", got, "fromshell")
	}
}

func TestLoadDotEnv_missingFileIsOK(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("expected nil for missing file, got %v", err)
	}
}
