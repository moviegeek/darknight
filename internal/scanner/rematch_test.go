package scanner_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/moviegeek/darknight/internal/matcher"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/scanner"
	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/tmdb"
)

// fakeSearcher serves canned TMDB search results.
type fakeSearcher map[string][]tmdb.SearchResult

func (f fakeSearcher) SearchMovieAll(_ context.Context, query string, limit int) ([]tmdb.SearchResult, error) {
	rs, ok := f[query]
	if !ok || len(rs) == 0 {
		return nil, tmdb.ErrNotFound
	}
	return rs, nil
}

// A: an unmatched movie row must be (re)matched on the next scan even when the
// file's bytes are unchanged. Gating the matcher on `unchanged` left rows stuck
// at tmdb_id=NULL / match_attempts=0 forever.
func TestScan_MatchesUnchangedFileWithUnmatchedMovie(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	root := t.TempDir()
	rel := filepath.Join(root, "Heat.1995.BluRay.1080p.x264-GROUP")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "Heat.1995.BluRay.1080p.x264-GROUP.mkv"),
		[]byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary(ctx, "F", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// first scan: no matcher wired, so the row stays unmatched
	sc := &scanner.Scanner{Store: s, Logger: log, FFProbe: fakeProbe}
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan1: %v", err)
	}
	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil || len(movies) != 1 {
		t.Fatalf("expected 1 movie, got %d (%v)", len(movies), err)
	}
	if movies[0].TMDBID != 0 || movies[0].MatchAttempts != 0 {
		t.Fatalf("expected an unmatched row, got %+v", movies[0])
	}

	// second scan: file byte-identical (unchanged), matcher now available.
	sc.Matcher = matcher.New(fakeSearcher{
		"Heat": {{ID: 949, Title: "Heat", ReleaseDate: "1995-12-15"}},
	}, log)
	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan2: %v", err)
	}
	movies, err = s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 {
		t.Fatalf("expected 1 movie after rescan, got %d", len(movies))
	}
	if movies[0].TMDBID != 949 {
		t.Errorf("unchanged file was not re-matched: tmdb_id=%d attempts=%d status=%q",
			movies[0].TMDBID, movies[0].MatchAttempts, movies[0].MatchStatus)
	}
	if movies[0].MatchStatus != model.MatchStatusMatched {
		t.Errorf("match_status = %q, want matched", movies[0].MatchStatus)
	}
}

// B: when the matcher's winning tmdb_id already belongs to another row, the
// files must move onto that row and the duplicate seed row must disappear -
// movies.tmdb_id is UNIQUE, so the old code would have errored instead.
func TestScan_MergesIntoRowOwningTheTMDBID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// an enriched row that lost its files (orphan), stored under its original title
	owner := &model.Movie{
		Title: "無間道", TitleEn: "Infernal Affairs", OriginalTitle: "無間道",
		Year: 2002, TMDBID: 10775,
	}
	if err := s.UpsertMovie(ctx, owner); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	rel := filepath.Join(root, "Infernal.Affairs.2002.BluRay.1080p.x264-GROUP")
	if err := os.MkdirAll(rel, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rel, "Infernal.Affairs.2002.BluRay.1080p.x264-GROUP.mkv"),
		[]byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary(ctx, "F", root, 0)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	sc := &scanner.Scanner{Store: s, Logger: log, FFProbe: fakeProbe,
		Matcher: matcher.New(fakeSearcher{
			"Infernal Affairs": {{ID: 10775, Title: "Infernal Affairs",
				OriginalTitle: "無間道", ReleaseDate: "2002-12-12"}},
		}, log)}

	if _, err := sc.ScanLibrary(ctx, lib); err != nil {
		t.Fatalf("scan: %v", err)
	}

	movies, err := s.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 {
		titles := make([]string, len(movies))
		for i, m := range movies {
			titles[i] = m.Title
		}
		t.Fatalf("expected the release to merge into the existing row, got %d rows: %v",
			len(movies), titles)
	}
	if movies[0].ID != owner.ID {
		t.Errorf("merged into the wrong row: got %d want %d", movies[0].ID, owner.ID)
	}
	files, err := s.ListMovieFiles(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("expected the file to hang off the owner row, got %d", len(files))
	}
}
