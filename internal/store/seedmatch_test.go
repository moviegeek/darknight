package store_test

import (
	"context"
	"testing"

	"github.com/moviegeek/darknight/internal/model"
)

// Two different films from the SAME year must never collapse onto one movie
// row. Regression test for the seed matcher comparing title_en/original_title
// against an empty string (which matched any unenriched row of that year and
// merged unrelated films - observed as 7 different 2017 films on one row).
func TestUpsertMovieSeed_SameYearDifferentTitlesStaySeparate(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	a := &model.Movie{Title: "Three Billboards Outside Ebbing Missouri", Year: 2017}
	if err := s.UpsertMovieSeed(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := &model.Movie{Title: "Der Hauptmann", Year: 2017}
	if err := s.UpsertMovieSeed(ctx, b); err != nil {
		t.Fatal(err)
	}
	c := &model.Movie{Title: "Claires Camera", Year: 2017}
	if err := s.UpsertMovieSeed(ctx, c); err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.ID == c.ID || b.ID == c.ID {
		t.Fatalf("distinct 2017 films collapsed onto one row: %d/%d/%d", a.ID, b.ID, c.ID)
	}

	// re-seeding the same title+year must reattach, not duplicate
	again := &model.Movie{Title: "Der Hauptmann", Year: 2017}
	if err := s.UpsertMovieSeed(ctx, again); err != nil {
		t.Fatal(err)
	}
	if again.ID != b.ID {
		t.Fatalf("re-seed created a duplicate: got %d want %d", again.ID, b.ID)
	}
}

// An enriched row whose display title became the original-language title must
// still be found by the English name parsed from the directory.
func TestUpsertMovieSeed_MatchesEnrichedByTitleEn(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	enriched := &model.Movie{
		Title: "烏鴉與麻雀", OriginalTitle: "烏鴉與麻雀", TitleEn: "Crows and Sparrows",
		Year: 1949, TMDBID: 100914,
	}
	if err := s.UpsertMovie(ctx, enriched); err != nil {
		t.Fatal(err)
	}
	seed := &model.Movie{Title: "Crows and Sparrows", Year: 1949}
	if err := s.UpsertMovieSeed(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if seed.ID != enriched.ID {
		t.Fatalf("seed did not reattach to the enriched row: got %d want %d", seed.ID, enriched.ID)
	}
}
