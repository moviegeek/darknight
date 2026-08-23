package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/moviegeek/darknight/internal/model"
)

// FindMovieFileByRelease looks up a movie_file by its unique (library, dir,
// file) release key. Returns ErrNotFound when absent - the scanner uses this
// to decide between insert and update. fileName is '' for disc releases.
func (s *Store) FindMovieFileByRelease(ctx context.Context, libraryID int64, dirPath, fileName string) (*model.MovieFile, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT id, movie_id, library_id, '' AS library_name, dir_path, file_name, is_disc, file_size, file_modified,
  release_group, edition, source, resolution, video_codec, audio_codec, audio_channels,
  hdr, dolby_vision, bit_depth, audio_count, language, is_collection, raw_name,
  duration_sec, video_bitrate, frame_rate, width, height, container,
  ffprobe_json, ffprobe_version, ffprobe_at,
  nfo_path, subtitle_languages, has_external_subtitle, scanned_at, created_at, updated_at
FROM movie_files WHERE library_id = ? AND dir_path = ? AND file_name = ?`, libraryID, dirPath, fileName)
	mf, err := scanMovieFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &mf, nil
}

// UpsertMovieFile inserts a new movie_file or updates an existing one keyed by
// (library_id, dir_path, file_name). On update the parsed fields are
// rewritten; ffprobe fields are kept unless re-probed (caller passes 0 to mean
// "unknown/keep").
func (s *Store) UpsertMovieFile(ctx context.Context, mf *model.MovieFile) error {
	now := time.Now().Unix()
	if mf.CreatedAt == 0 {
		mf.CreatedAt = now
	}
	mf.UpdatedAt = now
	if mf.ScannedAt == 0 {
		mf.ScannedAt = now
	}

	isDisc := 0
	if mf.IsDisc {
		isDisc = 1
	}
	dv := 0
	if mf.DolbyVision {
		dv = 1
	}
	isColl := 0
	if mf.IsCollection {
		isColl = 1
	}
	hasExtSub := 0
	if mf.HasExternalSubtitle {
		hasExtSub = 1
	}
	movieID := nullableInt64(mf.MovieID)

	existing, err := s.FindMovieFileByRelease(ctx, mf.LibraryID, mf.DirPath, mf.FileName)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if existing != nil {
		mf.ID = existing.ID
		_, err := s.DB.ExecContext(ctx, `
UPDATE movie_files SET movie_id=?, file_name=?, is_disc=?, file_size=?, file_modified=?,
  release_group=?, edition=?, source=?, resolution=?, video_codec=?, audio_codec=?,
  audio_channels=?, hdr=?, dolby_vision=?, bit_depth=?, audio_count=?, language=?,
  is_collection=?, raw_name=?, duration_sec=?, video_bitrate=?, frame_rate=?, width=?, height=?,
  container=?, ffprobe_json=?, ffprobe_version=?, ffprobe_at=?,
  nfo_path=?, subtitle_languages=?, has_external_subtitle=?, scanned_at=?, updated_at=?
WHERE id=?`,
			movieID, mf.FileName, isDisc, mf.FileSize, mf.FileModified,
			mf.ReleaseGroup, mf.Edition, mf.Source, mf.Resolution, mf.VideoCodec,
			mf.AudioCodec, mf.AudioChannels, mf.HDR, dv, mf.BitDepth, mf.AudioCount,
			mf.Language, isColl, mf.RawName, mf.DurationSec, mf.VideoBitrate,
			mf.FrameRate, mf.Width, mf.Height, mf.Container,
			mf.FFProbeJSON, mf.FFProbeVersion, mf.FFProbeAt,
			mf.NFOPath,
			mf.SubtitleLanguages, hasExtSub, mf.ScannedAt, mf.UpdatedAt, mf.ID)
		return err
	}

	res, err := s.DB.ExecContext(ctx, `
INSERT INTO movie_files(movie_id, library_id, dir_path, file_name, is_disc, file_size,
  file_modified, release_group, edition, source, resolution, video_codec, audio_codec,
  audio_channels, hdr, dolby_vision, bit_depth, audio_count, language, is_collection,
  raw_name, duration_sec, video_bitrate, frame_rate, width, height, container,
  ffprobe_json, ffprobe_version, ffprobe_at,
  nfo_path, subtitle_languages, has_external_subtitle, scanned_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		movieID, mf.LibraryID, mf.DirPath, mf.FileName, isDisc, mf.FileSize,
		mf.FileModified, mf.ReleaseGroup, mf.Edition, mf.Source, mf.Resolution,
		mf.VideoCodec, mf.AudioCodec, mf.AudioChannels, mf.HDR, dv, mf.BitDepth,
		mf.AudioCount, mf.Language, isColl, mf.RawName, mf.DurationSec,
		mf.VideoBitrate, mf.FrameRate, mf.Width, mf.Height, mf.Container,
		mf.FFProbeJSON, mf.FFProbeVersion, mf.FFProbeAt,
		mf.NFOPath, mf.SubtitleLanguages, hasExtSub, mf.ScannedAt, mf.CreatedAt, mf.UpdatedAt)
	if err != nil {
		return err
	}
	mf.ID, _ = res.LastInsertId()
	return nil
}

