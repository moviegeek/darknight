package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/moviegeek/darknight/internal/api"
	"github.com/moviegeek/darknight/internal/config"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/scanner"
	"github.com/moviegeek/darknight/internal/server"
	"github.com/moviegeek/darknight/internal/store"
)

// newApp spins up a store + api + server backed by a temp DB and returns a
// test server plus the store so tests can seed data directly.
func newApp(t *testing.T) (*store.Store, *httptest.Server) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "api.db")
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	sc := scanner.New(st, log)
	apih := api.New(st, sc, log, nil)
	cfg := &config.Config{DatabasePath: dbPath, HTTPAddr: ":0"}
	handler := server.New(cfg, apih, log)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return st, srv
}

func TestHealth(t *testing.T) {
	_, srv := newApp(t)
	res, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var body map[string]string
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestLibraryLifecycleAndScan(t *testing.T) {
	st, srv := newApp(t)
	root := t.TempDir()
	ctx := context.Background()

	// create library
	body, _ := json.Marshal(map[string]interface{}{
		"name": "Films", "root_path": root,
	})
	res, err := http.Post(srv.URL+"/api/libraries", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", res.StatusCode)
	}
	var lib map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&lib)
	libID := int64(lib["id"].(float64))

	// list libraries
	res, _ = http.Get(srv.URL + "/api/libraries")
	var libs []map[string]interface{}
	_ = json.NewDecoder(res.Body).Decode(&libs)
	if len(libs) != 1 {
		t.Fatalf("expected 1 library, got %d", len(libs))
	}

	// drop a release into the root and trigger a scan
	relDir := filepath.Join(root, "Akira.1988.720p.BluRay.DD+5.1.x264-DON")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(relDir, "Akira.1988.720p.BluRay.DD+5.1.x264-DON.mkv"),
		[]byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ = http.Post(srv.URL+"/api/libraries/"+strconv.FormatInt(libID, 10)+"/scan",
		"application/json", nil)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("scan status = %d", res.StatusCode)
	}

	// the scan runs in a goroutine; poll for the movie AND its file to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res, _ := http.Get(srv.URL + "/api/movies")
		var page struct {
			Items []map[string]interface{} `json:"items"`
			Total int                      `json:"total"`
		}
		_ = json.NewDecoder(res.Body).Decode(&page)
		if page.Total > 0 && len(page.Items) > 0 {
			if page.Items[0]["title"] != "Akira" {
				t.Fatalf("unexpected title: %v", page.Items[0]["title"])
			}
			// movie files endpoint should return 1 file
			movieID := int64(page.Items[0]["id"].(float64))
			res2, _ := http.Get(srv.URL + "/api/movies/" + strconv.FormatInt(movieID, 10) + "/files")
			var files []map[string]interface{}
			_ = json.NewDecoder(res2.Body).Decode(&files)
			if len(files) == 1 {
				if files[0]["resolution"] != "720p" {
					t.Fatalf("unexpected resolution: %v", files[0]["resolution"])
				}
				return
			}
			// files not yet written; keep polling
		}
		_ = st
		_ = ctx
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for async scan")
}

func TestListMoviesFilters(t *testing.T) {
	st, srv := newApp(t)
	ctx := context.Background()

	m1 := mustMovie(t, st, ctx, "Casino", 1995, "tt0112641", 524)
	mustFile(t, st, ctx, m1, "Casino.1995.UHD.BluRay.2160p.10bit.HDR.DTS-X.7.1.x265-beAst",
		"2160p", "UHD BluRay", "x265", "HDR10", true)
	m2 := mustMovie(t, st, ctx, "Casablanca", 1942, "tt0034583", 0)
	mustFile(t, st, ctx, m2, "Casablanca.1942.BluRay.1080p.x264-CMCT",
		"1080p", "BluRay", "x264", "", false)

	check := func(query string, wantTotal int) {
		t.Helper()
		url := srv.URL + "/api/movies" + query
		res, _ := http.Get(url)
		var page struct {
			Items []map[string]interface{} `json:"items"`
			Total int                      `json:"total"`
		}
		_ = json.NewDecoder(res.Body).Decode(&page)
		if page.Total != wantTotal {
			t.Fatalf("query %q -> total=%d, want %d (items=%v)", query, page.Total, wantTotal, page.Items)
		}
	}
	check("?resolution=2160p", 1)
	check("?hdr=HDR10", 1)
	check("?dolby_vision=true", 1)
	check("?q=Cas", 2) // Casino + Casablanca both start with "Cas"
	check("", 2)
}

