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

// TestScanLibrary_MultiVersionRelease verifies the file-grained model: two
// files of the same film in one directory (720p + 1080p) become TWO
// movie_files rows attached to the SAME movie.
func TestScanLibrary_MultiVersionRelease(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	relDir := filepath.Join(root, "The.Bourne.Identity.2002.BluRay")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"The.Bourne.Identity.2002.720p.BluRay.DTS.x264-ESiR.mkv",
		"The.Bourne.Identity.2002.1080p.BluRay.DTS.x264-ESiR.mkv",
	} {
		if err := os.WriteFile(filepath.Join(relDir, f), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}
	stats, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stats.Added != 2 {
		t.Fatalf("expected 2 added files, got %+v", stats)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 {
		t.Fatalf("expected 1 movie, got %d", len(movies))
	}
	files, err := s.ListMovieFiles(ctx, movies[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 movie_files on one movie, got %d", len(files))
	}
	// ordered by resolution DESC: 1080p first
	if files[0].Resolution != "1080p" || files[1].Resolution != "720p" {
		t.Fatalf("expected 1080p then 720p, got %s / %s", files[0].Resolution, files[1].Resolution)
	}
}

// TestScanLibrary_FlatCollectionPack verifies a flat anthology dir: the dir
// name is a year-range collection, every mkv inside is its own movie.
func TestScanLibrary_FlatCollectionPack(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	relDir := filepath.Join(root, "Alien.Anthology.1979-1997.BluRay.1080p.x264-HDChina")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"Alien.1979.BluRay.1080p.x264-HDChina.mkv",
		"Aliens.1986.BluRay.1080p.x264-HDChina.mkv",
		"Alien.3.1992.BluRay.1080p.x264-HDChina.mkv",
		"Alien.Resurrection.1997.BluRay.1080p.x264-HDChina.mkv",
	} {
		if err := os.WriteFile(filepath.Join(relDir, f), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan: %v", err)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// four distinct (title, year) pairs -> four logical movies
	if len(movies) != 4 {
		t.Fatalf("expected 4 movies from the flat anthology, got %d", len(movies))
	}
	totalFiles := 0
	for _, m := range movies {
		files, err := s.ListMovieFiles(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		totalFiles += len(files)
	}
	if totalFiles != 4 {
		t.Fatalf("expected 4 movie_files total, got %d", totalFiles)
	}
}

// TestScanLibrary_SkipsNoise verifies sample files and extras directories do
// not become releases.
func TestScanLibrary_SkipsNoise(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	relDir := filepath.Join(root, "Escape.From.Mogadishu.2021.BluRay")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"Escape.From.Mogadishu.2021.BluRay.x264.mkv",
		"Escape.From.Mogadishu.2021.BluRay.x264.Sample.mkv",
	} {
		if err := os.WriteFile(filepath.Join(relDir, f), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// an extras dir with bonus videos - must be ignored entirely
	extras := filepath.Join(root, "Up.2009.BluRay", "extras")
	if err := os.MkdirAll(extras, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extras, "Dug's.Special.Mission.2009.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}
	stats, err := sc.ScanLibrary(ctx, lib)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stats.Added != 1 {
		t.Fatalf("expected 1 added file (sample skipped, extras dir ignored), got %+v", stats)
	}
}

// TestScanLibrary_NestedDiscPack keeps the nested grouping-dir behaviour:
// one level of dirs between root and release is still descended into.
func TestScanLibrary_NestedGroupDir(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	group := filepath.Join(root, "Director.Collections")
	rel := filepath.Join(group, "Tokyo.Story.1953.720p.BluRay.x264-WiKi")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "Tokyo.Story.1953.720p.BluRay.x264-WiKi.mkv"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary(ctx, "Films", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: s, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), FFProbe: fakeProbe}
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan: %v", err)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || movies[0].Title != "Tokyo Story" {
		t.Fatalf("expected Tokyo Story movie, got %+v", movies)
	}
}
