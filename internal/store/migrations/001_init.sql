-- 001_init.sql — initial schema for the moviegeek library database.
--
-- Conventions:
--   * INTEGER PRIMARY KEY (rowid alias) for all surrogate ids.
--   * timestamps stored as unix seconds (INTEGER), 0 = unknown.
--   * enums stored as TEXT with a CHECK where it aids integrity.
--   * foreign keys are ON; PRAGMA foreign_keys = ON is set by the driver setup.
--   * denormalised technical fields on movie_files come from filename parsing
--     (release_group, edition, source, resolution, codec, hdr, ...) and are
--     cheap filter dimensions, hence indexed.

-- A media library root directory that gets scanned recursively.
CREATE TABLE libraries (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    root_path     TEXT NOT NULL UNIQUE,
    scan_interval INTEGER NOT NULL DEFAULT 0,   -- seconds; 0 = manual only
    last_scan_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0
);

-- A logical movie, populated from TMDB (or .nfo when TMDB is unavailable).
-- One movie may have many movie_files (different releases / resolutions).
CREATE TABLE movies (
    id            INTEGER PRIMARY KEY,
    title         TEXT NOT NULL,
    sort_title    TEXT NOT NULL DEFAULT '',
    year          INTEGER NOT NULL DEFAULT 0,
    release_date  TEXT NOT NULL DEFAULT '',      -- ISO 8601 date
    runtime       INTEGER NOT NULL DEFAULT 0,    -- minutes
    synopsis      TEXT NOT NULL DEFAULT '',
    poster_path   TEXT NOT NULL DEFAULT '',      -- TMDB poster path
    backdrop_path TEXT NOT NULL DEFAULT '',
    tmdb_id       INTEGER UNIQUE,
    imdb_id       TEXT NOT NULL DEFAULT '',
    vote_average  REAL NOT NULL DEFAULT 0,
    vote_count    INTEGER NOT NULL DEFAULT 0,
    collection_id INTEGER,                       -- TMDB / system collection
    created_at    INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE SET NULL
);

CREATE INDEX idx_movies_sort_title ON movies(sort_title);
CREATE INDEX idx_movies_year ON movies(year);
CREATE INDEX idx_movies_tmdb ON movies(tmdb_id);
CREATE INDEX idx_movies_imdb ON movies(imdb_id);

-- A system collection (series / anthology). Sourced from TMDB collections
-- (Alien Anthology, Back to the Future, ...) or detected from filename
-- ("1979-1997", "Trilogy"). user_collections is a separate table below.
CREATE TABLE collections (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    tmdb_id     INTEGER UNIQUE,
    poster_path TEXT NOT NULL DEFAULT '',
    backdrop_path TEXT NOT NULL DEFAULT '',
    overview    TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0
);

-- Genres + many-to-many.
CREATE TABLE genres (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);
CREATE TABLE movie_genres (
    movie_id INTEGER NOT NULL,
    genre_id INTEGER NOT NULL,
    PRIMARY KEY (movie_id, genre_id),
    FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE,
    FOREIGN KEY (genre_id) REFERENCES genres(id) ON DELETE CASCADE
);
CREATE INDEX idx_movie_genres_genre ON movie_genres(genre_id);

-- People (cast/crew) + credits.
CREATE TABLE people (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    tmdb_id     INTEGER UNIQUE,
    profile_path TEXT NOT NULL DEFAULT ''
);
CREATE TABLE movie_credits (
    movie_id  INTEGER NOT NULL,
    person_id INTEGER NOT NULL,
    role      TEXT NOT NULL CHECK (role IN ('cast','crew')),
    job       TEXT NOT NULL DEFAULT '',   -- 'Director','Writer',... (crew)
    character TEXT NOT NULL DEFAULT '',   -- (cast)
    "order"   INTEGER NOT NULL DEFAULT 0, -- cast billing order
    PRIMARY KEY (movie_id, person_id, role)
    FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE,
    FOREIGN KEY (person_id) REFERENCES people(id) ON DELETE CASCADE
);
CREATE INDEX idx_credits_person ON movie_credits(person_id);
CREATE INDEX idx_credits_movie_cast ON movie_credits(movie_id) WHERE role='cast';

