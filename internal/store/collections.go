package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/tmdb"
)

// CollectionWithCount is a system collection plus how many of its member
// movies are present in the local library. The collections grid renders this
// count as a corner badge.
type CollectionWithCount struct {
	model.Collection
	MovieCount  int `json:"movie_count"`   // films of this collection on disk
	TotalParts  int `json:"total_parts"`   // total films in the TMDB collection (0 if never refreshed)
}

// collectionColumns is the canonical column list for scanning a collections
// row. tmdb_id is nullable (UNIQUE INTEGER without NOT NULL), so the caller
// scans it into a sql.NullInt64 and copies the value back onto the struct.
//
// The bare form is used for the single-row GetCollection path (no join); the
// list query uses collectionColumnsPrefixed because it joins movies, whose
// id/name/tmdb_id columns would otherwise be ambiguous.
const collectionColumns = `id, name, tmdb_id, poster_path, backdrop_path, overview, created_at, updated_at`

// collectionColumnsPrefixed is collectionColumns with the given table alias
// prepended to each column, for use in joins.
func collectionColumnsPrefixed(alias string) string {
	return alias + "id, " + alias + "name, " + alias + "tmdb_id, " + alias +
		"poster_path, " + alias + "backdrop_path, " + alias + "overview, " +
		alias + "created_at, " + alias + "updated_at"
}

// ListCollectionsWithCount returns every system collection that has at least
// minMovies local movies, with that movie count attached. Pass 2 to show only
// genuine multi-film collections (a single-member "合集" is rarely useful); pass
// 1 to include every collection that has at least one film on disk. Collections
// with no local movies are always hidden (leftover rows from a TMDB record
// whose films were never scanned). Only movies with at least one movie_file
// count as "local" - file-less movie rows are orphaned index entries the
// library list hides, so counting them here would disagree with the grid.
// Order is by movie count desc, then name asc, so the biggest collections
// surface first.
func (s *Store) ListCollectionsWithCount(ctx context.Context, minMovies int) ([]CollectionWithCount, error) {
	if minMovies < 1 {
		minMovies = 1
	}
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+collectionColumnsPrefixed("c.")+`, COUNT(m.id) AS movie_count,
       COALESCE(cp.total, 0) AS total_parts
FROM collections c
JOIN movies m ON m.collection_id = c.id
  AND EXISTS (SELECT 1 FROM movie_files mf WHERE mf.movie_id = m.id)
LEFT JOIN (
  SELECT collection_id, COUNT(*) AS total FROM collection_parts GROUP BY collection_id
) cp ON cp.collection_id = c.id
GROUP BY c.id
HAVING COUNT(m.id) >= ?
ORDER BY movie_count DESC, c.name ASC`, minMovies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionWithCount{}
	for rows.Next() {
		var c CollectionWithCount
		if err := scanCollectionRow(rows, &c.Collection, &c.MovieCount, &c.TotalParts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCollection fetches a single system collection by its row id. Returns
// ErrNotFound when no row matches.
func (s *Store) GetCollection(ctx context.Context, id int64) (*model.Collection, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+collectionColumns+` FROM collections WHERE id = ?`, id)
	var c model.Collection
	if err := scanCollectionRow(row, &c, nil, nil); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ListCollections returns every system collection row, ordered by name. Used
// by the batch "refresh all collections" action so it can iterate every
// collection that might carry a tmdb_id. Unlike ListCollectionsWithCount this
// returns rows with zero local movies too (the refresh should still update
// their metadata/parts).
func (s *Store) ListCollections(ctx context.Context) ([]model.Collection, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+collectionColumns+` FROM collections ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Collection{}
	for rows.Next() {
		var c model.Collection
		if err := scanCollectionRow(rows, &c, nil, nil); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanCollectionRow reads the canonical collection columns plus up to two
// optional trailing integers: the local movie count (ListCollectionsWithCount)
// and the total TMDB parts count. Either may be nil when not selected by the
// query (single-row GetCollection passes both as nil).
func scanCollectionRow(row scanner, c *model.Collection, movieCount, totalParts *int) error {
	var tmdbID sql.NullInt64
	var err error
	switch {
	case movieCount != nil && totalParts != nil:
		err = row.Scan(&c.ID, &c.Name, &tmdbID, &c.PosterPath, &c.BackdropPath,
			&c.Overview, &c.CreatedAt, &c.UpdatedAt, movieCount, totalParts)
	case movieCount != nil:
		err = row.Scan(&c.ID, &c.Name, &tmdbID, &c.PosterPath, &c.BackdropPath,
			&c.Overview, &c.CreatedAt, &c.UpdatedAt, movieCount)
	default:
		err = row.Scan(&c.ID, &c.Name, &tmdbID, &c.PosterPath, &c.BackdropPath,
			&c.Overview, &c.CreatedAt, &c.UpdatedAt)
	}
	if err != nil {
		return err
	}
	c.TMDBID = tmdbID.Int64
	return nil
}

// ReplaceCollectionParts swaps the cached TMDB member movies for a collection.
// The parts slice order is preserved as the "order" column so the detail page
// can render the series in release order. An empty parts slice clears the
// cached members (used when TMDB reports no parts). The whole swap runs in one
// transaction.
func (s *Store) ReplaceCollectionParts(ctx context.Context, collectionID int64, parts []tmdb.CollectionPart) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM collection_parts WHERE collection_id = ?`, collectionID); err != nil {
		return err
	}
	now := time.Now().Unix()
	for i, p := range parts {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO collection_parts(collection_id, tmdb_id, title, original_title, release_date,
  poster_path, overview, vote_average, "order", updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			collectionID, p.ID, p.Title, p.OriginalTitle, p.ReleaseDate,
			p.PosterPath, p.Overview, p.VoteAverage, i, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListCollectionParts returns the cached member movies of a collection in
// release order, with LocalMovieID filled from a LEFT JOIN on movies.tmdb_id
// so the caller can tell which films are already in the library (LocalMovieID
// != 0) and which are missing (LocalMovieID == 0). The join requires the local
// row to have at least one movie_file: a file-less movie row is an orphaned
// index entry, not a film on disk, so its part must render as missing.
// Returns an empty slice (not nil) when the collection has never been
// refreshed against TMDB.
func (s *Store) ListCollectionParts(ctx context.Context, collectionID int64) ([]model.CollectionPart, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT cp.id, cp.collection_id, cp.tmdb_id, cp.title, cp.original_title,
       cp.release_date, cp.poster_path, cp.overview, cp.vote_average, cp."order",
       COALESCE(m.id, 0) AS local_movie_id
FROM collection_parts cp
LEFT JOIN movies m ON m.tmdb_id = cp.tmdb_id
  AND EXISTS (SELECT 1 FROM movie_files mf WHERE mf.movie_id = m.id)
WHERE cp.collection_id = ?
ORDER BY cp."order" ASC`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CollectionPart{}
	for rows.Next() {
		var p model.CollectionPart
		if err := rows.Scan(&p.ID, &p.CollectionID, &p.TMDBID, &p.Title,
			&p.OriginalTitle, &p.ReleaseDate, &p.PosterPath, &p.Overview,
			&p.VoteAverage, &p.Order, &p.LocalMovieID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
