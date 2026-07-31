package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/tmdb"
)

// tmdbCacheTTL is how long a cached TMDB response is considered fresh.
const tmdbCacheTTL = 30 * 24 * time.Hour // 30 days

// GetTMDBCache returns the cached body for endpoint, or "" if absent / stale.
func (s *Store) GetTMDBCache(ctx context.Context, endpoint string) (string, bool, error) {
	var body string
	var fetched int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT body_json, fetched_at FROM tmdb_cache WHERE endpoint = ?`, endpoint).
		Scan(&body, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if time.Now().Unix()-fetched > int64(tmdbCacheTTL.Seconds()) {
		return "", false, nil
	}
	return body, true, nil
}

// SetTMDBCache stores (or replaces) a cached response.
func (s *Store) SetTMDBCache(ctx context.Context, endpoint, body string) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx, `
INSERT INTO tmdb_cache(endpoint, body_json, fetched_at) VALUES (?, ?, ?)
ON CONFLICT(endpoint) DO UPDATE SET body_json=excluded.body_json, fetched_at=excluded.fetched_at`,
		endpoint, body, now)
	return err
}

// UpsertGenres inserts any new genres from TMDB and returns their row ids,
// keyed by TMDB genre id. Existing genres are matched by name.
func (s *Store) UpsertGenres(ctx context.Context, genres []tmdb.Genre) (map[int64]int64, error) {
	out := make(map[int64]int64, len(genres))
	for _, g := range genres {
		var id int64
		// match by tmdb_id first, then by name
		err := s.DB.QueryRowContext(ctx, `SELECT id FROM genres WHERE name = ?`, g.Name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			res, err := s.DB.ExecContext(ctx,
				`INSERT INTO genres(name) VALUES (?)`, g.Name)
			if err != nil {
				return nil, err
			}
			id, _ = res.LastInsertId()
		} else if err != nil {
			return nil, err
		}
		out[g.ID] = id
	}
	return out, nil
}

// ReplaceMovieGenres swaps the genre links for a movie.
func (s *Store) ReplaceMovieGenres(ctx context.Context, movieID int64, genreIDs []int64) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM movie_genres WHERE movie_id = ?`, movieID); err != nil {
		return err
	}
	for _, gid := range genreIDs {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT OR IGNORE INTO movie_genres(movie_id, genre_id) VALUES (?, ?)`,
			movieID, gid); err != nil {
			return err
		}
	}
	return nil
}

// ListMovieGenres returns the genre names for a movie.
func (s *Store) ListMovieGenres(ctx context.Context, movieID int64) ([]model.Genre, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT g.id, g.name FROM genres g
JOIN movie_genres mg ON mg.genre_id = g.id
WHERE mg.movie_id = ? ORDER BY g.name`, movieID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Genre{}
	for rows.Next() {
		var g model.Genre
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpsertPerson inserts or finds a person by TMDB id, returning the row id.
func (s *Store) UpsertPerson(ctx context.Context, p tmdb.CastMember) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM people WHERE tmdb_id = ?`, p.ID).Scan(&id)
	if err == nil {
		// refresh profile path if we now have one and the row didn't
		if p.ProfilePath != "" {
			_, _ = s.DB.ExecContext(ctx,
				`UPDATE people SET profile_path = ? WHERE id = ? AND profile_path = ''`,
				p.ProfilePath, id)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO people(name, tmdb_id, profile_path) VALUES (?, ?, ?)`,
		p.Name, p.ID, p.ProfilePath)
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}

// upsertPersonByCrew mirrors UpsertPerson for crew rows.
func (s *Store) upsertPersonByCrew(ctx context.Context, p tmdb.CrewMember) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM people WHERE tmdb_id = ?`, p.ID).Scan(&id)
	if err == nil {
		if p.ProfilePath != "" {
			_, _ = s.DB.ExecContext(ctx,
				`UPDATE people SET profile_path = ? WHERE id = ? AND profile_path = ''`,
				p.ProfilePath, id)
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO people(name, tmdb_id, profile_path) VALUES (?, ?, ?)`,
		p.Name, p.ID, p.ProfilePath)
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}

// ReplaceMovieCredits swaps the cast/crew links for a movie. We cap the cast
// at the first N entries (TMDB returns billing order) and keep all directors
// plus writers.
func (s *Store) ReplaceMovieCredits(ctx context.Context, movieID int64, credits tmdb.Credits, maxCast int) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM movie_credits WHERE movie_id = ?`, movieID); err != nil {
		return err
	}
	// cast
	if maxCast > len(credits.Cast) {
		maxCast = len(credits.Cast)
	}
	for i := 0; i < maxCast; i++ {
		c := credits.Cast[i]
		pid, err := s.UpsertPerson(ctx, c)
		if err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `
INSERT OR REPLACE INTO movie_credits(movie_id, person_id, role, job, character, "order")
VALUES (?, ?, 'cast', '', ?, ?)`,
			movieID, pid, c.Character, c.Order); err != nil {
			return err
		}
	}
	// crew: only keep Directors and Writers
	for _, c := range credits.Crew {
		if c.Job != "Director" && c.Job != "Writer" && c.Job != "Screenplay" {
			continue
		}
		pid, err := s.upsertPersonByCrew(ctx, c)
		if err != nil {
			return err
		}
		if _, err := s.DB.ExecContext(ctx, `
INSERT OR REPLACE INTO movie_credits(movie_id, person_id, role, job, character, "order")
VALUES (?, ?, 'crew', ?, '', 0)`,
			movieID, pid, c.Job); err != nil {
			return err
		}
	}
	return nil
}

// ListMovieCredits returns cast + crew for a movie, joined with person info.
func (s *Store) ListMovieCredits(ctx context.Context, movieID int64) (cast, crew []model.Credit, people map[int64]model.Person, err error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT mc.movie_id, mc.person_id, mc.role, mc.job, mc.character, mc."order",
       p.name, p.tmdb_id, p.profile_path
FROM movie_credits mc
JOIN people p ON p.id = mc.person_id
WHERE mc.movie_id = ?
ORDER BY mc.role, mc."order"`, movieID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	cast = []model.Credit{}
	crew = []model.Credit{}
	people = map[int64]model.Person{}
	for rows.Next() {
		var c model.Credit
		var p model.Person
		var tmdbID sql.NullInt64
		if err := rows.Scan(&c.MovieID, &c.PersonID, &c.Role, &c.Job,
			&c.Character, &c.Order, &p.Name, &tmdbID, &p.ProfilePath); err != nil {
			return nil, nil, nil, err
		}
		p.ID = c.PersonID
		p.TMDBID = tmdbID.Int64
		people[c.PersonID] = p
		if c.Role == "cast" {
			cast = append(cast, c)
		} else {
			crew = append(crew, c)
		}
	}
	return cast, crew, people, rows.Err()
}

// UpsertCollection inserts or updates a system collection from TMDB.
func (s *Store) UpsertCollection(ctx context.Context, col *tmdb.Collection) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT id FROM collections WHERE tmdb_id = ?`, col.ID).Scan(&id)
	if err == nil {
		_, err := s.DB.ExecContext(ctx, `
UPDATE collections SET name=?, poster_path=?, backdrop_path=?, overview=?, updated_at=?
WHERE id=?`,
			col.Name, col.PosterPath, col.BackdropPath, col.Overview,
			time.Now().Unix(), id)
		return id, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `
INSERT INTO collections(name, tmdb_id, poster_path, backdrop_path, overview, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		col.Name, col.ID, col.PosterPath, col.BackdropPath, col.Overview, now, now)
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	return id, nil
}
