package metadata_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"log/slog"

	"github.com/moviegeek/darknight/internal/metadata"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/tmdb"
)

// enrichStubServer serves /movie/{id}: the full MovieDetails JSON for the
// primary (en-US) fetch, and just {"title":...} for the secondary (zh-CN)
// title fetch. primaryJSON must carry original_title, original_language and
// production_countries.
func enrichStubServer(t *testing.T, primaryJSON, secondaryTitle string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("language") == "zh-CN" {
			b, _ := json.Marshal(struct {
				Title string `json:"title"`
			}{Title: secondaryTitle})
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write([]byte(primaryJSON))
	})
	return httptest.NewServer(mux)
}

func newEnrichTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "enrich.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// TestEnrich_BilingualNonChinese: a Japanese-original film must fetch the
// secondary zh-CN title and store the country as an ISO code.
func TestEnrich_BilingualNonChinese(t *testing.T) {
	ctx := context.Background()
	st := newEnrichTestStore(t)
	primaryJSON := `{"id":123,"title":"Spirited Away",` +
		`"original_title":"千と千尋の神隠し","original_language":"ja",` +
		`"production_countries":[{"iso_3166_1":"JP","name":"Japan"}],"origin_country":["JP"]}`
	srv := enrichStubServer(t, primaryJSON, "千与千寻")
	defer srv.Close()

	c := tmdb.New("TESTKEY", "en-US", "zh-CN")
	c.BaseURL = srv.URL
	e := metadata.New(c, st, quietLogger())

	m := &model.Movie{Title: "Spirited Away", Year: 2001, TMDBID: 123}
	if ok, err := e.EnrichMovie(ctx, m); err != nil || !ok {
		t.Fatalf("enrich: ok=%v err=%v", ok, err)
	}

	got, err := st.GetMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("get movie: %v", err)
	}
	if got.OriginalTitle != "千と千尋の神隠し" {
		t.Errorf("original_title=%q", got.OriginalTitle)
	}
	if got.TitleEn != "Spirited Away" {
		t.Errorf("title_en=%q", got.TitleEn)
	}
	if got.TitleZh != "千与千寻" {
		t.Errorf("title_zh=%q (secondary fetch should have run)", got.TitleZh)
	}
	if got.OriginalLanguage != "ja" {
		t.Errorf("original_language=%q", got.OriginalLanguage)
	}
	if got.Country != "JP" {
		t.Errorf("country=%q want ISO code JP", got.Country)
	}
	if got.Title != "千と千尋の神隠し" {
		t.Errorf("title should be the original title, got %q", got.Title)
	}
}

// TestEnrich_BilingualChineseOriginal: a Chinese-original film must skip the
// secondary fetch (title_zh == original_title) and still store the ISO country.
func TestEnrich_BilingualChineseOriginal(t *testing.T) {
	ctx := context.Background()
	st := newEnrichTestStore(t)
	primaryJSON := `{"id":456,"title":"Farewell My Concubine",` +
		`"original_title":"霸王别姬","original_language":"zh",` +
		`"production_countries":[{"iso_3166_1":"CN","name":"China"}],"origin_country":["CN"]}`
	// If the secondary fetch erroneously ran, title_zh would become this:
	srv := enrichStubServer(t, primaryJSON, "SHOULD-NOT-BE-USED")
	defer srv.Close()

	c := tmdb.New("TESTKEY", "en-US", "zh-CN")
	c.BaseURL = srv.URL
	e := metadata.New(c, st, quietLogger())

	m := &model.Movie{Title: "Farewell My Concubine", Year: 1993, TMDBID: 456}
	if ok, err := e.EnrichMovie(ctx, m); err != nil || !ok {
		t.Fatalf("enrich: ok=%v err=%v", ok, err)
	}

	got, err := st.GetMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("get movie: %v", err)
	}
	if got.TitleZh != "霸王别姬" {
		t.Errorf("title_zh=%q, expected original_title (secondary fetch should be skipped)",
			got.TitleZh)
	}
	if got.TitleEn != "Farewell My Concubine" {
		t.Errorf("title_en=%q", got.TitleEn)
	}
	if got.Country != "CN" {
		t.Errorf("country=%q want ISO code CN", got.Country)
	}
}

