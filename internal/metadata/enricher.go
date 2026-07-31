// Package metadata enriches the local movie index with TMDB data. It wraps the
// raw tmdb.Client with a SQLite-backed response cache so the app keeps working
// offline after the first successful fetch.
//
// EnrichMovie resolves a movie by the priority chain (tmdb_id > imdb_id >
// title+year), persists the resulting fields onto the movie row, and writes
// genres, credits, and collection rows. It is idempotent: re-running it on the
// same movie refreshes the cached TMDB payload only after the TTL expires.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/tmdb"
)

// Enricher coordinates TMDB lookups against the store cache.
type Enricher struct {
	TMDB   *tmdb.Client
	Store  *store.Store
	Logger *slog.Logger
}

// New returns an Enricher. If the tmdb.Client is disabled (no API key), calls
// to EnrichMovie are no-ops.
func New(c *tmdb.Client, st *store.Store, log *slog.Logger) *Enricher {
	if log == nil {
		log = slog.Default()
	}
	// share our logger with the client so its resolution-chain trace flows
	// through the same handler when debug logging is on.
	if c != nil && c.Logger == nil {
		c.Logger = log
	}
	return &Enricher{TMDB: c, Store: st, Logger: log}
}

// Enabled reports whether TMDB enrichment is available.
func (e *Enricher) Enabled() bool { return e != nil && e.TMDB != nil && e.TMDB.Enabled() }

// EnrichMovie fetches TMDB data for the given movie and persists it.
//
// Resolution order, applied per field:
//   - tmdb_id is authoritative when set; imdb_id is filled from the TMDB
//     external_ids if the movie's was empty.
//   - title / overview / poster_path / backdrop_path / runtime / vote_* are
//     overwritten when TMDB has a non-empty value.
//
// Returns (refreshed, error). refreshed=false when offline or no match found.
func (e *Enricher) EnrichMovie(ctx context.Context, m *model.Movie) (bool, error) {
	return e.enrich(ctx, m, false)
}

// EnrichMovieForce is EnrichMovie but bypasses the local TMDB cache - used by
// the manual "refresh metadata" UI action.
func (e *Enricher) EnrichMovieForce(ctx context.Context, m *model.Movie) (bool, error) {
	return e.enrich(ctx, m, true)
}

