package matcher

import (
	"context"
	"testing"

	"github.com/moviegeek/darknight/internal/tmdb"
)

func TestScore(t *testing.T) {
	cases := []struct {
		name             string
		title, original  string
		candYear         int
		query            string
		queryYear        int
		rank             int
		wantMin, wantMax float64 // expected score window
	}{
		{
			name: "exact title + year", title: "In Bruges", candYear: 2008,
			query: "In Bruges", queryYear: 2008, rank: 0,
			wantMin: 100, wantMax: 100,
		},
		{
			name:  "wrong year, right title (Spirited Away 2011 vs 2001)",
			title: "Spirited Away", original: "千と千尋の神隠し", candYear: 2001,
			query: "Spirited Away", queryYear: 2011, rank: 0,
			wantMin: 60, wantMax: 70, // exact-title shortcircuit needs yearDiff<=1, so no 100; titleSim=1
		},
		{
			name: "typo token (Grean vs Green)", title: "Green Snake", original: "青蛇", candYear: 1993,
			query: "Grean Snake", queryYear: 1993, rank: 0,
			wantMin: 90, wantMax: 100,
		},
		{
			name:  "original title match (Doro no kawa)",
			title: "Doro no kawa", original: "泥の河", candYear: 1981,
			query: "Doro no kawa", queryYear: 1981, rank: 0,
			wantMin: 100, wantMax: 100,
		},
		{
			name: "different movie entirely", title: "Jaws", candYear: 1975,
			query: "JFK", queryYear: 1991, rank: 0,
			wantMin: 0, wantMax: 30, // whole-string levenshtein gives small nonzero similarity
		},
		{
			name: "query year unknown", title: "Z", candYear: 1969,
			query: "Z", queryYear: 0, rank: 0,
			wantMin: 95, wantMax: 100,
		},
	}
	for _, c := range cases {
		got := Score(c.title, c.original, c.candYear, c.query, c.queryYear, c.rank)
		if got < c.wantMin || got > c.wantMax {
			t.Errorf("%s: Score = %v, want [%v, %v]", c.name, got, c.wantMin, c.wantMax)
		}
	}
}

func TestScoreTearsGoBy(t *testing.T) {
	// "As Tears Goes By" (release typo) vs TMDB "As Tears Go By" (1988).
	got := Score("As Tears Go By", "", 1988, "As Tears Goes By", 1988, 0)
	// tokens: [as tears goes by] vs [as tears go by]: fuzzyEq(goes,go) fails
	// (distance 2), Jaccard = 3/4; whole-string levenshtein: 1 edit of 16 chars.
	if got < 70 {
		t.Errorf("As Tears Goes By score = %v, want >= 70 (should pending-accept)", got)
	}
}

// fakeSearcher replays canned queries.
type fakeSearcher map[string][]tmdb.SearchResult

func (f fakeSearcher) SearchMovieAll(_ context.Context, query string, limit int) ([]tmdb.SearchResult, error) {
	rs, ok := f[query]
	if !ok || len(rs) == 0 {
		return nil, tmdb.ErrNotFound
	}
	if limit > 0 && len(rs) > limit {
		rs = rs[:limit]
	}
	return rs, nil
}

func TestMatchAccept(t *testing.T) {
	fs := fakeSearcher{
		"JFK": {
			{ID: 1, Title: "JFK", ReleaseDate: "1991-12-20"},
			{ID: 2, Title: "JFK: Reloaded", ReleaseDate: "2004-01-01"},
		},
	}
	m := New(fs, nil)
	res, err := m.Match(context.Background(), []string{"JFK"}, 1991)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionAccept || res.Best == nil || res.Best.TMDBID != 1 {
		t.Fatalf("decision=%v best=%+v", res.Decision, res.Best)
	}
}

func TestMatchVariantFallback(t *testing.T) {
	// primary variant finds nothing; the AKA fallback hits.
	fs := fakeSearcher{
		"High Heels": {
			{ID: 9, Title: "High Heels", ReleaseDate: "1991-10-25"},
		},
	}
	m := New(fs, nil)
	res, err := m.Match(context.Background(), []string{"Tacones Lejanos", "High Heels"}, 1991)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionAccept || res.Best.TMDBID != 9 {
		t.Fatalf("decision=%v best=%+v", res.Decision, res.Best)
	}
}

func TestMatchNoCandidates(t *testing.T) {
	m := New(fakeSearcher{}, nil)
	res, err := m.Match(context.Background(), []string{"Nothing"}, 1990)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != DecisionNone || res.Best != nil {
		t.Fatalf("decision=%v best=%+v", res.Decision, res.Best)
	}
}
