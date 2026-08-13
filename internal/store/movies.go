package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moviegeek/darknight/internal/model"
)

// UpsertMovieSeed is the scanner's seed-row counterpart to UpsertMovie. It
// resolves an existing movie for a release parsed from a directory name (+ an
// optional .nfo) and returns its id, creating one when nothing matches.
//
// It differs from UpsertMovie in two ways that matter for the scan path:
//
//  1. Title matching is widened. The enricher overwrites movies.title with the
//     TMDB original title (e.g. "十三人の刺客") and keeps the parsed English
//     name only in title_en. A re-scan re-parses the English name from the
//     directory, so matching only movies.title misses the enriched row and
//     inserts a duplicate. Seed matching therefore also tries title_en and
//     original_title, in addition to title, against both the parsed title and
//     the .nfo title.
//  2. Enriched rows are protected. When the matched row already carries a
//     tmdb_id, the scanner has nothing authoritative to contribute: only
//     movie_id reattachment is needed. The TMDB-derived display fields
//     (title/original_title/title_en/title_zh/poster/synopsis/...) are left
//     untouched so a re-scan can never clobber enriched metadata. The row is
//     only updated when it has no tmdb_id yet (first-time seed before enrich,
//     or offline mode).
//
// m.ID, m.CreatedAt and m.UpdatedAt are handled as in UpsertMovie.
func (s *Store) UpsertMovieSeed(ctx context.Context, m *model.Movie) error {
	now := time.Now().Unix()
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now

	// Resolve an existing id with the widened title match.
	var existingID int64
	if m.ID != 0 {
		existingID = m.ID
	}
	if existingID == 0 && m.TMDBID != 0 {
		if err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM movies WHERE tmdb_id = ?`, m.TMDBID).Scan(&existingID); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if existingID == 0 && m.IMDBID != "" {
		if err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM movies WHERE imdb_id = ?`, m.IMDBID).Scan(&existingID); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if existingID == 0 && m.Title != "" {
		row := s.DB.QueryRowContext(ctx, `
SELECT id, tmdb_id FROM movies
WHERE year = ?
  AND (title = ? OR title_en = ? OR original_title = ?
       OR title = ? OR title_en = ? OR original_title = ?)`,
			m.Year,
			m.Title, m.Title, m.Title,
			m.OriginalTitle, m.OriginalTitle, m.OriginalTitle)
		var id int64
		var tmdbID sql.NullInt64
		if err := row.Scan(&id, &tmdbID); err == nil {
			existingID = id
			// When the matched row is already enriched, the scanner has nothing
			// authoritative to add: only reattach by id, leave display fields.
			m.ID = id
			if tmdbID.Valid && tmdbID.Int64 != 0 {
				return nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	if existingID != 0 {
		m.ID = existingID
		_, err := s.DB.ExecContext(ctx, `
UPDATE movies SET title=?, sort_title=?, year=?, runtime=?, synopsis=?,
  tmdb_id=COALESCE(NULLIF(tmdb_id,0),?),
  imdb_id=COALESCE(NULLIF(imdb_id,''),?),
  country=COALESCE(NULLIF(?,''), country),
  collection_id=COALESCE(?, collection_id),
  updated_at=?
WHERE id=?`,
			m.Title, m.SortTitle, m.Year, m.Runtime, m.Synopsis,
			nullableInt64(m.TMDBID), m.IMDBID,
			m.Country, nullableInt64(m.CollectionID),
			m.UpdatedAt, m.ID)
		return err
	}

	res, err := s.DB.ExecContext(ctx, `
INSERT INTO movies(title, sort_title, year, release_date, runtime, synopsis,
  poster_path, backdrop_path, tmdb_id, imdb_id, vote_average, vote_count,
  collection_id, country, countries, original_language, original_title, title_en, title_zh,
  created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Title, m.SortTitle, m.Year, m.ReleaseDate, m.Runtime, m.Synopsis,
		m.PosterPath, m.BackdropPath, nullableInt64(m.TMDBID), m.IMDBID,
		m.VoteAverage, m.VoteCount, nullableInt64(m.CollectionID),
		m.Country, m.Countries, m.OriginalLanguage, m.OriginalTitle, m.TitleEn, m.TitleZh,
		m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	return nil
}

// UpsertMovie inserts or updates a movie keyed by tmdb_id (when set) or by
// (title, year). Returns the movie with its id filled in.
//
// Matching by tmdb_id is authoritative when TMDB is available; otherwise we
// fall back to (title, year) so a re-scan reattaches files to the same row.
func (s *Store) UpsertMovie(ctx context.Context, m *model.Movie) error {
	now := time.Now().Unix()
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now

	// resolve existing id.
	//
	// Priority: an explicit m.ID (caller already has the row, e.g. the TMDB
	// enricher re-saving the same movie) > tmdb_id > imdb_id > (title, year).
	// Without the m.ID check, an enrich that changes the title would fail to
	// match the original row and INSERT a duplicate.
	var existingID int64
	if m.ID != 0 {
		existingID = m.ID
	}
	switch {
	case existingID != 0:
		// already resolved above
	case m.TMDBID != 0:
		err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM movies WHERE tmdb_id = ?`, m.TMDBID).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			existingID = 0
		} else if err != nil {
			return err
		}
	case m.IMDBID != "":
		err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM movies WHERE imdb_id = ?`, m.IMDBID).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			existingID = 0
		} else if err != nil {
			return err
		}
	}
	if existingID == 0 && m.Title != "" {
		err := s.DB.QueryRowContext(ctx,
			`SELECT id FROM movies WHERE title = ? AND year = ?`, m.Title, m.Year).Scan(&existingID)
		if errors.Is(err, sql.ErrNoRows) {
			existingID = 0
		} else if err != nil {
			return err
		}
	}

	if existingID != 0 {
		m.ID = existingID
		_, err := s.DB.ExecContext(ctx, `
UPDATE movies SET title=?, sort_title=?, year=?, release_date=?, runtime=?, synopsis=?,
  poster_path=?, backdrop_path=?, tmdb_id=COALESCE(NULLIF(tmdb_id,0),?),
  imdb_id=COALESCE(NULLIF(imdb_id,''),?),
  vote_average=CASE WHEN ? > 0 THEN ? ELSE vote_average END,
  vote_count=CASE WHEN ? > 0 THEN ? ELSE vote_count END,
  collection_id=COALESCE(?, collection_id),
  country=COALESCE(NULLIF(?,''), country),
  countries=COALESCE(NULLIF(?,''), countries),
  original_language=COALESCE(NULLIF(?,''), original_language),
  original_title=COALESCE(NULLIF(?,''), original_title),
  title_en=COALESCE(NULLIF(?,''), title_en),
  title_zh=COALESCE(NULLIF(?,''), title_zh),
  updated_at=?
WHERE id=?`,
			m.Title, m.SortTitle, m.Year, m.ReleaseDate, m.Runtime, m.Synopsis,
			m.PosterPath, m.BackdropPath, nullableInt64(m.TMDBID), m.IMDBID,
			m.VoteAverage, m.VoteAverage, m.VoteCount, m.VoteCount,
			nullableInt64(m.CollectionID), m.Country, m.Countries, m.OriginalLanguage,
			m.OriginalTitle, m.TitleEn, m.TitleZh,
			m.UpdatedAt, m.ID)
		return err
	}

	res, err := s.DB.ExecContext(ctx, `
INSERT INTO movies(title, sort_title, year, release_date, runtime, synopsis,
  poster_path, backdrop_path, tmdb_id, imdb_id, vote_average, vote_count,
  collection_id, country, countries, original_language, original_title, title_en, title_zh,
  created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Title, m.SortTitle, m.Year, m.ReleaseDate, m.Runtime, m.Synopsis,
		m.PosterPath, m.BackdropPath, nullableInt64(m.TMDBID), m.IMDBID,
		m.VoteAverage, m.VoteCount, nullableInt64(m.CollectionID),
		m.Country, m.Countries, m.OriginalLanguage, m.OriginalTitle, m.TitleEn, m.TitleZh,
		m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	return nil
}

// MovieFilter carries the optional dimensions for the movies list endpoint.
// Empty / zero values mean "no filter on this dimension".
type MovieFilter struct {
	Query            string   // title substring
	Genres           []string // any-of (OR)
	YearFrom         int
	YearTo           int
	Resolution       string // matches any movie_file with this resolution
	Source           string
	VideoCodec       string
	HDR              string
	DolbyVision      bool
	Collection       int64    // movies in this collection
	Watched          string   // "" | "watched" | "unwatched" | "watching"
	Countries        []string // any-of (OR); matches any production country on the movie
	SubtitleLang     string   // e.g. "chi", "eng"; any movie_file with this subtitle language
	ExternalSubtitle bool     // only movies with an external subtitle file
	NoChiSubtitle    bool     // only movies with no Chinese subtitle on any file
}

// MovieSort selects the list ordering.
type MovieSort struct {
	Field string // year | title | vote_average | added | size
	Desc  bool
}

// ListMoviesOpts is the full list query input.
type ListMoviesOpts struct {
	Filter MovieFilter
	Sort   MovieSort
	Limit  int
	Offset int
}

// ListMovies returns a page of movies matching the filter. Each movie carries
// the best (highest-resolution) movie_file for badge display via a join.
func (s *Store) ListMovies(ctx context.Context, opts ListMoviesOpts) ([]model.Movie, error) {
	q, args := buildMoviesQuery(opts, false)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Movie{}
	for rows.Next() {
		m, err := scanMovieRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CountMovies returns the total number of movies matching the filter (ignoring
// pagination), for paginated UIs.
func (s *Store) CountMovies(ctx context.Context, f MovieFilter) (int, error) {
	q, args := buildMoviesQuery(ListMoviesOpts{Filter: f}, true)
	row := s.DB.QueryRowContext(ctx, q, args...)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// buildMoviesQuery assembles the SELECT for the movies list. The filter joins
// movie_files when a technical dimension is set, and dedupes with DISTINCT so a
// movie with several files still appears once.
func buildMoviesQuery(opts ListMoviesOpts, count bool) (string, []interface{}) {
	var b strings.Builder
	var args []interface{}

	if count {
		b.WriteString(`SELECT COUNT(DISTINCT m.id) `)
	} else {
		b.WriteString(`SELECT DISTINCT `)
		b.WriteString(movieColumns("m."))
	}

	b.WriteString(`
FROM movies m`)

	joinedFiles := false
	f := opts.Filter
	if f.Resolution != "" || f.Source != "" || f.VideoCodec != "" ||
		f.HDR != "" || f.DolbyVision || f.SubtitleLang != "" || f.ExternalSubtitle {
		joinedFiles = true
		b.WriteString(`
JOIN movie_files mf ON mf.movie_id = m.id`)
	}

	// subtitle language filter needs a "movie has a file whose subtitle_languages
	// contains the requested tag" predicate. We keep it simple with a LIKE on
	// the aggregated field.
	if f.SubtitleLang != "" || f.ExternalSubtitle {
		b.WriteString(`
LEFT JOIN (
  SELECT movie_file_id,
         GROUP_CONCAT(DISTINCT language) AS langs,
         MAX(CASE WHEN is_embedded = 0 THEN 1 ELSE 0 END) AS has_external
  FROM subtitles
  GROUP BY movie_file_id
) sub ON sub.movie_file_id = mf.id`)
	}

	var where []string
	// addParam adds a clause with a single bound placeholder.
	addParam := func(clause string, arg interface{}) {
		where = append(where, clause)
		args = append(args, arg)
	}
	// addClause adds a literal clause with no parameters (e.g. `mf.dolby_vision = 1`).
	addClause := func(clause string) {
		where = append(where, clause)
	}
	// addParams adds a clause with multiple bound placeholders.
	addParams := func(clause string, vals ...interface{}) {
		where = append(where, clause)
		args = append(args, vals...)
	}

	if f.Query != "" {
		// match the search term against any stored title variant so a user can
		// find a film by its English, Chinese, or original title.
		like := "%" + f.Query + "%"
		addParams(`(m.title LIKE ? OR m.original_title LIKE ? OR m.title_en LIKE ? OR m.title_zh LIKE ?)`,
			like, like, like, like)
	}
	if f.YearFrom > 0 {
		addParam(`m.year >= ?`, f.YearFrom)
	}
	if f.YearTo > 0 {
		addParam(`m.year <= ?`, f.YearTo)
	}
	if f.Collection > 0 {
		addParam(`m.collection_id = ?`, f.Collection)
	}
	if f.Resolution != "" {
		addParam(`mf.resolution = ?`, f.Resolution)
	}
	if f.Source != "" {
		addParam(`mf.source = ?`, f.Source)
	}
	if f.VideoCodec != "" {
		addParam(`mf.video_codec = ?`, f.VideoCodec)
	}
	if f.HDR != "" {
		addParam(`mf.hdr = ?`, f.HDR)
	}
	if f.DolbyVision {
		addClause(`mf.dolby_vision = 1`)
	}
	if len(f.Countries) > 0 {
		// countries holds the full comma-separated list; movies.country (the
		// legacy single-value column) is the fallback for rows scanned before
		// that field existed, so both are checked.
		var ors []string
		for _, code := range f.Countries {
			ors = append(ors, `((','||m.countries||',') LIKE '%,'||?||',%' OR (m.countries = '' AND m.country = ?))`)
			args = append(args, code, code)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if f.SubtitleLang != "" {
		addParam(`sub.langs LIKE '%' || ? || '%'`, f.SubtitleLang)
	}
	if f.ExternalSubtitle {
		addClause(`sub.has_external = 1`)
	}
	if f.NoChiSubtitle {
		addClause(`NOT EXISTS (
  SELECT 1 FROM movie_files nc
  WHERE nc.movie_id = m.id AND (',' || nc.subtitle_languages || ',') LIKE '%,chi,%'
)`)
	}
	switch f.Watched {
	case "watched":
		addClause(`EXISTS (SELECT 1 FROM watch_status ws WHERE ws.movie_id=m.id AND ws.status='watched')`)
	case "unwatched":
		addClause(`NOT EXISTS (SELECT 1 FROM watch_status ws WHERE ws.movie_id=m.id AND ws.status='watched')`)
	case "watching":
		addClause(`EXISTS (SELECT 1 FROM watch_status ws WHERE ws.movie_id=m.id AND ws.status='watching')`)
	}

	if len(where) > 0 {
		b.WriteString("\nWHERE ")
		b.WriteString(strings.Join(where, " AND "))
	}

	_ = joinedFiles

	if !count {
		orderBy := "m.sort_title ASC"
		dir := "ASC"
		if opts.Sort.Desc {
			dir = "DESC"
		}
		switch opts.Sort.Field {
		case "year":
			orderBy = "m.year " + dir + ", m.sort_title ASC"
		case "title":
			orderBy = "m.sort_title " + dir
		case "vote_average":
			orderBy = "m.vote_average " + dir + ", m.sort_title ASC"
		case "added":
			orderBy = "m.created_at " + dir
		default:
			orderBy = "m.sort_title ASC"
		}
		b.WriteString("\nORDER BY " + orderBy)

		limit := opts.Limit
		if limit == 0 {
			// 0 means "no limit" - used by the grid which scrolls infinitely.
		} else if limit < 0 || limit > 200 {
			limit = 50
			b.WriteString(fmt.Sprintf("\nLIMIT %d OFFSET %d", limit, opts.Offset))
		} else {
			b.WriteString(fmt.Sprintf("\nLIMIT %d OFFSET %d", limit, opts.Offset))
		}
	}

	return b.String(), args
}

// movieColumns is the canonical column list for scanning a Movie row, with an
// optional table alias prefix.
func movieColumns(prefix string) string {
	return strings.Join([]string{
		prefix + "id", prefix + "title", prefix + "sort_title", prefix + "year",
		prefix + "release_date", prefix + "runtime", prefix + "synopsis",
		prefix + "poster_path", prefix + "backdrop_path", prefix + "tmdb_id",
		prefix + "imdb_id", prefix + "vote_average", prefix + "vote_count",
		prefix + "collection_id", prefix + "country", prefix + "countries", prefix + "original_language",
		prefix + "original_title", prefix + "title_en", prefix + "title_zh",
		prefix + "created_at", prefix + "updated_at",
	}, ", ")
}

// CountryCount is one row of CountryCounts: an ISO country code and how many
// movies list it among their production countries.
type CountryCount struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

// CountryCounts returns every production-country code found across all
// movies (splitting the comma-separated `countries` list, falling back to
// the legacy single-value `country` column), with how many movies carry
// each one. Counts are over the whole library, ignoring any active filter -
// callers needing a filtered count should use CountMovies with Countries set
// to a single code instead.
func (s *Store) CountryCounts(ctx context.Context) ([]CountryCount, error) {
	rows, err := s.DB.QueryContext(ctx, `
WITH RECURSIVE split(movie_id, code, rest) AS (
  SELECT id, NULL,
    CASE WHEN countries != '' THEN countries || ',' ELSE country || ',' END
  FROM movies
  WHERE countries != '' OR country != ''
  UNION ALL
  SELECT movie_id,
    substr(rest, 1, instr(rest, ',') - 1),
    substr(rest, instr(rest, ',') + 1)
  FROM split
  WHERE rest != ''
)
SELECT code, COUNT(DISTINCT movie_id) AS n
FROM split
WHERE code IS NOT NULL AND code != ''
GROUP BY code
ORDER BY n DESC, code ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CountryCount{}
	for rows.Next() {
		var c CountryCount
		if err := rows.Scan(&c.Code, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetMovie fetches a single movie by id.
func (s *Store) GetMovie(ctx context.Context, id int64) (*model.Movie, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+movieColumns("m.")+` FROM movies m WHERE m.id = ?`, id)
	m, err := scanMovieRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// scanner is the minimal interface both *sql.Row and *sql.Rows satisfy for
// scanning a single row.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanMovieRow scans a movie row using movieColumns(), tolerating NULL in the
// nullable columns (tmdb_id, collection_id).
func scanMovieRow(row scanner) (*model.Movie, error) {
	var m model.Movie
	var tmdbID sql.NullInt64
	var collectionID sql.NullInt64
	if err := row.Scan(
		&m.ID, &m.Title, &m.SortTitle, &m.Year, &m.ReleaseDate, &m.Runtime,
		&m.Synopsis, &m.PosterPath, &m.BackdropPath, &tmdbID, &m.IMDBID,
		&m.VoteAverage, &m.VoteCount, &collectionID, &m.Country, &m.Countries, &m.OriginalLanguage,
		&m.OriginalTitle, &m.TitleEn, &m.TitleZh,
		&m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return nil, err
	}
	m.TMDBID = tmdbID.Int64
	m.CollectionID = collectionID.Int64
	return &m, nil
}

// ListMovieFiles returns every physical release for a movie, newest scan first.
func (s *Store) ListMovieFiles(ctx context.Context, movieID int64) ([]model.MovieFile, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT mf.id, mf.movie_id, mf.library_id, l.name,
  mf.dir_path, mf.file_name, mf.is_disc, mf.file_size, mf.file_modified,
  mf.release_group, mf.edition, mf.source, mf.resolution, mf.video_codec, mf.audio_codec, mf.audio_channels,
  mf.hdr, mf.dolby_vision, mf.bit_depth, mf.audio_count, mf.language, mf.is_collection, mf.raw_name,
  mf.duration_sec, mf.video_bitrate, mf.frame_rate, mf.width, mf.height, mf.container,
  mf.ffprobe_json, mf.ffprobe_version, mf.ffprobe_at,
  mf.nfo_path, mf.subtitle_languages, mf.has_external_subtitle, mf.scanned_at, mf.created_at, mf.updated_at
FROM movie_files mf
JOIN libraries l ON l.id = mf.library_id
WHERE mf.movie_id = ?
ORDER BY mf.resolution DESC, mf.file_size DESC`, movieID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MovieFile{}
	for rows.Next() {
		mf, err := scanMovieFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mf)
	}
	return out, rows.Err()
}

func scanMovieFile(rows interface {
	Scan(dest ...interface{}) error
}) (model.MovieFile, error) {
	var mf model.MovieFile
	var isDisc, dv, isColl, hasExtSub int64
	err := rows.Scan(
		&mf.ID, &mf.MovieID, &mf.LibraryID, &mf.LibraryName, &mf.DirPath, &mf.FileName, &isDisc,
		&mf.FileSize, &mf.FileModified, &mf.ReleaseGroup, &mf.Edition,
		&mf.Source, &mf.Resolution, &mf.VideoCodec, &mf.AudioCodec,
		&mf.AudioChannels, &mf.HDR, &dv, &mf.BitDepth, &mf.AudioCount,
		&mf.Language, &isColl, &mf.RawName, &mf.DurationSec, &mf.VideoBitrate,
		&mf.FrameRate, &mf.Width, &mf.Height, &mf.Container,
		&mf.FFProbeJSON, &mf.FFProbeVersion, &mf.FFProbeAt,
		&mf.NFOPath,
		&mf.SubtitleLanguages, &hasExtSub, &mf.ScannedAt, &mf.CreatedAt, &mf.UpdatedAt)
	mf.IsDisc = isDisc != 0
	mf.DolbyVision = dv != 0
	mf.IsCollection = isColl != 0
	mf.HasExternalSubtitle = hasExtSub != 0
	return mf, err
}

// nullableInt64 returns a sql.NullInt64-friendly value: 0 -> NULL for
// UNIQUE-but-optional columns (tmdb_id, collection_id).
func nullableInt64(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
