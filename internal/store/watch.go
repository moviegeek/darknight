package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/moviegeek/darknight/internal/model"
)

// AddWatch records a viewing and upserts watch_status to watched with
// last_played_at advanced to at least watchedAt.
func (s *Store) AddWatch(ctx context.Context, movieID, watchedAt int64, hasTime bool) (*model.WatchPlay, error) {
	if movieID <= 0 {
		return nil, fmt.Errorf("movie id required")
	}
	if watchedAt <= 0 {
		return nil, fmt.Errorf("watched_at required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	ht := 0
	if hasTime {
		ht = 1
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO watch_history (movie_id, watched_at, has_time, source, created_at)
		VALUES (?, ?, ?, 'manual', ?)`, movieID, watchedAt, ht, now)
	if err != nil {
		return nil, fmt.Errorf("insert watch history: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := upsertWatchedStatus(ctx, tx, movieID, watchedAt, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &model.WatchPlay{
		ID:        id,
		MovieID:   movieID,
		WatchedAt: watchedAt,
		HasTime:   hasTime,
		Source:    "manual",
		CreatedAt: now,
	}, nil
}

// ListWatchHistory returns plays for a movie, newest first. When the movie is
// marked watched in watch_status but has no history rows (e.g. Trakt sync
// only), a synthetic play with id=0 and source="status" is returned so the UI
// can still offer a single removal.
func (s *Store) ListWatchHistory(ctx context.Context, movieID int64) ([]model.WatchPlay, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, movie_id, watched_at, has_time, source, created_at
		FROM watch_history
		WHERE movie_id = ?
		ORDER BY watched_at DESC, id DESC`, movieID)
	if err != nil {
		return nil, fmt.Errorf("list watch history: %w", err)
	}
	defer rows.Close()

	out := []model.WatchPlay{}
	for rows.Next() {
		var p model.WatchPlay
		var ht int
		if err := rows.Scan(&p.ID, &p.MovieID, &p.WatchedAt, &ht, &p.Source, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.HasTime = ht != 0
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		return out, nil
	}

	var status string
	var lastPlayed int64
	err = s.DB.QueryRowContext(ctx, `
		SELECT status, last_played_at FROM watch_status WHERE movie_id = ?`, movieID).
		Scan(&status, &lastPlayed)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if status == "watched" {
		out = append(out, model.WatchPlay{
			ID:        0,
			MovieID:   movieID,
			WatchedAt: lastPlayed,
			HasTime:   lastPlayed != 0,
			Source:    "status",
		})
	}
	return out, nil
}

// DeleteWatch removes one play. playID 0 means "most recent" (or the
// synthetic status-only row). After deletion, watch_status is resynced from
// remaining history or cleared when none remain.
func (s *Store) DeleteWatch(ctx context.Context, movieID, playID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM watch_history WHERE movie_id = ?`, movieID).Scan(&n); err != nil {
		return err
	}

	if n == 0 {
		// Status-only watched (Trakt): clearing removes the summary row.
		res, err := tx.ExecContext(ctx, `DELETE FROM watch_status WHERE movie_id = ?`, movieID)
		if err != nil {
			return err
		}
		aff, _ := res.RowsAffected()
		if aff == 0 {
			return ErrNotFound
		}
		return tx.Commit()
	}

	if playID > 0 {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM watch_history WHERE id = ? AND movie_id = ?`, playID, movieID)
		if err != nil {
			return err
		}
		aff, _ := res.RowsAffected()
		if aff == 0 {
			return ErrNotFound
		}
	} else {
		// Most recent by watched_at, then id.
		res, err := tx.ExecContext(ctx, `
			DELETE FROM watch_history
			WHERE id = (
				SELECT id FROM watch_history
				WHERE movie_id = ?
				ORDER BY watched_at DESC, id DESC
				LIMIT 1
			)`, movieID)
		if err != nil {
			return err
		}
		aff, _ := res.RowsAffected()
		if aff == 0 {
			return ErrNotFound
		}
	}

	if err := resyncWatchStatus(ctx, tx, movieID); err != nil {
		return err
	}
	return tx.Commit()
}

// WatchedMovieIDs returns the set of movie ids currently marked watched.
func (s *Store) WatchedMovieIDs(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT movie_id FROM watch_status WHERE status = 'watched'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// GetWatchStatus returns the summary row for a movie, or nil when absent.
func (s *Store) GetWatchStatus(ctx context.Context, movieID int64) (*model.WatchStatus, error) {
	var ws model.WatchStatus
	err := s.DB.QueryRowContext(ctx, `
		SELECT movie_id, status, progress, last_played_at, rating, updated_at
		FROM watch_status WHERE movie_id = ?`, movieID).
		Scan(&ws.MovieID, &ws.Status, &ws.Progress, &ws.LastPlayedAt, &ws.Rating, &ws.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

func upsertWatchedStatus(ctx context.Context, tx *sql.Tx, movieID, watchedAt, now int64) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO watch_status (movie_id, status, progress, last_played_at, rating, updated_at)
		VALUES (?, 'watched', 0, ?, 0, ?)
		ON CONFLICT(movie_id) DO UPDATE SET
			status = 'watched',
			last_played_at = MAX(watch_status.last_played_at, excluded.last_played_at),
			updated_at = excluded.updated_at`, movieID, watchedAt, now)
	if err != nil {
		return fmt.Errorf("upsert watch status: %w", err)
	}
	return nil
}

// resyncWatchStatus sets watch_status from remaining history, or deletes it
// when the movie has no plays left. Unlike AddWatch's upsert, this assigns
// last_played_at absolutely (it may move backwards when a newer play is
// removed).
func resyncWatchStatus(ctx context.Context, tx *sql.Tx, movieID int64) error {
	var maxAt sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT MAX(watched_at) FROM watch_history WHERE movie_id = ?`, movieID).Scan(&maxAt)
	if err != nil {
		return err
	}
	if !maxAt.Valid {
		_, err := tx.ExecContext(ctx, `DELETE FROM watch_status WHERE movie_id = ?`, movieID)
		return err
	}
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO watch_status (movie_id, status, progress, last_played_at, rating, updated_at)
		VALUES (?, 'watched', 0, ?, 0, ?)
		ON CONFLICT(movie_id) DO UPDATE SET
			status = 'watched',
			last_played_at = excluded.last_played_at,
			updated_at = excluded.updated_at`, movieID, maxAt.Int64, now)
	return err
}
