-- 011: trakt.tv watch-status sync state
--
-- Single-row table (id=1, pre-inserted) holding the OAuth tokens acquired via
-- the device flow and the sync bookkeeping. The app credentials (client id /
-- secret) stay in env config; only runtime state lives here.
--
--   access_token / refresh_token / token_expires_at : the current OAuth pair;
--       refresh tokens are single-use, each refresh replaces both columns.
--   username          : the Trakt account chosen at connect time, for display.
--   remote_watched_at : movies.watched_at from /sync/last_activities at the
--       last successful sync - unchanged value means nothing new to import.
--   last_sync_result  : JSON summary of the last sync, shown in the settings UI.

CREATE TABLE trakt_state (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    access_token     TEXT NOT NULL DEFAULT '',
    refresh_token    TEXT NOT NULL DEFAULT '',
    token_expires_at INTEGER NOT NULL DEFAULT 0, -- unix seconds
    username         TEXT NOT NULL DEFAULT '',
    remote_watched_at TEXT NOT NULL DEFAULT '',  -- RFC3339 from last_activities
    last_sync_at     INTEGER NOT NULL DEFAULT 0, -- unix seconds
    last_sync_result TEXT NOT NULL DEFAULT ''    -- JSON
);

INSERT INTO trakt_state (id) VALUES (1);
