package store_test

import (
	"context"
	"testing"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/tmdb"
)

// seedMovieWithFile inserts a movie row and, when files is true, attaches one
// movie_file to it. A row seeded with files=false reproduces what the scanner
// leaves behind when a release disappears from disk: the movie_files row is
// pruned but the movies row (kept as a metadata cache) survives file-less.
func seedMovieWithFile(t *testing.T, s *store.Store, m *model.Movie, files bool) int64 {
	t.Helper()
	ctx := context.Background()
	if err := s.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("upsert movie %s: %v", m.Title, err)
	}
	if !files {
		return m.ID
	}
	lib, err := s.CreateLibrary(ctx, "L"+m.Title, "/tmp/"+m.Title, 0)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	mf := &model.MovieFile{
		MovieID: m.ID, LibraryID: lib.ID,
		DirPath: m.Title, FileName: m.Title + ".mkv",
		Resolution: "1080p",
	}
	if err := s.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatalf("upsert movie_file: %v", err)
	}
	return m.ID
}

// TestListMovies_HidesFilelessByDefault verifies that file-less movie rows
// (orphaned index entries) are excluded from the default list and count, while
// the data-health buckets still surface them for debugging.
func TestListMovies_HidesFilelessByDefault(t *testing.T) {
	s := newTestStore(t)

	ok := seedMovieWithFile(t, s, &model.Movie{Title: "On Disk", Year: 2001, TMDBID: 101}, true)
	seedMovieWithFile(t, s, &model.Movie{Title: "Orphan Matched", Year: 2002, TMDBID: 102}, false)
	seedMovieWithFile(t, s, &model.Movie{Title: "Orphan Raw", Year: 2003}, false)
	seedMovieWithFile(t, s, &model.Movie{Title: "On Disk No TMDB", Year: 2004}, true)

	ids := func(f store.MovieFilter) map[int64]bool {
		t.Helper()
		got, err := s.ListMovies(context.Background(), store.ListMoviesOpts{Filter: f})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := make(map[int64]bool, len(got))
		for _, m := range got {
			out[m.ID] = true
		}
		return out
	}

	if got := ids(store.MovieFilter{}); len(got) != 2 || !got[ok] {
		t.Fatalf("default list = %v, want only the two file-backed rows", got)
	}
	if n, err := s.CountMovies(context.Background(), store.MovieFilter{}); err != nil || n != 2 {
		t.Fatalf("default count = %d (%v), want 2", n, err)
	}

	got := ids(store.MovieFilter{MatchIssue: "no_files"})
	if len(got) != 2 || got[ok] {
		t.Fatalf("no_files = %v, want the two orphan rows only", got)
	}
	if got := ids(store.MovieFilter{MatchIssue: "unmatched"}); len(got) != 3 {
		t.Fatalf("unmatched = %v, want both orphans plus the no-tmdb row", got)
	}
	if got := ids(store.MovieFilter{MatchIssue: "no_tmdb"}); len(got) != 1 {
		t.Fatalf("no_tmdb = %v, want the file-backed no-tmdb row", got)
	}
	if got := ids(store.MovieFilter{MatchIssue: "multi_version"}); len(got) != 0 {
		t.Fatalf("multi_version = %v, want none", got)
	}
}

// TestCollections_IgnoreFilelessMembers verifies collection bookkeeping agrees
// with the (default) library list: file-less member rows don't count toward a
// collection's movie_count, a collection whose only member is file-less is
// hidden entirely, and such members resolve to local_movie_id = 0 in the parts
// list so they render as missing films.
func TestCollections_IgnoreFilelessMembers(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	colID, err := s.UpsertCollection(ctx, &tmdb.Collection{ID: 860, Name: "French Connection Collection"})
	if err != nil {
		t.Fatalf("upsert collection: %v", err)
	}
	soloID, err := s.UpsertCollection(ctx, &tmdb.Collection{ID: 861, Name: "Solo Collection"})
	if err != nil {
		t.Fatalf("upsert collection: %v", err)
	}

	owned := &model.Movie{Title: "The French Connection", Year: 1971, TMDBID: 8874, CollectionID: colID}
	seedMovieWithFile(t, s, owned, true)
	seedMovieWithFile(t, s, &model.Movie{Title: "French Connection II", Year: 1975, TMDBID: 10711, CollectionID: colID}, false)
	seedMovieWithFile(t, s, &model.Movie{Title: "Solo Orphan", Year: 1999, TMDBID: 999, CollectionID: soloID}, false)

	if err := s.ReplaceCollectionParts(ctx, colID, []tmdb.CollectionPart{
		{ID: 8874, Title: "The French Connection", ReleaseDate: "1971-10-07"},
		{ID: 10711, Title: "The French Connection II", ReleaseDate: "1975-06-27"},
	}); err != nil {
		t.Fatalf("replace parts: %v", err)
	}

	cols, err := s.ListCollectionsWithCount(ctx, 1)
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("collections = %+v, want only the collection with a file-backed member", cols)
	}
	if cols[0].ID != colID || cols[0].MovieCount != 1 {
		t.Fatalf("collection = %+v, want id %d with movie_count 1 (file-less member not counted)", cols[0], colID)
	}

	parts, err := s.ListCollectionParts(ctx, colID)
	if err != nil {
		t.Fatalf("list parts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(parts))
	}
	if parts[0].LocalMovieID != owned.ID {
		t.Fatalf("part 0 local id = %d, want %d", parts[0].LocalMovieID, owned.ID)
	}
	if parts[1].LocalMovieID != 0 {
		t.Fatalf("file-less member must resolve to local_movie_id 0 (missing), got %d", parts[1].LocalMovieID)
	}
}