// TestListMovies_CountryFilterAndFields verifies the country filter (ISO code)
// narrows the list and that the bilingual title fields surface in the response.
func TestListMovies_CountryFilterAndFields(t *testing.T) {
	st, srv := newApp(t)
	ctx := context.Background()

	seed := []*model.Movie{
		{Title: "Matrix", Year: 1999, TMDBID: 603,
			OriginalTitle: "The Matrix", OriginalLanguage: "en",
			TitleEn: "The Matrix", TitleZh: "黑客帝国", Country: "US"},
		{Title: "Akira", Year: 1988, TMDBID: 149,
			OriginalTitle: "AKIRA", OriginalLanguage: "ja",
			TitleEn: "Akira", TitleZh: "阿基拉", Country: "JP"},
	}
	for _, m := range seed {
		if err := st.UpsertMovie(ctx, m); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	get := func(query string) (int, map[string]interface{}) {
		t.Helper()
		res, err := http.Get(srv.URL + "/api/movies" + query)
		if err != nil {
			t.Fatalf("get %q: %v", query, err)
		}
		var page struct {
			Items []map[string]interface{} `json:"items"`
			Total int                      `json:"total"`
		}
		_ = json.NewDecoder(res.Body).Decode(&page)
		var first map[string]interface{}
		if len(page.Items) > 0 {
			first = page.Items[0]
		}
		return page.Total, first
	}

	if n, _ := get("?country=US"); n != 1 {
		t.Fatalf("?country=US -> %d, want 1", n)
	}
	if n, _ := get("?country=JP"); n != 1 {
		t.Fatalf("?country=JP -> %d, want 1", n)
	}
	if n, _ := get("?country=XX"); n != 0 {
		t.Fatalf("?country=XX -> %d, want 0", n)
	}

	// the response surfaces the bilingual fields (movieListItem embeds model.Movie).
	n, item := get("?country=JP")
	if n != 1 || item == nil {
		t.Fatalf("expected 1 JP movie, got %d (item=%v)", n, item)
	}
	if item["original_title"] != "AKIRA" || item["title_en"] != "Akira" ||
		item["title_zh"] != "阿基拉" || item["original_language"] != "ja" ||
		item["country"] != "JP" {
		t.Fatalf("bilingual fields missing/wrong in response: %+v", item)
	}
}

func mustMovie(t *testing.T, st *store.Store, ctx context.Context, title string, year int, imdb string, tmdb int64) int64 {
	m := &model.Movie{Title: title, Year: year, IMDBID: imdb, TMDBID: tmdb}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("upsert movie %s: %v", title, err)
	}
	return m.ID
}

func mustFile(t *testing.T, st *store.Store, ctx context.Context, movieID int64,
	rawName, res, source, codec, hdr string, dv bool) {
	lib, err := st.CreateLibrary(ctx, "L"+strconv.FormatInt(movieID, 10), "/tmp/"+rawName, 0)
	if err != nil {
		t.Fatalf("create lib: %v", err)
	}
	mf := &model.MovieFile{
		MovieID: movieID, LibraryID: lib.ID, DirPath: rawName, FileName: rawName + ".mkv",
		RawName: rawName, Resolution: res, Source: source, VideoCodec: codec,
		HDR: hdr, DolbyVision: dv,
	}
	if err := st.UpsertMovieFile(ctx, mf); err != nil {
		t.Fatalf("upsert file: %v", err)
	}
}

// keep fmt import alive
var _ = fmt.Sprintf