// TestEnrichCollection_PartsCachedAndResolved: EnrichCollection must cache the
// TMDB collection's parts, and ListCollectionParts must resolve which of those
// parts correspond to a local movie (by tmdb_id) vs. which are missing. Also
// verifies ListCollectionsWithCount reports total_parts alongside movie_count.
func TestEnrichCollection_PartsCachedAndResolved(t *testing.T) {
	ctx := context.Background()
	st := newEnrichTestStore(t)

	// collection JSON returned by /collection/{id}: 3 parts, in release order.
	const collJSON = `{"id":863,"name":"Back to the Future Collection",` +
		`"poster_path":"/abc.jpg","backdrop_path":"/bg.jpg","overview":"trilogy","parts":[` +
		`{"id":105,"title":"Back to the Future","original_title":"Back to the Future","release_date":"1985-07-03","poster_path":"/p1.jpg","overview":"","vote_average":8.3},` +
		`{"id":166,"title":"Back to the Future Part II","original_title":"Back to the Future Part II","release_date":"1989-11-22","poster_path":"/p2.jpg","overview":"","vote_average":7.7},` +
		`{"id":196,"title":"Back to the Future Part III","original_title":"Back to the Future Part III","release_date":"1990-05-25","poster_path":"/p3.jpg","overview":"","vote_average":7.4}` +
		`]}`
	mux := http.NewServeMux()
	mux.HandleFunc("/collection/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(collJSON))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := tmdb.New("TESTKEY", "en-US", "zh-CN")
	c.BaseURL = srv.URL
	e := metadata.New(c, st, quietLogger())

	// seed a collection row + one local movie that matches part 105 (owned);
	// parts 166 and 196 will be "missing".
	colID, err := st.UpsertCollection(ctx, &tmdb.Collection{ID: 863, Name: "Back to the Future Collection"})
	if err != nil {
		t.Fatalf("upsert collection: %v", err)
	}
	if err := st.UpsertMovie(ctx, &model.Movie{
		Title: "Back to the Future", Year: 1985, TMDBID: 105, CollectionID: colID,
	}); err != nil {
		t.Fatalf("upsert movie: %v", err)
	}

	ok, err := e.EnrichCollection(ctx, colID)
	if err != nil || !ok {
		t.Fatalf("EnrichCollection: ok=%v err=%v", ok, err)
	}

	parts, err := st.ListCollectionParts(ctx, colID)
	if err != nil {
		t.Fatalf("ListCollectionParts: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	// release order preserved
	if parts[0].Title != "Back to the Future" || parts[2].Title != "Back to the Future Part III" {
		t.Errorf("parts out of order: %q, %q, %q", parts[0].Title, parts[1].Title, parts[2].Title)
	}
	// part 105 owned (local movie id set), the other two missing
	if parts[0].LocalMovieID == 0 {
		t.Errorf("part 105 should be owned, got local_movie_id=0")
	}
	if parts[1].LocalMovieID != 0 || parts[2].LocalMovieID != 0 {
		t.Errorf("parts 166/196 should be missing, got local ids %d, %d",
			parts[1].LocalMovieID, parts[2].LocalMovieID)
	}

	// ListCollectionsWithCount must report movie_count=1 and total_parts=3.
	cols, err := st.ListCollectionsWithCount(ctx, 1)
	if err != nil {
		t.Fatalf("ListCollectionsWithCount: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("got %d collections, want 1", len(cols))
	}
	if cols[0].MovieCount != 1 {
		t.Errorf("movie_count=%d want 1", cols[0].MovieCount)
	}
	if cols[0].TotalParts != 3 {
		t.Errorf("total_parts=%d want 3", cols[0].TotalParts)
	}
}
