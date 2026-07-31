-- 008_collection_parts.sql - cache the member movies of each TMDB collection.
--
-- The /collection/{id} endpoint returns a "parts" array listing every film in
-- a series. We persist those members so the UI can show which films of a
-- collection are already in the library ("已有") and which are missing
-- ("缺失"), without re-hitting TMDB. Rows are written on demand by the
-- "刷新元数据" action (EnrichCollection) and replaced wholesale on each refresh.
--
-- "order" preserves TMDB's parts-array index (release order) so the detail
-- page can render the series chronologically with owned and missing films
-- interleaved.

CREATE TABLE collection_parts (
    id              INTEGER PRIMARY KEY,
    collection_id   INTEGER NOT NULL,
    tmdb_id         INTEGER NOT NULL,         -- TMDB movie id of the member
    title           TEXT NOT NULL DEFAULT '',
    original_title  TEXT NOT NULL DEFAULT '',
    release_date    TEXT NOT NULL DEFAULT '',  -- YYYY-MM-DD, for ordering
    poster_path     TEXT NOT NULL DEFAULT '',
    overview        TEXT NOT NULL DEFAULT '',
    vote_average    REAL NOT NULL DEFAULT 0,
    "order"         INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0,
    UNIQUE (collection_id, tmdb_id),
    FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE
);
CREATE INDEX idx_collection_parts_collection ON collection_parts(collection_id, "order");