-- A single physical release of a movie: a file or a disc folder.
-- Technical fields come from filename parsing; ffprobe refines the
-- container-level ones (duration, bitrate, frame_rate, channels, codecs).
CREATE TABLE movie_files (
    id              INTEGER PRIMARY KEY,
    movie_id        INTEGER,                       -- nullable until matched
    library_id      INTEGER NOT NULL,
    dir_path        TEXT NOT NULL,                 -- absolute dir of the release
    file_name       TEXT NOT NULL DEFAULT '',      -- main file; '' for disc rips
    is_disc         INTEGER NOT NULL DEFAULT 0,    -- 1 = BDMV folder structure
    file_size       INTEGER NOT NULL DEFAULT 0,    -- bytes (main file or disc total)
    file_modified   INTEGER NOT NULL DEFAULT 0,    -- mtime of main file / dir

    -- parsed from the release name
    release_group   TEXT NOT NULL DEFAULT '',
    edition         TEXT NOT NULL DEFAULT '',
    source          TEXT NOT NULL DEFAULT '',
    resolution      TEXT NOT NULL DEFAULT '',
    video_codec     TEXT NOT NULL DEFAULT '',
    audio_codec     TEXT NOT NULL DEFAULT '',
    audio_channels  TEXT NOT NULL DEFAULT '',
    hdr             TEXT NOT NULL DEFAULT '',
    dolby_vision   INTEGER NOT NULL DEFAULT 0,
    bit_depth       INTEGER NOT NULL DEFAULT 0,
    audio_count     INTEGER NOT NULL DEFAULT 0,
    language        TEXT NOT NULL DEFAULT '',
    is_collection   INTEGER NOT NULL DEFAULT 0,
    raw_name        TEXT NOT NULL DEFAULT '',      -- original dir/file name

    -- refined by ffprobe (0 = not probed / unknown)
    duration_sec    REAL NOT NULL DEFAULT 0,
    video_bitrate   INTEGER NOT NULL DEFAULT 0,
    frame_rate      REAL NOT NULL DEFAULT 0,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    container       TEXT NOT NULL DEFAULT '',

    nfo_path        TEXT NOT NULL DEFAULT '',
    scanned_at      INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER NOT NULL DEFAULT 0,

    FOREIGN KEY (movie_id)   REFERENCES movies(id)      ON DELETE SET NULL,
    FOREIGN KEY (library_id) REFERENCES libraries(id)   ON DELETE CASCADE
);

-- A single (library_id, dir_path) identifies a release uniquely.
CREATE UNIQUE INDEX idx_movie_files_release ON movie_files(library_id, dir_path);
CREATE INDEX idx_movie_files_movie ON movie_files(movie_id);
CREATE INDEX idx_movie_files_resolution ON movie_files(resolution);
CREATE INDEX idx_movie_files_source ON movie_files(source);
CREATE INDEX idx_movie_files_codec ON movie_files(video_codec);
CREATE INDEX idx_movie_files_hdr ON movie_files(hdr);

-- Audio tracks inside a movie_file (from ffprobe).
CREATE TABLE audio_tracks (
    id           INTEGER PRIMARY KEY,
    movie_file_id INTEGER NOT NULL,
    language     TEXT NOT NULL DEFAULT '',
    codec        TEXT NOT NULL DEFAULT '',
    channels     INTEGER NOT NULL DEFAULT 0,
    title        TEXT NOT NULL DEFAULT '',
    is_default   INTEGER NOT NULL DEFAULT 0,
    is_lossless  INTEGER NOT NULL DEFAULT 0,
    "order"      INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (movie_file_id) REFERENCES movie_files(id) ON DELETE CASCADE
);
CREATE INDEX idx_audio_tracks_file ON audio_tracks(movie_file_id);