func (e *Enricher) enrich(ctx context.Context, m *model.Movie, force bool) (bool, error) {
	if !e.Enabled() {
		return false, nil
	}
	details, err := e.fetchMovie(ctx, m, force)
	if err != nil {
		if errors.Is(err, tmdb.ErrNotFound) {
			e.Logger.Debug("tmdb no match", "title", m.Title, "year", m.Year)
			return false, nil
		}
		return false, err
	}

	// Apply fields, keeping non-empty local values only when TMDB is empty.
	//
	// Primary display title is the original title (falling back to the
	// localized primary title). The primary TMDB fetch is English
	// (TMDB_LANGUAGE defaults to en-US), so details.Title doubles as the
	// English localized title.
	if details.OriginalTitle != "" {
		m.OriginalTitle = details.OriginalTitle
		m.Title = details.OriginalTitle
	} else if details.Title != "" {
		m.Title = details.Title
	}
	if details.Title != "" {
		m.TitleEn = details.Title
	}
	if details.ReleaseDate != "" {
		m.ReleaseDate = details.ReleaseDate
		if len(details.ReleaseDate) >= 4 {
			// release_date YYYY-MM-DD -> year
			if y := atoiSafe(details.ReleaseDate[:4]); y > 0 {
				m.Year = y
			}
		}
	}
	if details.Runtime > 0 {
		m.Runtime = details.Runtime
	}
	if details.Overview != "" {
		m.Synopsis = details.Overview
	}
	if details.PosterPath != "" {
		m.PosterPath = details.PosterPath
	}
	if details.BackdropPath != "" {
		m.BackdropPath = details.BackdropPath
	}
	if details.VoteAverage > 0 {
		m.VoteAverage = details.VoteAverage
	}
	if details.VoteCount > 0 {
		m.VoteCount = details.VoteCount
	}
	if details.IMDBID != "" {
		m.IMDBID = details.IMDBID
	}
	m.TMDBID = details.ID
	if details.OriginalLanguage != "" {
		m.OriginalLanguage = details.OriginalLanguage
	}
	if len(details.ProductionCountries) > 0 && details.ProductionCountries[0].ISO3166_1 != "" {
		codes := make([]string, 0, len(details.ProductionCountries))
		for _, c := range details.ProductionCountries {
			if c.ISO3166_1 != "" {
				codes = append(codes, c.ISO3166_1)
			}
		}
		m.Country = codes[0]
		m.Countries = strings.Join(codes, ",")
	} else if len(details.OriginCountry) > 0 {
		m.Country = details.OriginCountry[0]
		m.Countries = strings.Join(details.OriginCountry, ",")
	}

	// Secondary (alt-language) title for bilingual display. When the film's
	// original language is Chinese the original title already is the Chinese
	// title, so no extra request is needed; otherwise fetch the localized
	// title in the alt language (cached separately so refreshes are cheap).
	if isChineseLang(m.OriginalLanguage) {
		m.TitleZh = m.OriginalTitle
	} else if e.TMDB.LanguageAlt != "" {
		if zh, err := e.fetchTitle(ctx, details.ID, e.TMDB.LanguageAlt, force); err == nil {
			m.TitleZh = zh
		} else {
			e.Logger.Warn("tmdb secondary title", "tmdb_id", details.ID, "lang", e.TMDB.LanguageAlt, "err", err)
		}
	}

	// upsert collection if present
	if details.BelongsToCollection != nil && details.BelongsToCollection.ID != 0 {
		colID, err := e.Store.UpsertCollection(ctx, details.BelongsToCollection)
		if err != nil {
			e.Logger.Warn("upsert collection", "err", err)
		} else {
			m.CollectionID = colID
		}
	}

	// re-save movie with new fields
	if err := e.Store.UpsertMovie(ctx, m); err != nil {
		return false, fmt.Errorf("upsert enriched movie: %w", err)
	}

	// genres
	if len(details.Genres) > 0 {
		genreMap, err := e.Store.UpsertGenres(ctx, details.Genres)
		if err != nil {
			e.Logger.Warn("upsert genres", "err", err)
		} else {
			ids := make([]int64, 0, len(details.Genres))
			for _, g := range details.Genres {
				if rid, ok := genreMap[g.ID]; ok {
					ids = append(ids, rid)
				}
			}
			if err := e.Store.ReplaceMovieGenres(ctx, m.ID, ids); err != nil {
				e.Logger.Warn("replace genres", "err", err)
			}
		}
	}

	// credits
	if len(details.Credits.Cast) > 0 || len(details.Credits.Crew) > 0 {
		if err := e.Store.ReplaceMovieCredits(ctx, m.ID, details.Credits, 30); err != nil {
			e.Logger.Warn("replace credits", "err", err)
		}
	}

	e.Logger.Debug("tmdb applied",
		"title", m.Title, "tmdb_id", m.TMDBID, "imdb_id", m.IMDBID,
		"genres", len(details.Genres), "cast", len(details.Credits.Cast),
		"crew", len(details.Credits.Crew), "collection", m.CollectionID)
	e.Logger.Info("tmdb enriched", "title", m.Title, "year", m.Year, "tmdb_id", m.TMDBID)
	return true, nil
}

// EnrichCollection refreshes one system collection's TMDB metadata (name,
// poster, backdrop, overview) and its member-movie list by its row id. It is
// the manual "刷新元数据" action on the collection detail page: it always hits
// TMDB (no local cache) and writes the result back through UpsertCollection
// plus ReplaceCollectionParts. Requires the collection to carry a tmdb_id;
// rows without one cannot be enriched.
//
// Returns (refreshed, error); refreshed is false when the collection is
// unknown, has no tmdb_id, or TMDB has no record for it.
func (e *Enricher) EnrichCollection(ctx context.Context, collectionID int64) (bool, error) {
	if !e.Enabled() {
		return false, nil
	}
	col, err := e.Store.GetCollection(ctx, collectionID)
	if err != nil {
		return false, err
	}
	if col.TMDBID == 0 {
		e.Logger.Debug("enrich collection: no tmdb_id", "collection_id", collectionID, "name", col.Name)
		return false, nil
	}
	e.Logger.Debug("enrich collection", "collection_id", collectionID, "tmdb_id", col.TMDBID, "name", col.Name)
	tc, err := e.TMDB.GetCollection(ctx, col.TMDBID)
	if err != nil {
		if errors.Is(err, tmdb.ErrNotFound) {
			e.Logger.Debug("tmdb collection not found", "tmdb_id", col.TMDBID)
			return false, nil
		}
		return false, err
	}
	if _, err := e.Store.UpsertCollection(ctx, tc); err != nil {
		return false, fmt.Errorf("upsert enriched collection: %w", err)
	}
	// cache the member movies so the detail page can show missing films
	// offline. Only overwrite when TMDB returned parts: an empty list usually
	// means TMDB simply has none, but we avoid wiping a previously good cache
	// on a transient empty response.
	if len(tc.Parts) > 0 {
		if err := e.Store.ReplaceCollectionParts(ctx, collectionID, tc.Parts); err != nil {
			e.Logger.Warn("replace collection parts", "collection_id", collectionID, "err", err)
		}
	}
	e.Logger.Info("tmdb collection enriched", "name", tc.Name, "tmdb_id", tc.ID, "parts", len(tc.Parts))
	return true, nil
}

