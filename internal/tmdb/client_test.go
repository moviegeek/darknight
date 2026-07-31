package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer returns a test server that responds to /movie/{id} and /search/movie.
func stubServer(t *testing.T, movieJSON, searchJSON string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/movie/", func(w http.ResponseWriter, r *http.Request) {
		// verify api_key + language were appended. The primary fetch is zh-CN;
		// the secondary title fetch (GetMovieTitle) uses the alt language en-US.
		q := r.URL.Query()
		if q.Get("api_key") != "TESTKEY" {
			t.Errorf("missing api_key, got %q", q.Get("api_key"))
		}
		if lang := q.Get("language"); lang != "zh-CN" && lang != "en-US" {
			t.Errorf("expected zh-CN or en-US language, got %q", lang)
		}
		if strings.Contains(r.URL.Path, "/999") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(movieJSON))
	})
	mux.HandleFunc("/search/movie", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "Empty Results" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchJSON))
	})
	mux.HandleFunc("/find/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"movie_results":[{"id":42,"title":"Found"}]}`))
	})
	return httptest.NewServer(mux)
}

func TestClient_GetMovie(t *testing.T) {
	movie := MovieDetails{
		ID: 123, Title: "Test", IMDBID: "tt0000001",
		Genres: []Genre{{ID: 1, Name: "Drama"}},
		Credits: Credits{
			Cast: []CastMember{{ID: 1, Name: "Actor", Order: 0}},
			Crew: []CrewMember{{ID: 2, Name: "Director", Job: "Director"}},
		},
	}
	b, _ := json.Marshal(movie)
	srv := stubServer(t, string(b), "")
	defer srv.Close()

	c := New("TESTKEY", "zh-CN", "en-US")
	c.BaseURL = srv.URL
	got, err := c.GetMovie(context.Background(), 123)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if got.ID != 123 || got.Title != "Test" {
		t.Errorf("got %+v", got)
	}
	if len(got.Genres) != 1 || got.Genres[0].Name != "Drama" {
		t.Errorf("genres wrong: %+v", got.Genres)
	}
	if len(got.Credits.Cast) != 1 {
		t.Errorf("cast wrong: %+v", got.Credits.Cast)
	}
}

func TestClient_GetMovie_NotFound(t *testing.T) {
	srv := stubServer(t, `{}`, "")
	defer srv.Close()
	c := New("TESTKEY", "zh-CN", "en-US")
	c.BaseURL = srv.URL
	_, err := c.GetMovie(context.Background(), 999)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_SearchMovie(t *testing.T) {
	srv := stubServer(t, "", `{"results":[{"id":1,"title":"Hit"}]}`)
	defer srv.Close()
	c := New("TESTKEY", "zh-CN", "en-US")
	c.BaseURL = srv.URL
	got, err := c.SearchMovie(context.Background(), "Hit", 2000)
	if err != nil {
		t.Fatalf("SearchMovie: %v", err)
	}
	if got.ID != 1 || got.Title != "Hit" {
		t.Errorf("got %+v", got)
	}
}

func TestClient_SearchMovie_Empty(t *testing.T) {
	srv := stubServer(t, "", ``)
	defer srv.Close()
	c := New("TESTKEY", "zh-CN", "en-US")
	c.BaseURL = srv.URL
	_, err := c.SearchMovie(context.Background(), "Empty Results", 0)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestClient_FindByIMDB(t *testing.T) {
	srv := stubServer(t, "", "")
	defer srv.Close()
	c := New("TESTKEY", "zh-CN", "en-US")
	c.BaseURL = srv.URL
	got, err := c.FindByIMDB(context.Background(), "tt0000001")
	if err != nil {
		t.Fatalf("FindByIMDB: %v", err)
	}
	if got.ID != 42 {
		t.Errorf("got id=%d, want 42", got.ID)
	}
}

func TestClient_Disabled(t *testing.T) {
	c := New("", "", "")
	if c.Enabled() {
		t.Fatal("empty-key client should be disabled")
	}
	_, err := c.GetMovie(context.Background(), 1)
	if err != ErrNoAPIKey {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestClient_GetMovieTitle(t *testing.T) {
	movie := MovieDetails{ID: 123, Title: "Localized Title"}
	b, _ := json.Marshal(movie)
	srv := stubServer(t, string(b), "")
	defer srv.Close()

	c := New("TESTKEY", "zh-CN", "en-US")
	c.BaseURL = srv.URL
	// secondary fetch: plain /movie/{id}?language=en-US, only title needed.
	got, err := c.GetMovieTitle(context.Background(), 123, "en-US")
	if err != nil {
		t.Fatalf("GetMovieTitle: %v", err)
	}
	if got != "Localized Title" {
		t.Fatalf("got %q want Localized Title", got)
	}
}

func TestMovieDetails_ProductionCountries(t *testing.T) {
	raw := `{"id":1,"title":"X","original_title":"X","original_language":"ja",` +
		`"production_countries":[{"iso_3166_1":"JP","name":"Japan"}],"origin_country":["JP"]}`
	var d MovieDetails
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.OriginalLanguage != "ja" || d.OriginalTitle != "X" {
		t.Fatalf("original fields wrong: %+v", d)
	}
	if len(d.ProductionCountries) != 1 || d.ProductionCountries[0].ISO3166_1 != "JP" ||
		d.ProductionCountries[0].Name != "Japan" {
		t.Fatalf("production_countries wrong: %+v", d.ProductionCountries)
	}
	if len(d.OriginCountry) != 1 || d.OriginCountry[0] != "JP" {
		t.Fatalf("origin_country wrong: %+v", d.OriginCountry)
	}
}

func TestImageURL(t *testing.T) {
	got := ImageURL("/abc.jpg", "w500")
	want := "https://image.tmdb.org/t/p/w500/abc.jpg"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if ImageURL("", "w500") != "" {
		t.Error("empty path should return empty URL")
	}
}
