// Package api implements the REST API consumed by the web frontend. All routes
// are mounted under /api. Handlers stay thin: they parse request parameters,
// call the store / scanner, and serialise JSON.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/moviegeek/darknight/internal/matcher"
	"github.com/moviegeek/darknight/internal/metadata"
	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/scanner"
	"github.com/moviegeek/darknight/internal/store"
)

// API holds the dependencies shared by all handlers.
type API struct {
	Store    *store.Store
	Scanner  *scanner.Scanner
	Enricher *metadata.Enricher
	// Matcher powers the manual-match endpoints (live candidate search and
	// batch rematch). Nil when TMDB is not configured.
	Matcher  *matcher.Matcher
	Logger   *slog.Logger

	mu       sync.Mutex
	scanLock map[int64]struct{} // libraries currently being scanned
	// collectionEnrichRunning guards the batch "refresh all collections" job so
	// a second request while one is in flight returns 409 instead of stacking
	// duplicate TMDB load.
	collectionEnrichRunning bool
}

// New returns an API with the given deps and an empty scan lock map.
func New(s *store.Store, sc *scanner.Scanner, log *slog.Logger, enricher *metadata.Enricher) *API {
	if log == nil {
		log = slog.Default()
	}
	return &API{Store: s, Scanner: sc, Enricher: enricher, Logger: log, scanLock: make(map[int64]struct{})}
}

// Router mounts all API routes. Caller is responsible for adding middleware
// (CORS, logging) and the SPA fallback for non-/api paths.
func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", a.health)

	r.Get("/libraries", a.listLibraries)
	r.Post("/libraries", a.createLibrary)
	r.Get("/libraries/{id}", a.getLibrary)
	r.Delete("/libraries/{id}", a.deleteLibrary)
	r.Post("/libraries/{id}/scan", a.scanLibrary)

	r.Get("/movies", a.listMovies)
	r.Get("/movies/facets", a.movieFacets)
	r.Get("/movies/{id}", a.getMovie)
	r.Get("/movies/{id}/cast", a.getMovieCast)
	r.Get("/movies/{id}/files", a.listMovieFiles)
	r.Get("/movies/{id}/files/{fid}", a.getMovieFile)

	r.Get("/matches/pending", a.listPendingMatches)
	r.Get("/movies/{id}/candidates", a.movieCandidates)
	r.Post("/movies/{id}/match", a.matchMovie)
	r.Post("/movies/{id}/unmatch", a.unmatchMovie)
	r.Post("/movies/{id}/rename", a.renameMovieRelease)
	r.Post("/matches/rematch", a.rematchAll)

	r.Get("/countries", a.listCountries)
	r.Get("/genres", a.listGenres)
	r.Get("/collections", a.listCollections)
	r.Get("/collections/{id}", a.getCollection)
	r.Get("/collections/{id}/parts", a.listCollectionParts)
	r.Post("/collections/{id}/enrich", a.enrichCollection)
	r.Post("/collections/enrich-all", a.enrichAllCollections)

	r.Get("/dev/tables", a.listTables)
	r.Post("/dev/sql", a.execSQL)
	return r
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Warn("write json", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseInt64(r *http.Request, key string) (int64, error) {
	v := chi.URLParam(r, key)
	if v == "" {
		return 0, errors.New("missing " + key)
	}
	return strconv.ParseInt(v, 10, 64)
}

func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func queryBool(r *http.Request, key string) bool {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return false
	}
	b, err := strconv.ParseBool(raw)
	if err == nil {
		return b
	}
	return raw == "1" || raw == "true"
}

// ---------- health ----------

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- libraries ----------

func (a *API) listLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := a.Store.ListLibraries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, libs)
}

type createLibraryReq struct {
	Name         string `json:"name"`
	RootPath     string `json:"root_path"`
	ScanInterval int    `json:"scan_interval"`
}

