// Package tmdb is a minimal client for The Movie Database (TMDB) v3 REST API.
//
// It is intentionally narrow: only the endpoints we need for movie library
// enrichment are modelled - search by title, find by imdb id, movie details
// with credits/external ids, and collection details. Responses are cached by
// the store layer (tmdb_cache table) so the app still works offline after the
// first fetch.
//
// API docs: https://developer.themoviedb.org/reference
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// BaseURL is the v3 API root.
	BaseURL = "https://api.themoviedb.org/3"
	// ImageBase is the CDN root for posters / backdrops / profiles.
	ImageBase = "https://image.tmdb.org/t/p"
)

// ErrNotFound is returned when TMDB has no match for a lookup.
var ErrNotFound = errors.New("tmdb: not found")

// ErrNoAPIKey is returned when the client has no API key configured. Callers
// should treat this as "offline mode" and skip enrichment.
var ErrNoAPIKey = errors.New("tmdb: no api key configured")

// Client is a TMDB v3 HTTP client. The zero value is not usable - construct
// one with New.
type Client struct {
	APIKey   string
	Language string // primary locale, e.g. "en-US"; "" falls back to TMDB default
	// LanguageAlt is the secondary locale used to fetch the localized title
	// for bilingual display (e.g. "zh-CN" for the Chinese title).
	LanguageAlt string
	BaseURL     string // override for testing; defaults to BaseURL
	HTTP        *http.Client
	// Logger, when set, receives debug traces of the resolution chain and
	// each API request. Nil = silent (the default for tests).
	Logger *slog.Logger
}

// New returns a ready client. An empty apiKey yields a client whose methods
// all return ErrNoAPIKey - safe to call, no-op enrichment. language is the
// primary fetch locale; languageAlt is the secondary locale for the bilingual
// title fetch (may be "").
func New(apiKey, language, languageAlt string) *Client {
	c := &Client{
		APIKey:      apiKey,
		Language:    language,
		LanguageAlt: languageAlt,
		BaseURL:     BaseURL,
		HTTP: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	return c
}

// Enabled reports whether TMDB enrichment is available.
func (c *Client) Enabled() bool { return c != nil && c.APIKey != "" }

// debug logs at debug level when a logger is wired, otherwise it is a no-op.
func (c *Client) debug(msg string, args ...any) {
	if c.Logger != nil {
		c.Logger.Debug(msg, args...)
	}
}

// MovieDetails is the subset of /movie/{id} we persist. Append-To-Response
// fills Credits and ExternalIDs in one request.
type MovieDetails struct {
	ID                  int64       `json:"id"`
	IMDBID              string      `json:"imdb_id"`
	Title               string      `json:"title"`
	OriginalTitle       string      `json:"original_title"`
	ReleaseDate         string      `json:"release_date"` // YYYY-MM-DD
	Runtime             int         `json:"runtime"`
	Overview            string      `json:"overview"`
	PosterPath          string      `json:"poster_path"`
	BackdropPath        string      `json:"backdrop_path"`
	VoteAverage         float64     `json:"vote_average"`
	VoteCount           int         `json:"vote_count"`
	OriginalLanguage    string      `json:"original_language"`
	OriginCountry       []string    `json:"origin_country"`
	ProductionCountries []struct {
		ISO3166_1 string `json:"iso_3166_1"`
		Name      string `json:"name"`
	} `json:"production_countries"`
	Genres              []Genre       `json:"genres"`
	BelongsToCollection *Collection   `json:"belongs_to_collection"`
	Credits             Credits       `json:"credits"`
	ExternalIDs         ExternalIDs   `json:"external_ids"`
}

// Genre is a TMDB genre.
type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Collection is a TMDB movie collection (series / anthology). The Parts field
// holds the member movies returned by /collection/{id} and is used to detect
// which films of a collection are missing from the local library.
type Collection struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	PosterPath   string            `json:"poster_path"`
	BackdropPath string            `json:"backdrop_path"`
	Overview     string            `json:"overview"`
	Parts        []CollectionPart  `json:"parts"`
}