-- Subtitles: external files (.srt/.ass next to the movie) and embedded streams.
CREATE TABLE subtitles (
    id            INTEGER PRIMARY KEY,
    movie_file_id INTEGER NOT NULL,
    file_path     TEXT NOT NULL DEFAULT '',  -- external file; '' if embedded
    language      TEXT NOT NULL DEFAULT '',
    format        TEXT NOT NULL DEFAULT '',  -- srt, ass, ssa, pgs, ...
    is_embedded   INTEGER NOT NULL DEFAULT 0,
    is_default    INTEGER NOT NULL DEFAULT 0,
    "order"       INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (movie_file_id) REFERENCES movie_files(id) ON DELETE CASCADE
);
CREATE INDEX idx_subtitles_file ON subtitles(movie_file_id);

-- Per-movie watch state for the current (single) user.
CREATE TABLE watch_status (
    movie_id      INTEGER PRIMARY KEY,
    status        TEXT NOT NULL DEFAULT 'unwatched'
                  CHECK (status IN ('unwatched','watching','watched')),
    progress      REAL NOT NULL DEFAULT 0,   -- 0..1
    last_played_at INTEGER NOT NULL DEFAULT 0,
    rating        INTEGER NOT NULL DEFAULT 0, -- 0 (none) or 1..10
    updated_at    INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (movie_id) REFERENCES movies(id) ON DELETE CASCADE
);

-- User-curated playlists (ordered movie sequences).
CREATE TABLE playlists (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE playlist_items (
    id          INTEGER PRIMARY KEY,
    playlist_id INTEGER NOT NULL,
    movie_id    INTEGER NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0,
    note        TEXT NOT NULL DEFAULT '',
    added_at    INTEGER NOT NULL DEFAULT 0,
    UNIQUE (playlist_id, movie_id),
    FOREIGN KEY (playlist_id) REFERENCES playlists(id) ON DELETE CASCADE,
    FOREIGN KEY (movie_id)    REFERENCES movies(id)    ON DELETE CASCADE
);
CREATE INDEX idx_playlist_items_playlist ON playlist_items(playlist_id, position);

-- User-curated collections (themed albums: "Kubrick", "Cyberpunk", ...).
CREATE TABLE user_collections (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    cover_path  TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE user_collection_items (
    id            INTEGER PRIMARY KEY,
    collection_id INTEGER NOT NULL,
    movie_id      INTEGER NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    note          TEXT NOT NULL DEFAULT '',
    added_at      INTEGER NOT NULL DEFAULT 0,
    UNIQUE (collection_id, movie_id),
    FOREIGN KEY (collection_id) REFERENCES user_collections(id) ON DELETE CASCADE,
    FOREIGN KEY (movie_id)      REFERENCES movies(id)           ON DELETE CASCADE
);
CREATE INDEX idx_uc_items_collection ON user_collection_items(collection_id, position);

-- TMDB response cache keyed by canonical endpoint path + query.
-- Allows offline operation and avoids re-fetching within the TTL.
CREATE TABLE tmdb_cache (
    endpoint   TEXT PRIMARY KEY,        -- e.g. "/movie/123?language=zh-CN"
    body_json  TEXT NOT NULL,
    fetched_at INTEGER NOT NULL DEFAULT 0
);

-- Scan job history for observability and incremental scheduling.
CREATE TABLE scan_jobs (
    id            INTEGER PRIMARY KEY,
    library_id    INTEGER NOT NULL,
    started_at    INTEGER NOT NULL DEFAULT 0,
    finished_at   INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'running'
                  CHECK (status IN ('running','completed','failed')),
    files_added   INTEGER NOT NULL DEFAULT 0,
    files_updated INTEGER NOT NULL DEFAULT 0,
    files_removed INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (library_id) REFERENCES libraries(id) ON DELETE CASCADE
);
CREATE INDEX idx_scan_jobs_library ON scan_jobs(library_id, started_at);
