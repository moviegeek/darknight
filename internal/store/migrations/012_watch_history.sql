-- 012: per-movie watch history (multiple plays with dates)
--
-- watch_status stays the summary row used by filters ("is this movie
-- watched?"). watch_history records each individual viewing. Manual marks
-- always write a history row and upsert watch_status; removing the last
-- history row clears watch_status. Trakt sync still writes watch_status only
-- (no per-play detail from Trakt), so a watched movie may temporarily have
-- status without history until the user adds/removes a play.

CREATE TABLE watch_history (
    id         INTEGER PRIMARY KEY,
    movie_id   INTEGER NOT NULL,
    watched_at INTEGER NOT NULL,              -- unix seconds
    has_time   INTEGER NOT NULL DEFAULT 0,    -- 1 = user supplied a clock time
    source     TEXT NOT NULL DEFAULT 'manual'
               CHECK (source IN ('manual', 'trakt')),
    created_at INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
);

CREATE INDEX idx_watch_history_movie ON watch_history(movie_id, watched_at DESC);