func (a *API) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req createLibraryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Name == "" || req.RootPath == "" {
		writeError(w, http.StatusBadRequest, "name and root_path required")
		return
	}
	lib, err := a.Store.CreateLibrary(r.Context(), req.Name, req.RootPath, req.ScanInterval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lib)
}

func (a *API) getLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lib, err := a.Store.GetLibrary(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lib)
}

func (a *API) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.Store.DeleteLibrary(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scanLibrary triggers an async scan. The response returns immediately with
// the scan started; subsequent GET /libraries/{id} polls last_scan_at.
func (a *API) scanLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lib, err := a.Store.GetLibrary(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.mu.Lock()
	if _, running := a.scanLock[id]; running {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "scan already running")
		return
	}
	a.scanLock[id] = struct{}{}
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.scanLock, id)
			a.mu.Unlock()
		}()
		ctx, cancel := contextWithTimeout(30 * time.Minute)
		defer cancel()
		stats, err := a.Scanner.ScanLibrary(ctx, lib)
		if err != nil {
			a.Logger.Error("scan failed", "library", lib.Name, "err", err)
			return
		}
		a.Logger.Info("scan done", "library", lib.Name,
			"added", stats.Added, "updated", stats.Updated,
			"unchanged", stats.Unchanged, "removed", stats.Removed, "errors", stats.Errors)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// ---------- movies ----------

// movieListItem is a Movie plus a few denormalised fields the grid needs
// (best file resolution/size/subtitle) so the client can badge without a second call.
type movieListItem struct {
	model.Movie
	HasFiles            bool   `json:"has_files"`
	FileCount           int    `json:"file_count"` // how many movie_files map here (>1 = multi-version)
	BestResolution      string `json:"best_resolution"`
	BestSource          string `json:"best_source"`
	BestHDR             string `json:"best_hdr"`
	DolbyVision         bool   `json:"dolby_vision"`
	Watched             bool   `json:"watched"`
	HasChiSubtitle      bool   `json:"has_chi_subtitle"`
	HasExternalSubtitle bool   `json:"has_external_subtitle"`
}

// splitCSV splits a comma-separated query value into trimmed, non-empty
// parts. Returns nil for an empty input so it plugs directly into a filter
// field that means "no filter" when empty/nil.
func splitCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseMovieFilter reads the dimension filters shared by /movies and
// /movies/facets out of the request's query string.
func parseMovieFilter(r *http.Request) store.MovieFilter {
	q := r.URL.Query()
	f := store.MovieFilter{
		Query:            q.Get("q"),
		Genres:           q["genre"],
		YearFrom:         queryInt(r, "year_from", 0),
		YearTo:           queryInt(r, "year_to", 0),
		Resolution:       q.Get("resolution"),
		Source:           q.Get("source"),
		VideoCodec:       q.Get("codec"),
		HDR:              q.Get("hdr"),
		DolbyVision:      queryBool(r, "dolby_vision"),
		Watched:          q.Get("watched"),
		Countries:        splitCSV(q.Get("country")),
		SubtitleLang:     q.Get("subtitle_lang"),
		ExternalSubtitle: queryBool(r, "external_subtitle"),
		NoChiSubtitle:    queryBool(r, "no_chi_subtitle"),
		MatchIssue:       q.Get("match_issue"),
		MatchStatus:      q.Get("match_status"),
	}
	if c := q.Get("collection"); c != "" {
		if id, err := strconv.ParseInt(c, 10, 64); err == nil {
			f.Collection = id
		}
	}
	return f
}

