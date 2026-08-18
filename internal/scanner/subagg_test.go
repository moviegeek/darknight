package scanner_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/moviegeek/darknight/internal/scanner"
	"github.com/moviegeek/darknight/internal/store"
)

// An unchanged rescan must NOT wipe the stored subtitle aggregate. The
// subtitles table is only rebuilt when the file changed, so the carry-over
// list in scanOneFile has to include SubtitleLanguages /
// HasExternalSubtitle - forgetting them empties subtitle_languages on every
// incremental scan and breaks the "no Chinese subtitle" filter (observed:
// 425 rows with an empty aggregate while their detail rows still existed).
func TestScan_UnchangedRescanKeepsSubtitleAggregate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	rel := filepath.Join(root, "Heat.1995.BluRay.1080p.x264-GROUP")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	mkv := filepath.Join(rel, "Heat.1995.BluRay.1080p.x264-GROUP.mkv")
	sub := filepath.Join(rel, "Heat.1995.BluRay.1080p.x264-GROUP.chi.srt")
	if err := os.WriteFile(mkv, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sub, []byte("1\n00:00:01 --> 00:00:02\n你好\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary(ctx, "F", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan1: %v", err)
	}

	// patch the aggregate as ffprobe would have written it (fakeProbe's
	// streams carry a chi subtitle; the aggregate comes from the scan path)
	movies, _ := s.ListMovies(ctx, store.ListMoviesOpts{})
	if len(movies) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(movies))
	}
	files, _ := s.ListMovieFiles(ctx, movies[0].ID)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].SubtitleLanguages != "chi" {
		t.Fatalf("scan1 aggregate = %q, want chi (fakeProbe has a chi sub)", files[0].SubtitleLanguages)
	}

	// second scan: file bytes untouched -> unchanged path
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan2: %v", err)
	}
	files2, _ := s.ListMovieFiles(ctx, movies[0].ID)
	if files2[0].SubtitleLanguages != "chi" {
		t.Errorf("unchanged rescan wiped subtitle aggregate: %q", files2[0].SubtitleLanguages)
	}
}
