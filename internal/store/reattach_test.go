package store_test

import (
	"context"
	"testing"

	"github.com/moviegeek/darknight/internal/model"
)

// C: a release whose title differs from the enriched row only in case,
// punctuation or diacritics must REATTACH, not insert a duplicate. This is the
// 169-duplicates-vs-148-orphans regression.
func TestUpsertMovieSeed_ReattachesViaFoldedKey(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	cases := []struct {
		name        string
		stored      model.Movie // enriched row as TMDB wrote it
		parsedTitle string      // what the scanner parses from the release name
		parsedYear  int
	}{
		{"case only", model.Movie{Title: "As Good as It Gets", TitleEn: "As Good as It Gets",
			Year: 1997, TMDBID: 2898}, "As Good As It Gets", 1997},
		{"diacritics", model.Movie{Title: "Człowiek z marmuru", TitleEn: "Man of Marble",
			Year: 1977, TMDBID: 224}, "Czlowiek z marmuru", 1977},
		{"dropped dots", model.Movie{Title: "Fantastic Mr. Fox", TitleEn: "Fantastic Mr. Fox",
			Year: 2009, TMDBID: 10315}, "Fantastic Mr Fox", 2009},
		{"year drift", model.Movie{Title: "Following", TitleEn: "Following",
			Year: 1999, TMDBID: 11660}, "Following", 1998},
		{"commas", model.Movie{Title: "Jeanne Dielman, 23, quai du Commerce, 1080 Bruxelles",
			Year: 1976, TMDBID: 12345}, "Jeanne Dielman 23 Quai du Commerce 1080 Bruxelles", 1975},
	}
	for _, c := range cases {
		stored := c.stored
		if err := s.UpsertMovie(ctx, &stored); err != nil {
			t.Fatalf("%s: seed enriched row: %v", c.name, err)
		}
		seed := &model.Movie{Title: c.parsedTitle, Year: c.parsedYear}
		if err := s.UpsertMovieSeed(ctx, seed); err != nil {
			t.Fatalf("%s: seed: %v", c.name, err)
		}
		if seed.ID != stored.ID {
			t.Errorf("%s: %q did not reattach to %q (got id %d want %d)",
				c.name, c.parsedTitle, stored.Title, seed.ID, stored.ID)
		}
	}
}

// The folded key must not merge different films that happen to fold alike but
// sit years apart (remakes), nor films with genuinely different titles.
func TestUpsertMovieSeed_FoldedKeyDoesNotOvermerge(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	orig := &model.Movie{Title: "Ikiru", TitleEn: "Ikiru", Year: 1952, TMDBID: 3782}
	if err := s.UpsertMovie(ctx, orig); err != nil {
		t.Fatal(err)
	}
	// a 2022 remake-ish title that folds identically must stay separate
	remake := &model.Movie{Title: "Ikiru", Year: 2022}
	if err := s.UpsertMovieSeed(ctx, remake); err != nil {
		t.Fatal(err)
	}
	if remake.ID == orig.ID {
		t.Fatalf("1952 and 2022 same-key films merged (id %d)", remake.ID)
	}
}

// B: DeleteMovieIfEmpty must refuse to delete a row that still owns files.
func TestDeleteMovieIfEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	lib, err := s.CreateLibrary(ctx, "F", "/m", 0)
	if err != nil {
		t.Fatal(err)
	}
	keep := &model.Movie{Title: "Heat", Year: 1995}
	if err := s.UpsertMovieSeed(ctx, keep); err != nil {
		t.Fatal(err)
	}
	mf := &model.MovieFile{MovieID: keep.ID, LibraryID: lib.ID,
		DirPath: "Heat", FileName: "Heat.mkv", RawName: "Heat"}
	if err := s.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMovieIfEmpty(ctx, keep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMovie(ctx, keep.ID); err != nil {
		t.Fatalf("row with files was deleted: %v", err)
	}

	// once its files are detached, the same call removes it
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE movie_files SET movie_id = NULL WHERE movie_id = ?`, keep.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMovieIfEmpty(ctx, keep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMovie(ctx, keep.ID); err == nil {
		t.Fatal("empty row should have been deleted")
	}
}
