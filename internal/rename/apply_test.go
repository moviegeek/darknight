package rename

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyDirAndFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Grean.Snake.1993.BluRay.1080p.x264-HDChina")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"Grean.Snake.1993.BluRay.1080p.x264-HDChina.mkv",
		"Grean.Snake.1993.BluRay.1080p.x264-HDChina.chi.srt",
		"Grean.Snake.1993.BluRay.1080p.x264-HDChina.nfo",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := Build(dir, files[0], files, "Green Snake", 1993)
	if plan.DirNew == plan.DirOld || len(plan.Moves) == 0 {
		t.Fatalf("expected a plan, got %+v", plan)
	}
	if err := Apply(plan, dir); err != nil {
		t.Fatalf("apply: %v", err)
	}
	newDir := filepath.Join(root, plan.DirNew)
	for _, want := range []string{
		"Green.Snake.1993.BluRay.1080p.x264-HDChina.mkv",
		"Green.Snake.1993.BluRay.1080p.x264-HDChina.chi.srt",
		"Green.Snake.1993.BluRay.1080p.x264-HDChina.nfo",
	} {
		if _, err := os.Stat(filepath.Join(newDir, want)); err != nil {
			t.Errorf("missing after apply: %s (%v)", want, err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("old dir still exists: %v", err)
	}
}

func TestApplyCanonicalNoop(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Heat.1995.BluRay.1080p.x264-G")
	os.MkdirAll(dir, 0o755)
	f := "Heat.1995.BluRay.1080p.x264-G.mkv"
	os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)

	plan := Build(dir, f, []string{f}, "Heat", 1995)
	if len(plan.Moves) != 0 || plan.DirNew != plan.DirOld {
		t.Fatalf("expected empty plan, got %+v", plan)
	}
	if err := Apply(plan, dir); err != nil {
		t.Fatalf("apply noop: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
		t.Errorf("file should be untouched: %v", err)
	}
}
