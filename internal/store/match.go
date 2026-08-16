package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/moviegeek/darknight/internal/model"
)

// SetMovieMatch records a matcher decision on a movie row: the accepted
// tmdb_id (0 = none) and the state-machine fields. Status must be one of the
// model.MatchStatus* values.
func (s *Store) SetMovieMatch(ctx context.Context, movieID int64, tmdbID int64, status string, score int, failReason string) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE movies SET
  tmdb_id = COALESCE(?, tmdb_id),
  match_status = ?,
  match_score = ?,
  match_attempts = match_attempts + 1,
  last_match_at = ?,
  fail_reason = ?,
  updated_at = ?
WHERE id = ?`,
		nullableInt64(tmdbID), status, score, time.Now().Unix(), failReason, time.Now().Unix(), movieID)
	return err
}

// SetMatchCandidates stores (or clears, when empty) the pending candidate
// list as JSON on the movie row, for the manual-review UI. Candidates from
// the matcher package marshal to a compact JSON array.
func (s *Store) SetMatchCandidates(ctx context.Context, movieID int64, cands interface{}) error {
	var body string
	if cands != nil {
		b, err := json.Marshal(cands)
		if err != nil {
			return err
		}
		body = string(b)
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE movies SET match_candidates = ?, updated_at = ? WHERE id = ?`,
		body, time.Now().Unix(), movieID)
	return err
}

// GetMatchCandidates reads the stored pending candidate JSON ("" when none).
func (s *Store) GetMatchCandidates(ctx context.Context, movieID int64) (string, error) {
	var body string
	err := s.DB.QueryRowContext(ctx,
		`SELECT match_candidates FROM movies WHERE id = ?`, movieID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return body, err
}

// ListMoviesForRematch returns movies the matcher should retry: unmatched or
// pending rows whose last attempt is older than olderThan seconds (0 = all
// regardless of age), plus never-attempted rows. Rows with match_status
// 'manual' are never returned - a human decision is final.
func (s *Store) ListMoviesForRematch(ctx context.Context, olderThan int64) ([]model.Movie, error) {
	cutoff := time.Now().Unix() - olderThan
	rows, err := s.DB.QueryContext(ctx, `
SELECT `+movieColumns("m.")+`
FROM movies m
WHERE m.match_status IN ('unmatched','pending')
  AND (m.last_match_at = 0 OR m.last_match_at < ?)`,
		cutoff)
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

// ClearMovieMatch removes the tmdb_id and resets the row to unmatched - the
// "detatch wrong match" action. The movie row itself (and its title) is kept;
// a rescan re-runs the matcher against it.
func (s *Store) ClearMovieMatch(ctx context.Context, movieID int64) error {
	res, err := s.DB.ExecContext(ctx, `
UPDATE movies SET
  tmdb_id = NULL,
  match_status = ?,
  match_score = 0,
  fail_reason = 'unmatched by user',
  match_candidates = '',
  updated_at = ?
WHERE id = ?`, model.MatchStatusUnmatched, time.Now().Unix(), movieID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FindMovieByTMDB resolves a movie row by its tmdb_id (nil when absent).
func (s *Store) FindMovieByTMDB(ctx context.Context, tmdbID int64) (*model.Movie, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+movieColumns("m.")+` FROM movies m WHERE m.tmdb_id = ?`, tmdbID)
	m, err := scanMovieRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return m, nil
}

// DeleteMovieIfEmpty drops a movie row only when no movie_files reference it.
// Used after merging a duplicate seed row into the row that owns its tmdb_id:
// the guard makes the call safe to run unconditionally.
func (s *Store) DeleteMovieIfEmpty(ctx context.Context, movieID int64) error {
	_, err := s.DB.ExecContext(ctx, `
DELETE FROM movies WHERE id = ?
  AND NOT EXISTS (SELECT 1 FROM movie_files WHERE movie_id = movies.id)`, movieID)
	return err
}

// RenameMovieFileRelease rewrites a movie_file row's release key and paths
// after an on-disk rename: dir_path/file_name move to the new release name,
// the stored nfo path follows, and external subtitle rows are repointed.
//
// absDirNew is the release directory's new absolute path - both the nfo and
// the subtitle paths are absolute and live inside it, so their directory
// segment must be rewritten too, not just the basename.
func (s *Store) RenameMovieFileRelease(ctx context.Context, movieFileID int64, newRelDir, newFileName, newNFOName, absDirNew string) error {
	now := time.Now().Unix()
	if _, err := s.DB.ExecContext(ctx, `
UPDATE movie_files SET
  dir_path = ?, file_name = ?, nfo_path = ?, raw_name = ?,
  updated_at = ?
WHERE id = ?`,
		newRelDir, newFileName, newNFOName, newFileName, now, movieFileID); err != nil {
		return err
	}
	// external subtitles: rebuild as <absDirNew>/<new main stem>.<lang>.<ext>
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, file_path FROM subtitles WHERE movie_file_id = ? AND is_embedded = 0 AND file_path != ''`,
		movieFileID)
	if err != nil {
		return err
	}
	defer rows.Close()
	type sub struct {
		id   int64
		path string
	}
	var subs []sub
	for rows.Next() {
		var x sub
		if err := rows.Scan(&x.id, &x.path); err != nil {
			return err
		}
		subs = append(subs, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range subs {
		newPath := remapSubPath(x.path, newFileName, absDirNew)
		if newPath == "" || newPath == x.path {
			continue
		}
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE subtitles SET file_path = ? WHERE id = ?`, newPath, x.id); err != nil {
			return err
		}
	}
	return nil
}

// remapSubPath rebuilds an external subtitle's absolute path after a rename:
// the new release directory, the new main-file stem, and the original language
// tag + extension. The rename engine names subs "<main stem>.<tag>.<ext>", so
// the tag is read from the old basename's trailing ".<tag>.<ext>".
func remapSubPath(oldPath, newMainFile, absDirNew string) string {
	oldBase := filepath.Base(oldPath)
	ext := filepath.Ext(oldBase)
	stem := strings.TrimSuffix(oldBase, ext)
	tag := ""
	if i := strings.LastIndex(stem, "."); i >= 0 {
		cand := stem[i+1:]
		if len(cand) >= 2 && len(cand) <= 3 && isASCIILetters(cand) {
			tag = cand
		}
	}
	newStem := strings.TrimSuffix(newMainFile, filepath.Ext(newMainFile))
	newBase := newStem
	if tag != "" {
		newBase += "." + tag
	}
	newBase += ext
	dir := absDirNew
	if dir == "" {
		dir = filepath.Dir(oldPath)
	}
	return filepath.Join(dir, newBase)
}

func isASCIILetters(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// AttachMovieFiles points every movie_file of fromMovieID at toMovieID - used
// after a manual match or a matcher correction merges two logical rows.
func (s *Store) AttachMovieFiles(ctx context.Context, fromMovieID, toMovieID int64) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE movie_files SET movie_id = ?, updated_at = ? WHERE movie_id = ?`,
		toMovieID, time.Now().Unix(), fromMovieID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
