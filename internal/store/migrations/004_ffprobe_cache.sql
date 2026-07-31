-- 004_ffprobe_cache.sql - cache verbatim ffprobe JSON per movie_file.
--
-- The scanner already skips ffprobe on unchanged files (size+mtime match);
-- this caches the raw JSON so future media-info fields can be backfilled
-- without re-probing. ffprobe_version invalidates the cache when the probe
-- flags change; ffprobe_at records when the JSON was captured.

ALTER TABLE movie_files ADD COLUMN ffprobe_json    TEXT    NOT NULL DEFAULT '';
ALTER TABLE movie_files ADD COLUMN ffprobe_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE movie_files ADD COLUMN ffprobe_at      INTEGER NOT NULL DEFAULT 0;

-- find rows needing (re)probing quickly during the backfill command.
CREATE INDEX IF NOT EXISTS idx_movie_files_ffprobe_version
    ON movie_files(ffprobe_version) WHERE ffprobe_version = 0;
