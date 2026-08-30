// Trakt.tv integration persistence: the single-row OAuth/sync state table
// (migration 011), the union-semantics watch-status upsert used by the sync,
// and the external-id lookup tables used to match Trakt entries to local
// movies. Client credentials live in env config, never here.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/moviegeek/darknight/internal/model"
)

// TraktState mirrors the trakt_state row (id=1, always present after
// migration). AccessToken empty means "not connected".
type TraktState struct {
	AccessToken     string
	RefreshToken    string
	TokenExpiresAt  int64  // unix seconds
	Username        string // trakt account chosen at connect time
	RemoteWatchedAt string // movies.watched_at at the last successful sync (RFC3339)
	LastSyncAt      int64  // unix seconds
	LastSyncResult  string // JSON summary for the settings UI
}

// Connected reports whether OAuth tokens are stored.
func (s *TraktState) Connected() bool { return s != nil && s.AccessToken != "" }

// GetTraktState returns the trakt_state singleton row.
func (s *Store) GetTraktState(ctx context.Context) (*TraktState, error) {
	var st TraktState
	err := s.DB.QueryRowContext(ctx, `
		SELECT access_token, refresh_token, token_expires_at, username,
		       remote_watched_at, last_sync_at, last_sync_result
		FROM trakt_state WHERE id = 1`).
		Scan(&st.AccessToken, &st.RefreshToken, &st.TokenExpiresAt, &st.Username,
			&st.RemoteWatchedAt, &st.LastSyncAt, &st.LastSyncResult)
	if err != nil {
		return nil, fmt.Errorf("get trakt_state: %w", err)
	}
	return &st, nil
}

// SaveTraktAuth stores the OAuth pair (and the account name) from a completed
// device flow or a token refresh. Tokens are rotated together - refresh
// tokens are single-use.
func (s *Store) SaveTraktAuth(ctx context.Context, username, accessToken, refreshToken string, expiresAt int64) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE trakt_state
		SET access_token = ?, refresh_token = ?, token_expires_at = ?, username = ?
		WHERE id = 1`,
		accessToken, refreshToken, expiresAt, username)
	if err != nil {
		return fmt.Errorf("save trakt auth: %w", err)
	}
	return nil
}

// ClearTraktAuth forgets the OAuth pair (disconnect). Sync history stays.
func (s *Store) ClearTraktAuth(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE trakt_state
		SET access_token = '', refresh_token = '', token_expires_at = 0, username = ''
		WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("clear trakt auth: %w", err)
	}
	return nil
}

// SaveTraktSync records the sync bookkeeping: the remote watched-at snapshot
// used for change detection and the JSON result shown in the UI.
func (s *Store) SaveTraktSync(ctx context.Context, remoteWatchedAt, resultJSON string, at int64) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE trakt_state
		SET remote_watched_at = ?, last_sync_at = ?, last_sync_result = ?
		WHERE id = 1`, remoteWatchedAt, at, resultJSON)
	if err != nil {
		return fmt.Errorf("save trakt sync: %w", err)
	}
	return nil
}

// WatchedMark is one "mark this movie watched" instruction from the sync.
type WatchedMark struct {
	MovieID      int64
	LastPlayedAt int64 // unix seconds, 0 = unknown
}

// MarkWatched applies union-semantics upserts: Trakt may mark a movie watched
// and advance last_played_at, but never clears a local watched row and never
// touches progress or rating. Movies already watched locally with a newer
// timestamp keep theirs. The batch runs in one transaction.
func (s *Store) MarkWatched(ctx context.Context, marks []WatchedMark) error {
	if len(marks) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mark watched: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO watch_status (movie_id, status, progress, last_played_at, rating, updated_at)
		VALUES (?, 'watched', 0, ?, 0, ?)
		ON CONFLICT(movie_id) DO UPDATE SET
			status = 'watched',
			last_played_at = MAX(watch_status.last_played_at, excluded.last_played_at),
			updated_at = excluded.updated_at`)
	if err != nil {
		return fmt.Errorf("mark watched prepare: %w", err)
	}
	defer stmt.Close()

	now := time.Now().Unix()
	for _, m := range marks {
		if _, err := stmt.ExecContext(ctx, m.MovieID, m.LastPlayedAt, now); err != nil {
			return fmt.Errorf("mark watched movie %d: %w", m.MovieID, err)
		}
	}
	return tx.Commit()
}

// MovieIDByExternalID builds the lookup tables for matching Trakt entries to
// local movies: tmdb id -> movie id and imdb id (lowercased, "tt" included)
// -> movie id.
func (s *Store) MovieIDByExternalID(ctx context.Context) (map[int64]int64, map[string]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, tmdb_id, imdb_id FROM movies`)
	if err != nil {
		return nil, nil, fmt.Errorf("movie external ids: %w", err)
	}
	defer rows.Close()

	byTMDB := make(map[int64]int64)
	byIMDB := make(map[string]int64)
	for rows.Next() {
		var id int64
		var tmdbID sql.NullInt64 // NULL on movies without TMDB enrichment
		var imdbID sql.NullString
		if err := rows.Scan(&id, &tmdbID, &imdbID); err != nil {
			return nil, nil, err
		}
		if tmdbID.Valid && tmdbID.Int64 != 0 {
			byTMDB[tmdbID.Int64] = id
		}
		if imdbID.Valid && imdbID.String != "" {
			byIMDB[strings.ToLower(imdbID.String)] = id
		}
	}
	return byTMDB, byIMDB, rows.Err()
}

// ListWatchStatus returns the whole watch_status table keyed by movie id.
// Used by the sync to classify new vs already-watched without per-row reads.
func (s *Store) ListWatchStatus(ctx context.Context) (map[int64]model.WatchStatus, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT movie_id, status, progress, last_played_at, rating, updated_at
		FROM watch_status`)
	if err != nil {
		return nil, fmt.Errorf("list watch_status: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]model.WatchStatus)
	for rows.Next() {
		var ws model.WatchStatus
		if err := rows.Scan(&ws.MovieID, &ws.Status, &ws.Progress, &ws.LastPlayedAt,
			&ws.Rating, &ws.UpdatedAt); err != nil {
			return nil, err
		}
		out[ws.MovieID] = ws
	}
	return out, rows.Err()
}