// ListMovieFilesForReprobe returns file (non-disc) releases whose ffprobe cache
// is missing or was captured under a different ProbeVersion. Used by the
// `darknight reprobe` backfill command. Ordered by id for stable progress.
// Caller resolves each row's absolute path from its LibraryID + DirPath +
// FileName.
func (s *Store) ListMovieFilesForReprobe(ctx context.Context, version int) ([]model.MovieFile, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, movie_id, library_id, '' AS library_name, dir_path, file_name, is_disc, file_size, file_modified,
  release_group, edition, source, resolution, video_codec, audio_codec, audio_channels,
  hdr, dolby_vision, bit_depth, audio_count, language, is_collection, raw_name,
  duration_sec, video_bitrate, frame_rate, width, height, container,
  ffprobe_json, ffprobe_version, ffprobe_at,
  nfo_path, subtitle_languages, has_external_subtitle, scanned_at, created_at, updated_at
FROM movie_files WHERE is_disc = 0 AND (ffprobe_version = 0 OR ffprobe_version != ?)
ORDER BY id`, version)
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

// UpdateFFProbeCache writes the cached ffprobe JSON for a movie_file without
// touching the parsed/derived columns. Used by the `darknight reprobe` backfill
// to populate JSON for rows scanned before the cache existed.
func (s *Store) UpdateFFProbeCache(ctx context.Context, id int64, rawJSON string, version int, at int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE movie_files SET ffprobe_json=?, ffprobe_version=?, ffprobe_at=?, updated_at=? WHERE id=?`,
		rawJSON, version, at, time.Now().Unix(), id)
	return err
}

// ReplaceAudioTracks swaps the audio tracks for a movie_file.
func (s *Store) ReplaceAudioTracks(ctx context.Context, movieFileID int64, tracks []model.AudioTrack) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM audio_tracks WHERE movie_file_id = ?`, movieFileID); err != nil {
		return err
	}
	for i := range tracks {
		t := tracks[i]
		t.MovieFileID = movieFileID
		isDefault := 0
		if t.IsDefault {
			isDefault = 1
		}
		isLossless := 0
		if t.IsLossless {
			isLossless = 1
		}
		if _, err := s.DB.ExecContext(ctx, `
INSERT INTO audio_tracks(movie_file_id, language, codec, channels, title, is_default, is_lossless, "order")
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			t.MovieFileID, t.Language, t.Codec, t.Channels, t.Title,
			isDefault, isLossless, t.Order); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceSubtitles swaps the subtitles for a movie_file.
func (s *Store) ReplaceSubtitles(ctx context.Context, movieFileID int64, subs []model.Subtitle) error {
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM subtitles WHERE movie_file_id = ?`, movieFileID); err != nil {
		return err
	}
	for i := range subs {
		sub := subs[i]
		sub.MovieFileID = movieFileID
		isEmb := 0
		if sub.IsEmbedded {
			isEmb = 1
		}
		isDef := 0
		if sub.IsDefault {
			isDef = 1
		}
		if _, err := s.DB.ExecContext(ctx, `
INSERT INTO subtitles(movie_file_id, file_path, language, format, is_embedded, is_default, "order", file_size)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sub.MovieFileID, sub.FilePath, sub.Language, sub.Format,
			isEmb, isDef, sub.Order, sub.FileSize); err != nil {
			return err
		}
	}
	return nil
}