func (a *API) listMovies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := parseMovieFilter(r)

	sortField := q.Get("sort")
	if sortField == "" {
		sortField = "title"
	}
	opts := store.ListMoviesOpts{
		Filter: f,
		Sort:   store.MovieSort{Field: sortField, Desc: queryBool(r, "desc")},
		Limit:  queryInt(r, "limit", 50),
		Offset: queryInt(r, "offset", 0),
	}

	movies, err := a.Store.ListMovies(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	total, err := a.Store.CountMovies(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// enrich each movie with its best file summary
	items := make([]movieListItem, 0, len(movies))
	for _, m := range movies {
		files, err := a.Store.ListMovieFiles(r.Context(), m.ID)
		if err != nil {
			a.Logger.Warn("list files for movie", "id", m.ID, "err", err)
		}
		item := movieListItem{Movie: m, HasFiles: len(files) > 0, FileCount: len(files)}
		for _, f := range files {
			if item.BestResolution == "" {
				// first file is the best (ordered by resolution/size)
				item.BestResolution = f.Resolution
				item.BestSource = f.Source
				item.BestHDR = f.HDR
				item.DolbyVision = f.DolbyVision
			}
			if hasChineseLang(f.SubtitleLanguages) {
				item.HasChiSubtitle = true
			}
			if f.HasExternalSubtitle {
				item.HasExternalSubtitle = true
			}
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":  items,
		"total":  total,
		"limit":  opts.Limit,
		"offset": opts.Offset,
	})
}

// facet candidate values mirror the chip lists in the frontend's
// FilterPanel. Keeping them here lets /movies/facets report a count for
// every chip without a GROUP BY per dimension (some chips filter on a LIKE
// match, not an exact column value, so grouping isn't straightforward).
// Country codes aren't listed here - the panel builds its country chips
// dynamically from GET /countries, so movieFacets queries against that same
// dynamic list instead of a fixed one.
var (
	facetResolutions = []string{"2160p", "1080p", "720p"}
	facetSources     = []string{"BluRay", "UHD BluRay", "Bluray Disk", "WebDL", "HDTV"}
	facetCodecs      = []string{"x265", "x264", "AVC", "HEVC"}
	facetHDRs        = []string{"HDR10", "HDR10+", "DV"}
	facetSubLangs    = []string{"chi", "eng", "jpn", "kor"}
	facetWatched     = []string{"unwatched", "watching", "watched"}
	// facetMatchIssues are the data-health buckets; "unmatched" is the union
	// of "no_files" and "no_tmdb" so its count is not their sum.
	facetMatchIssues = []string{"unmatched", "no_files", "no_tmdb", "multi_version"}
	// facetMatchStatuses are the state-machine values. Use a single-select chip
	// group in the UI: pick one to filter to that bucket.
	facetMatchStatuses = []string{"matched", "pending", "unmatched", "manual"}
)

// movieFacets reports, for the current filter selection, how many movies
// would match if each chip were (additionally) selected. Each dimension is
// counted independently against every *other* active filter - e.g. the
// resolution counts reflect the current source/codec/... filters but ignore
// whatever resolution is already selected, so the panel can show "how many
// if I picked this" rather than collapsing to just the active choice.
func (a *API) movieFacets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	base := parseMovieFilter(r)

	countWith := func(mutate func(*store.MovieFilter)) (int, error) {
		f := base
		mutate(&f)
		return a.Store.CountMovies(ctx, f)
	}
	countEach := func(values []string, mutate func(*store.MovieFilter, string)) (map[string]int, error) {
		out := make(map[string]int, len(values))
		for _, v := range values {
			n, err := countWith(func(f *store.MovieFilter) { mutate(f, v) })
			if err != nil {
				return nil, err
			}
			out[v] = n
		}
		return out, nil
	}

	resolution, err := countEach(facetResolutions, func(f *store.MovieFilter, v string) { f.Resolution = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	source, err := countEach(facetSources, func(f *store.MovieFilter, v string) { f.Source = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	codec, err := countEach(facetCodecs, func(f *store.MovieFilter, v string) { f.VideoCodec = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hdr, err := countEach(facetHDRs, func(f *store.MovieFilter, v string) { f.HDR = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	countryCounts, err := a.Store.CountryCounts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	countryCodes := make([]string, len(countryCounts))
	for i, c := range countryCounts {
		countryCodes[i] = c.Code
	}
	country, err := countEach(countryCodes, func(f *store.MovieFilter, v string) { f.Countries = []string{v} })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subtitleLang, err := countEach(facetSubLangs, func(f *store.MovieFilter, v string) { f.SubtitleLang = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	watched, err := countEach(facetWatched, func(f *store.MovieFilter, v string) { f.Watched = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dolbyVision, err := countWith(func(f *store.MovieFilter) { f.DolbyVision = true })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	externalSubtitle, err := countWith(func(f *store.MovieFilter) { f.ExternalSubtitle = true })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	noChiSubtitle, err := countWith(func(f *store.MovieFilter) { f.NoChiSubtitle = true })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	matchIssue, err := countEach(facetMatchIssues, func(f *store.MovieFilter, v string) { f.MatchIssue = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	matchStatus, err := countEach(facetMatchStatuses, func(f *store.MovieFilter, v string) { f.MatchStatus = v })
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"resolution":        resolution,
		"source":            source,
		"video_codec":       codec,
		"hdr":               hdr,
		"dolby_vision":      dolbyVision,
		"country":           country,
		"subtitle_lang":     subtitleLang,
		"external_subtitle": externalSubtitle,
		"no_chi_subtitle":   noChiSubtitle,
		"watched":           watched,
		"match_issue":       matchIssue,
		"match_status":      matchStatus,
	})
}

// isChineseLangTag reports whether a subtitle language tag belongs to the
// Chinese family: ffprobe reports chi/zho/chs/cht, external subs are tagged
// chi/zh, and Cantonese (yue) counts as Chinese for the UI.
func isChineseLangTag(tag string) bool {
	switch tag {
	case "chi", "zh", "zho", "chs", "cht", "yue":
		return true
	}
	return false
}

// hasChineseLang reports whether a comma-separated subtitle language field
// contains any Chinese-family tag.
func hasChineseLang(langs string) bool {
	if langs == "" {
		return false
	}
	for _, p := range strings.Split(langs, ",") {
		if isChineseLangTag(strings.TrimSpace(p)) {
			return true
		}
	}
	return false
}

// hasLang reports whether a comma-separated subtitle language field contains lang.
func hasLang(langs, lang string) bool {
	if langs == "" {
		return false
	}
	for _, p := range strings.Split(langs, ",") {
		if strings.TrimSpace(p) == lang {
			return true
		}
	}
	return false
}

func (a *API) getMovie(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := a.Store.GetMovie(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "movie not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// attach genres + collection so the detail page can render them without
	// extra round-trips.
	genres, _ := a.Store.ListMovieGenres(r.Context(), id)
	resp := struct {
		*model.Movie
		Genres []model.Genre `json:"genres"`
	}{Movie: m, Genres: genres}
	writeJSON(w, http.StatusOK, resp)
}

// getMovieCast returns the cast + crew for a movie, joined with person info.
func (a *API) getMovieCast(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cast, crew, people, err := a.Store.ListMovieCredits(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"cast":   cast,
		"crew":   crew,
		"people": people,
	})
}

func (a *API) listMovieFiles(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	files, err := a.Store.ListMovieFiles(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// movieFileDetail bundles a movie_file with its audio tracks and subtitles.
type movieFileDetail struct {
	model.MovieFile
	AudioTracks []model.AudioTrack `json:"audio_tracks"`
	Subtitles   []model.Subtitle   `json:"subtitles"`
}

func (a *API) getMovieFile(w http.ResponseWriter, r *http.Request) {
	movieID, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fid, err := parseInt64(r, "fid")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	files, err := a.Store.ListMovieFiles(r.Context(), movieID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var mf *model.MovieFile
	for i := range files {
		if files[i].ID == fid {
			mf = &files[i]
			break
		}
	}
	if mf == nil {
		writeError(w, http.StatusNotFound, "file not found")
		return
	}
	audio, err := a.Store.ListAudioTracks(r.Context(), fid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	subs, err := a.Store.ListSubtitles(r.Context(), fid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, movieFileDetail{MovieFile: *mf, AudioTracks: audio, Subtitles: subs})
}

// ---------- collections ----------

// listCollections returns every system collection that has at least `min_movies`
// local movies, with that movie count attached. Defaults to 2 so single-member
// collections are hidden by default; pass ?min_movies=1 to include them.
// Collections with no local movies are always hidden.
func (a *API) listCollections(w http.ResponseWriter, r *http.Request) {
	minMovies := queryInt(r, "min_movies", 2)
	cols, err := a.Store.ListCollectionsWithCount(r.Context(), minMovies)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cols)
}

// getCollection returns the metadata for one collection (name, poster,
// backdrop, overview) so the detail page can render its header. The member
// movies are fetched separately via /movies?collection={id}.
func (a *API) getCollection(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	col, err := a.Store.GetCollection(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "collection not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, col)
}

// enrichCollection triggers an on-demand TMDB refresh for one collection's
// metadata (name / poster / backdrop / overview), mirroring the per-movie
// enrich endpoint. Useful from the detail page when a collection lacks a
// poster or synopsis.
func (a *API) enrichCollection(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.Enricher == nil || !a.Enricher.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "tmdb enrichment not configured")
		return
	}
	if _, err := a.Store.GetCollection(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "collection not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	refreshed, err := a.Enricher.EnrichCollection(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadGateway, "tmdb: "+err.Error())
		return
	}
	col, err := a.Store.GetCollection(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"refreshed": refreshed,
		"collection": col,
	})
}

// enrichAllCollections triggers a batch TMDB refresh of every collection's
// metadata + member-movie list. It runs asynchronously (refreshing dozens of
// collections can take a while); the response returns immediately with
// "started" and the result is logged. A second call while one is running
// returns 409. Requires TMDB to be configured.
func (a *API) enrichAllCollections(w http.ResponseWriter, r *http.Request) {
	if a.Enricher == nil || !a.Enricher.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "tmdb enrichment not configured")
		return
	}
	a.mu.Lock()
	if a.collectionEnrichRunning {
		a.mu.Unlock()
		writeError(w, http.StatusConflict, "collection refresh already running")
		return
	}
	a.collectionEnrichRunning = true
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.collectionEnrichRunning = false
			a.mu.Unlock()
		}()
		ctx, cancel := contextWithTimeout(30 * time.Minute)
		defer cancel()
		summary, err := a.Enricher.EnrichAllCollections(ctx)
		if err != nil {
			a.Logger.Error("enrich all collections", "err", err)
			return
		}
		a.Logger.Info("enrich all collections done",
			"total", summary.Total, "refreshed", summary.Refreshed,
			"skipped", summary.Skipped, "failed", summary.Failed)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// listCollectionParts returns the cached TMDB member movies of a collection in
// release order, each annotated with the local movie id when the film is in
// the library (local_movie_id != 0) or 0 when missing. The detail page uses
// this to render owned and missing films interleaved chronologically. Returns
// an empty list when the collection has never been refreshed against TMDB.
func (a *API) listCollectionParts(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parts, err := a.Store.ListCollectionParts(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parts)
}

// listCountries returns every production-country code seen across the
// library with its movie count, unfiltered - the filter panel uses this to
// decide which countries get their own chip (vs. folding into "其他").
func (a *API) listCountries(w http.ResponseWriter, r *http.Request) {
	counts, err := a.Store.CountryCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

// listGenres returns all genres ever seen (from TMDB), for the filter panel.
func (a *API) listGenres(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Store.DB.QueryContext(r.Context(),
		`SELECT id, name FROM genres ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []model.Genre{}
	for rows.Next() {
		var g model.Genre
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, g)
	}
	writeJSON(w, http.StatusOK, out)
}

// ensure url.URL / url package isn't dropped by the linter if unused later.
var _ = url.URL{}