// CollectionEnrichSummary is the outcome of an EnrichAllCollections run.
type CollectionEnrichSummary struct {
	Total     int `json:"total"`     // collections inspected
	Refreshed int `json:"refreshed"` // successfully updated from TMDB
	Skipped   int `json:"skipped"`   // no tmdb_id, or TMDB had no record
	Failed    int `json:"failed"`    // errored (logged, not fatal)
}

// EnrichAllCollections refreshes every system collection's TMDB metadata and
// member-movie list. It is the batch "刷新所有合集" action: it iterates every
// collection row, skips those without a tmdb_id, and calls EnrichCollection on
// each. A per-collection error is counted as failed and logged but does not
// abort the run, so one bad collection doesn't block the rest. Requires TMDB
// to be enabled.
func (e *Enricher) EnrichAllCollections(ctx context.Context) (CollectionEnrichSummary, error) {
	var summary CollectionEnrichSummary
	if !e.Enabled() {
		return summary, nil
	}
	cols, err := e.Store.ListCollections(ctx)
	if err != nil {
		return summary, fmt.Errorf("list collections: %w", err)
	}
	summary.Total = len(cols)
	for i := range cols {
		// stop early if the surrounding context was cancelled
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		col := &cols[i]
		ok, err := e.EnrichCollection(ctx, col.ID)
		switch {
		case err != nil:
			summary.Failed++
			e.Logger.Warn("enrich all: collection failed", "id", col.ID, "name", col.Name, "err", err)
		case ok:
			summary.Refreshed++
		default:
			summary.Skipped++
		}
	}
	e.Logger.Info("enrich all collections done",
		"total", summary.Total, "refreshed", summary.Refreshed,
		"skipped", summary.Skipped, "failed", summary.Failed)
	return summary, nil
}

// fetchMovie returns the cached or live TMDB details for m. When force is true
// the local cache is skipped (but the result is still written back to it).
func (e *Enricher) fetchMovie(ctx context.Context, m *model.Movie, force bool) (*tmdb.MovieDetails, error) {
	e.Logger.Debug("tmdb resolve",
		"title", m.Title, "year", m.Year, "tmdb_id", m.TMDBID, "imdb_id", m.IMDBID, "force", force)
	cacheKey := fmt.Sprintf("/movie/chain?tmdb_id=%d&imdb_id=%s&title=%s&year=%d&lang=%s",
		m.TMDBID, m.IMDBID, m.Title, m.Year, e.TMDB.Language)
	if !force {
		if body, ok, err := e.Store.GetTMDBCache(ctx, cacheKey); err == nil && ok {
			var d tmdb.MovieDetails
			if err := json.Unmarshal([]byte(body), &d); err == nil {
				e.Logger.Debug("tmdb cache hit", "title", m.Title, "tmdb_id", d.ID)
				return &d, nil
			}
		}
	}

	e.Logger.Debug("tmdb cache miss, fetching from API", "title", m.Title)
	d, err := e.TMDB.FindMovie(ctx, m.TMDBID, m.IMDBID, m.Title, m.Year)
	if err != nil {
		return nil, err
	}
	e.Logger.Debug("tmdb fetched", "title", m.Title, "tmdb_id", d.ID)
	if b, err := json.Marshal(d); err == nil {
		if err := e.Store.SetTMDBCache(ctx, cacheKey, string(b)); err != nil {
			e.Logger.Warn("set tmdb cache", "err", err)
		}
	}
	return d, nil
}

// fetchTitle returns a localized title for a movie, using a small separate
// cache so repeated enriches don't re-hit the API. The cached value is the
// raw title string.
func (e *Enricher) fetchTitle(ctx context.Context, tmdbID int64, lang string, force bool) (string, error) {
	cacheKey := fmt.Sprintf("/movie/%d/title?lang=%s", tmdbID, lang)
	if !force {
		if body, ok, err := e.Store.GetTMDBCache(ctx, cacheKey); err == nil && ok {
			return body, nil
		}
	}
	title, err := e.TMDB.GetMovieTitle(ctx, tmdbID, lang)
	if err != nil {
		return "", err
	}
	if err := e.Store.SetTMDBCache(ctx, cacheKey, title); err != nil {
		e.Logger.Warn("set tmdb title cache", "err", err)
	}
	return title, nil
}

// isChineseLang reports whether a TMDB original_language code denotes a
// Chinese variant. Covers the 2-letter "zh", its region subtags, plus the
// non-standard "cn" and ISO 639-3 "yue" codes TMDB sometimes uses.
func isChineseLang(lang string) bool {
	l := strings.ToLower(lang)
	return l == "zh" || strings.HasPrefix(l, "zh-") || strings.HasPrefix(l, "zh_") ||
		l == "cn" || l == "yue"
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