// CollectionPart is one member movie of a collection, as returned in the
// parts array of /collection/{id}. The array is in release order.
type CollectionPart struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	ReleaseDate   string  `json:"release_date"` // YYYY-MM-DD
	PosterPath    string  `json:"poster_path"`
	Overview      string  `json:"overview"`
	VoteAverage   float64 `json:"vote_average"`
}

// Credits bundles cast and crew for a movie.
type Credits struct {
	Cast []CastMember `json:"cast"`
	Crew []CrewMember `json:"crew"`
}

// CastMember is one actor in a movie.
type CastMember struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Character  string `json:"character"`
	Order      int    `json:"order"`
	ProfilePath string `json:"profile_path"`
}

// CrewMember is one crew role. We keep Job to filter Directors / Writers.
type CrewMember struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Job        string `json:"job"`
	Department string `json:"department"`
	ProfilePath string `json:"profile_path"`
}

// ExternalIDs carries cross-service ids for a movie.
type ExternalIDs struct {
	IMDBID      string `json:"imdb_id"`
	FacebookID  string `json:"facebook_id"`
	InstagramID string `json:"instagram_id"`
	TwitterID   string `json:"twitter_id"`
}

// SearchResult is one row of /search/movie.
type SearchResult struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	OriginalTitle string `json:"original_title"`
	ReleaseDate  string  `json:"release_date"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
	Popularity   float64 `json:"popularity"`
}

// Year returns the release year parsed from ReleaseDate, or 0.
func (sr *SearchResult) Year() int {
	if len(sr.ReleaseDate) >= 4 {
		if y, err := strconv.Atoi(sr.ReleaseDate[:4]); err == nil {
			return y
		}
	}
	return 0
}

// FindResult is the response from /find/{external_id}.
type FindResult struct {
	MovieResults []SearchResult `json:"movie_results"`
}

// GetMovie fetches /movie/{id} with credits + external_ids appended.
func (c *Client) GetMovie(ctx context.Context, tmdbID int64) (*MovieDetails, error) {
	if !c.Enabled() {
		return nil, ErrNoAPIKey
	}
	path := fmt.Sprintf("/movie/%d?append_to_response=credits,external_ids", tmdbID)
	var m MovieDetails
	if err := c.get(ctx, path, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// GetMovieTitle fetches only the localized title of a movie in lang (a plain
// /movie/{id}?language=lang, no append_to_response). Used for the secondary
// language title in bilingual display. Empty lang falls back to TMDB default.
func (c *Client) GetMovieTitle(ctx context.Context, tmdbID int64, lang string) (string, error) {
	if !c.Enabled() {
		return "", ErrNoAPIKey
	}
	path := fmt.Sprintf("/movie/%d", tmdbID)
	var m struct {
		Title string `json:"title"`
	}
	if err := c.getLang(ctx, path, lang, &m); err != nil {
		return "", err
	}
	return m.Title, nil
}

// FindByIMDB resolves an IMDB id (e.g. "tt0111161") to a TMDB movie id.
func (c *Client) FindByIMDB(ctx context.Context, imdbID string) (*SearchResult, error) {
	if !c.Enabled() {
		return nil, ErrNoAPIKey
	}
	if strings.HasPrefix(imdbID, "tt") {
		imdbID = imdbID[2:]
	}
	var fr FindResult
	if err := c.get(ctx, "/find/tt"+imdbID+"?external_source=imdb_id", &fr); err != nil {
		return nil, err
	}
	if len(fr.MovieResults) == 0 {
		return nil, ErrNotFound
	}
	return &fr.MovieResults[0], nil
}

// SearchMovie does a /search/movie query. The year, when > 0, biases matches.
func (c *Client) SearchMovie(ctx context.Context, query string, year int) (*SearchResult, error) {
	if !c.Enabled() {
		return nil, ErrNoAPIKey
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, "/search/movie?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	if len(resp.Results) == 0 {
		return nil, ErrNotFound
	}
	return &resp.Results[0], nil
}

// SearchMovieAll is the candidate-oriented search used by the matcher: it
// returns the full result list (capped at limit, TMDB's page size) instead of
// just the first row, so the caller can score every candidate. The year is
// never sent as a hard filter here - scoring compares years instead, because a
// wrongly parsed year must not zero out the results.
func (c *Client) SearchMovieAll(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if !c.Enabled() {
		return nil, ErrNoAPIKey
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("include_adult", "false")
	q.Set("page", "1")
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := c.get(ctx, "/search/movie?"+q.Encode(), &resp); err != nil {
		return nil, err
	}
	if len(resp.Results) == 0 {
		return nil, ErrNotFound
	}
	if limit > 0 && len(resp.Results) > limit {
		resp.Results = resp.Results[:limit]
	}
	return resp.Results, nil
}

// GetCollection fetches /collection/{id}.
func (c *Client) GetCollection(ctx context.Context, collectionID int64) (*Collection, error) {
	if !c.Enabled() {
		return nil, ErrNoAPIKey
	}
	var col Collection
	if err := c.get(ctx, fmt.Sprintf("/collection/%d", collectionID), &col); err != nil {
		return nil, err
	}
	return &col, nil
}

// FindMovie resolves a movie by the priority chain: tmdbID > imdbID > (title, year).
// Returns the full details (with credits) of the matched movie.
func (c *Client) FindMovie(ctx context.Context, tmdbID int64, imdbID, title string, year int) (*MovieDetails, error) {
	if !c.Enabled() {
		return nil, ErrNoAPIKey
	}
	if tmdbID > 0 {
		c.debug("tmdb: resolve by tmdb_id", "tmdb_id", tmdbID)
		return c.GetMovie(ctx, tmdbID)
	}
	if imdbID != "" {
		c.debug("tmdb: resolve by imdb_id", "imdb_id", imdbID)
		sr, err := c.FindByIMDB(ctx, imdbID)
		if err == nil {
			return c.GetMovie(ctx, sr.ID)
		}
		if errors.Is(err, ErrNotFound) {
			// fall through to title search
			c.debug("tmdb: imdb_id not found, falling back to title search", "imdb_id", imdbID)
		} else {
			return nil, err
		}
	}
	if title != "" {
		c.debug("tmdb: resolve by title", "title", title, "year", year)
		sr, err := c.SearchMovie(ctx, title, year)
		if err == nil {
			return c.GetMovie(ctx, sr.ID)
		}
		return nil, err
	}
	return nil, ErrNotFound
}

// get performs a GET against the API in the client's primary language,
// decoding v. Status 404 -> ErrNotFound.
func (c *Client) get(ctx context.Context, path string, v interface{}) error {
	return c.getLang(ctx, path, c.Language, v)
}

// getLang performs a GET against the API in the given language, decoding v.
// Status 404 -> ErrNotFound. Used for the secondary-language title fetch.
func (c *Client) getLang(ctx context.Context, path, lang string, v interface{}) error {
	u := c.BaseURL + path
	// append api_key + language. If path already has a query string, use &.
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	u += sep + "api_key=" + url.QueryEscape(c.APIKey)
	if lang != "" {
		u += "&language=" + url.QueryEscape(lang)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	c.debug("tmdb: response", "path", path, "status", resp.StatusCode, "bytes", len(body))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode >= 400:
		// surface TMDB error message for debugging
		var e struct {
			StatusMessage string `json:"status_message"`
		}
		_ = json.Unmarshal(body, &e)
		if e.StatusMessage != "" {
			return fmt.Errorf("tmdb: %d %s", resp.StatusCode, e.StatusMessage)
		}
		return fmt.Errorf("tmdb: %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}

// ImageURL builds a full TMDB CDN image URL from a poster/backdrop/profile path
// like "/abc.jpg" and a size bucket ("w500", "w1280", "original", ...). Empty
// path returns "".
func ImageURL(path, size string) string {
	if path == "" {
		return ""
	}
	if size == "" {
		size = "original"
	}
	return ImageBase + "/" + size + path
}
