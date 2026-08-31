package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
)

// TestListStaleMovieFiles verifies the pre-prune listing matches what
// RemoveStaleMovieFiles deletes: same keep-key semantics (including the
// empty-keep "everything is stale" case) and the owning movie's title joined
// in for readable logs.
func TestListStaleMovieFiles(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/media/films", 0)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	movie := &model.Movie{Title: "Casino", Year: 1995}
	if err := s.UpsertMovieSeed(ctx, movie); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	mf := &model.MovieFile{
		LibraryID: lib.ID, MovieID: movie.ID,
		DirPath: "Casino.1995.1080p.BluRay.x265-beAst", FileName: "Casino.1995.1080p.BluRay.x265-beAst.mkv",
	}
	if err := s.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatalf("upsert movie_file: %v", err)
	}

	key := "Casino.1995.1080p.BluRay.x265-beAst\x00Casino.1995.1080p.BluRay.x265-beAst.mkv"

	// keeping a different key lists the row as stale, with the movie title
	stale, err := s.ListStaleMovieFiles(ctx, lib.ID, []string{"Some.Other.Release\x00Other.mkv"})
	if err != nil {
		t.Fatalf("list stale: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale rows = %d, want 1", len(stale))
	}
	if stale[0].DirPath != mf.DirPath || stale[0].FileName != mf.FileName {
		t.Fatalf("stale row = %+v, want dir=%q file=%q", stale[0], mf.DirPath, mf.FileName)
	}
	if stale[0].Title != "Casino" || stale[0].Year != 1995 || stale[0].MovieID != movie.ID {
		t.Fatalf("stale row movie fields = %+v, want Casino (1995) id=%d", stale[0], movie.ID)
	}

	// keeping the actual key lists nothing
	stale, err = s.ListStaleMovieFiles(ctx, lib.ID, []string{key})
	if err != nil {
		t.Fatalf("list stale (keep): %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("stale rows = %d, want 0", len(stale))
	}

	// empty keep = every file of the library is stale (mirrors the delete)
	stale, err = s.ListStaleMovieFiles(ctx, lib.ID, nil)
	if err != nil {
		t.Fatalf("list stale (empty keep): %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("stale rows = %d, want 1", len(stale))
	}
}

// TestSnapshot_IsolatedCopy verifies Snapshot produces a working copy of the
// database that can be written to without affecting the source - the guarantee
// `rescan --dry-run` relies on when it scans a throwaway copy.
func TestSnapshot_IsolatedCopy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	lib, err := s.CreateLibrary(ctx, "Films", "/media/films", 0)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	if err := s.UpsertMovieSeed(ctx, &model.Movie{Title: "Casino", Year: 1995}); err != nil {
		t.Fatalf("seed movie: %v", err)
	}

	snapPath := filepath.Join(t.TempDir(), "snap.db")
	if err := s.Snapshot(ctx, snapPath); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	snap, err := store.Open(ctx, snapPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()

	// the copy carries the source data
	snapLib, err := snap.GetLibrary(ctx, lib.ID)
	if err != nil || snapLib.Name != "Films" {
		t.Fatalf("snapshot library = %+v, err = %v", snapLib, err)
	}
	var movies int
	if err := snap.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies`).Scan(&movies); err != nil {
		t.Fatalf("count snapshot movies: %v", err)
	}
	if movies != 1 {
		t.Fatalf("snapshot movies = %d, want 1", movies)
	}

	// writes against the snapshot must not leak into the source
	if err := snap.TouchLibraryScan(ctx, lib.ID, 4242); err != nil {
		t.Fatalf("touch snapshot library: %v", err)
	}
	if err := snap.SetTMDBCache(ctx, "/movie/1", "{}"); err != nil {
		t.Fatalf("set snapshot tmdb cache: %v", err)
	}
	snapMovies, err := snap.ListMovies(ctx, store.ListMoviesOpts{Filter: store.MovieFilter{MatchIssue: "no_files"}})
	if err != nil {
		t.Fatalf("list snapshot movies: %v", err)
	}
	if len(snapMovies) != 1 {
		t.Fatalf("snapshot movies = %d, want 1", len(snapMovies))
	}

	srcLib, err := s.GetLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatalf("get source library: %v", err)
	}
	if srcLib.LastScanAt != 0 {
		t.Fatalf("source last_scan_at = %d, want 0: snapshot write leaked", srcLib.LastScanAt)
	}
	var srcMovies, srcCache int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM movies`).Scan(&srcMovies); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tmdb_cache`).Scan(&srcCache); err != nil {
		t.Fatal(err)
	}
	if srcMovies != 1 || srcCache != 0 {
		t.Fatalf("source movies = %d, tmdb_cache = %d: snapshot writes leaked", srcMovies, srcCache)
	}
}
