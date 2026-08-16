-- 009: file-grained releases + match state machine
--
-- 1. movie_files unique key widens from (library_id, dir_path) to
--    (library_id, dir_path, file_name): a directory may legitimately hold
--    several versions of the same film (720p + 1080p) or several films of a
--    flat collection pack (Alien.Anthology). Every video file is now its own
--    row; discs keep file_name = ''.
-- 2. movies gains the match state machine columns so unmatched movies are
--    observable and manually correctable instead of silently sitting with a
--    NULL tmdb_id.

DROP INDEX IF EXISTS idx_movie_files_release;
CREATE UNIQUE INDEX idx_movie_files_release
    ON movie_files(library_id, dir_path, file_name);

ALTER TABLE movies ADD COLUMN match_status TEXT NOT NULL DEFAULT 'unmatched'
    CHECK (match_status IN ('matched','pending','unmatched','manual'));
ALTER TABLE movies ADD COLUMN match_score    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN match_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN last_match_at  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movies ADD COLUMN fail_reason    TEXT NOT NULL DEFAULT ''; --供 UI 展示
ALTER TABLE movies ADD COLUMN match_candidates TEXT NOT NULL DEFAULT ''; -- JSON array of matcher.Candidate (pending rows)

-- existing rows with a tmdb_id are considered matched
UPDATE movies SET match_status = 'matched' WHERE tmdb_id IS NOT NULL;