// ListAudioTracks returns the audio tracks for a movie_file, in stream order.
func (s *Store) ListAudioTracks(ctx context.Context, movieFileID int64) ([]model.AudioTrack, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, movie_file_id, language, codec, channels, title, is_default, is_lossless, "order"
FROM audio_tracks WHERE movie_file_id = ? ORDER BY "order"`, movieFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// initialise to a non-nil slice so JSON serialises to [] instead of null;
	// the frontend reads .length on these unconditionally.
	out := []model.AudioTrack{}
	for rows.Next() {
		var t model.AudioTrack
		var isDef, isLoss int64
		if err := rows.Scan(&t.ID, &t.MovieFileID, &t.Language, &t.Codec,
			&t.Channels, &t.Title, &isDef, &isLoss, &t.Order); err != nil {
			return nil, err
		}
		t.IsDefault = isDef != 0
		t.IsLossless = isLoss != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListSubtitles returns the subtitles for a movie_file, in stream order.
func (s *Store) ListSubtitles(ctx context.Context, movieFileID int64) ([]model.Subtitle, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, movie_file_id, file_path, language, format, is_embedded, is_default, "order", file_size
FROM subtitles WHERE movie_file_id = ? ORDER BY "order"`, movieFileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Subtitle{}
	for rows.Next() {
		var sub model.Subtitle
		var isEmb, isDef int64
		if err := rows.Scan(&sub.ID, &sub.MovieFileID, &sub.FilePath,
			&sub.Language, &sub.Format, &isEmb, &isDef, &sub.Order, &sub.FileSize); err != nil {
			return nil, err
		}
		sub.IsEmbedded = isEmb != 0
		sub.IsDefault = isDef != 0
		out = append(out, sub)
	}
	return out, rows.Err()
}

// RemoveStaleMovieFiles deletes movie_files for a library whose (dir, file)
// release key is no longer present in the keep set. Called at the end of a
// scan to prune deletions. An empty keep set is honoured (the library is
// genuinely empty); the scanner guards against a failed walk before calling.
func (s *Store) RemoveStaleMovieFiles(ctx context.Context, libraryID int64, keep []string) (int64, error) {
	if len(keep) == 0 {
		res, err := s.DB.ExecContext(ctx,
			`DELETE FROM movie_files WHERE library_id = ?`, libraryID)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	// NOT IN (...) with a bounded placeholder list.
	placeholders := ""
	args := make([]interface{}, 0, len(keep)+1)
	args = append(args, libraryID)
	for i, key := range keep {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, key)
	}
	// NOTE: SQLite string literals have no \xNN escape ('\x00' truncates to
	// ''), so the key separator must be built with char(0) to match the
	// Go-side releaseKey joiner.
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM movie_files WHERE library_id = ? AND dir_path || char(0) || file_name NOT IN (`+placeholders+`)`,
		args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AddSubtitle inserts one external subtitle row and refreshes the movie_file's
// subtitle_languages aggregate. Called by the upload endpoint after the file
// is on disk; the aggregate must follow, or the "no Chinese subtitle" filter
// and the 中字 badge read stale data (see the carry-over regression test).
func (s *Store) AddSubtitle(ctx context.Context, movieFileID int64, sub model.Subtitle) error {
	isDef := 0
	if sub.IsDefault {
		isDef = 1
	}
	res, err := s.DB.ExecContext(ctx, `
INSERT INTO subtitles(movie_file_id, file_path, language, format, is_embedded, is_default, "order", file_size)
VALUES (?, ?, ?, ?, 0, ?, COALESCE((SELECT MAX("order")+1 FROM subtitles WHERE movie_file_id = ?), 0), ?)`,
		movieFileID, sub.FilePath, sub.Language, sub.Format, isDef, movieFileID, sub.FileSize)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	sub.ID = id
	return s.refreshSubtitleAggregate(ctx, movieFileID)
}

// refreshSubtitleAggregate recomputes subtitle_languages / has_external_subtitle
// for one movie_file from its detail rows, preserving first-seen order.
func (s *Store) refreshSubtitleAggregate(ctx context.Context, movieFileID int64) error {
	_, err := s.DB.ExecContext(ctx, `
UPDATE movie_files SET
  subtitle_languages = COALESCE((
    SELECT GROUP_CONCAT(lang, ',') FROM (
      SELECT DISTINCT s.language AS lang, MIN(s."order") AS first_order
      FROM subtitles s
      WHERE s.movie_file_id = ? AND s.language != ''
      GROUP BY s.language ORDER BY first_order
    )), ''),
  has_external_subtitle = EXISTS(
    SELECT 1 FROM subtitles s
    WHERE s.movie_file_id = ? AND s.is_embedded = 0),
  updated_at = ?
WHERE id = ?`,
		movieFileID, movieFileID, time.Now().Unix(), movieFileID)
	return err
}

// UpdateSubtitlePath repoints a subtitle row from one absolute path to another
// after an on-disk rename. No-op when no row matches (e.g. a sidecar file the
// scanner never registered).
func (s *Store) UpdateSubtitlePath(ctx context.Context, movieFileID int64, oldPath, newPath string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE subtitles SET file_path = ? WHERE movie_file_id = ? AND file_path = ?`,
		newPath, movieFileID, oldPath)
	return err
}
