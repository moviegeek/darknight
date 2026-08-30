# darknight

A self-hosted movie library manager. Scans local movie files on disk, parses
release-name + `.nfo` + `ffprobe` metadata into SQLite, and serves a web UI for
browsing, filtering, and comparing your collection.

Single binary, single database file, no external services.

## Features

- **Deep file scanning** — every release is parsed for: title, year, source
  (BluRay / UHD BluRay / WebDL / …), resolution (720p / 1080p / 2160p), video
  codec (x264 / x265 / AVC / HEVC), audio codec + channels (DTS-HD MA / TrueHD
  / Atmos / DD+ / FLAC …), HDR / Dolby Vision, bit depth, edition (Director's
  Cut, Remastered, IMAX, …), release group, language, and collection flag.
  `ffprobe` then fills the real container-level info: duration, bitrate, frame
  rate, exact dimensions, multi-audio tracks, embedded + external subtitles.
- **Both file and disc releases** — single `.mkv`/`.mp4` and full Blu-ray
  folder rips (`BDMV/STREAM/*.m2ts`) are detected and stored.
- **Incremental scans** — a release whose main file size + mtime + duration is
  unchanged is skipped (no `ffprobe` re-run), so rescans are cheap. The raw
  `ffprobe` JSON is cached per file, so future media-info fields can be
  backfilled from it without re-probing; a `ProbeVersion` knob invalidates the
  cache when the probe flags change.
- **TMDB enrichment** (optional) — when `TMDB_API_KEY` is set, `.nfo` ids or
  parsed `(title, year)` are matched against TMDB for posters, backdrops,
  synopsis, cast, ratings. The primary fetch is `en-US`; the original title and
  original language always come back regardless of locale, and a secondary
  locale (`zh-CN` by default) supplies the localized title for bilingual
  display. Without a key the app runs in offline mode using only filename +
  `.nfo` info.
- **Bilingual titles** — the original title is shown as the primary line; a
  secondary line shows the "other" language: Chinese for English-original
  films, English for Chinese-original films, or both for anything else. Country
  is stored as an ISO 3166-1 code and the filter matches on it.
- **Filter / browse UI** — a dark, dense web frontend with a multi-dimension
  filter panel (resolution / source / codec / HDR / Dolby Vision / country /
  subtitle / watched state / search), quick chips, and a movie detail page
  that compares every physical version of a film side by side.
- **Trakt watch-status sync** (optional) - when `TRAKT_CLIENT_ID` /
  `TRAKT_CLIENT_SECRET` are set (create an API app at
  trakt.tv/oauth/applications), connect your trakt.tv account once via the
  OAuth device flow (设置 -> 连接 Trakt) and import your watched history into
  the library's watched filter. The sync is one-way and additive: Trakt can
  mark a local movie watched (and advance `last_played_at`), but never
  un-watches one or touches progress/rating. Movies are matched by TMDB id,
  then IMDb id; films watched on Trakt but absent from the library are
  reported as unmatched and skipped.

## Architecture

```
darknight/                     Go module (github.com/moviegeek/darknight)
├── cmd/darknight/             entrypoint: config → store → scanner → http
├── internal/
│   ├── config/                env-based config
│   ├── model/                 plain domain structs (mirror the SQL schema)
│   ├── parser/                release-name parser (table-driven, tested)
│   ├── nfo/                   .nfo (XML) parser
│   ├── ffprobe/               ffprobe JSON client
│   ├── scanner/               dir walk + classify + upsert (incremental)
│   ├── store/                 SQLite (modernc, pure Go) + migrations + queries
│   ├── trakt/                 trakt.tv REST client (OAuth device flow + watched list)
│   ├── traktsync/             one-way watch-status import (Trakt -> watch_status)
│   ├── api/                   chi REST handlers (/api/*)
│   └── server/                router + embedded SPA serving
└── web/                       frontend (Vite + React + TS + Tailwind)
    ├── embed.go               //go:embed dist → exposes web.Dist
    └── src/                   pages, components, api client
```

A single `go build` produces one binary that embeds the compiled frontend; the
SQLite database is created next to it on first run. No Node, no Postgres, no
Docker required at runtime — only `ffmpeg`/`ffprobe` on PATH for scanning.

## Quick start

### Prerequisites

- Go 1.22+
- Node 18+ (only for building the frontend)
- `ffmpeg` / `ffprobe` on PATH (for media probing)

### Build & run

```bash
make build                    # builds web/dist then bin/darknight
./bin/darknight               # serves on :8080, db at ./.data/darknight.db
```

Open http://localhost:8080, go to **设置**, add a media library root path
(e.g. `/Volumes/Media/Films`), and click **扫描**.

### Dev mode (hot-reload frontend, separate Go server)

```bash
# terminal 1: Go backend on :8080
make dev

# terminal 2: Vite dev server on :5173 (proxies /api → :8080)
make frontend-dev
```

Open http://localhost:5173.

## Configuration

On startup the server first loads a `.env` file (if present) and applies its
`KEY=VALUE` pairs as defaults, then reads the real environment variables,
which take precedence. The `.env` file is looked for in the current working
directory or next to the executable; set `DARKNIGHT_ENV_FILE` to point at a
specific file instead. A missing file is silently ignored.

All via environment variables:

| Var | Default | Purpose |
|---|---|---|
| `DARKNIGHT_ENV_FILE` | _(empty)_ | explicit `.env` file path (else `./.env` or next to the binary) |
| `DARKNIGHT_DB` | `.data/darknight.db` | SQLite database path |
| `DARKNIGHT_ADDR` | `:8080` | HTTP listen address |
| `TMDB_API_KEY` | _(empty)_ | enables TMDB metadata enrichment |
| `TMDB_LANGUAGE` | `en-US` | primary locale for TMDB text (overview, localized title) |
| `TMDB_LANGUAGE_ALT` | `zh-CN` | secondary locale for the bilingual title fetch |
| `TRAKT_CLIENT_ID` | _(empty)_ | trakt.tv OAuth app client id (enables Trakt sync together with the secret) |
| `TRAKT_CLIENT_SECRET` | _(empty)_ | trakt.tv OAuth app client secret |
| `TRAKT_REDIRECT_URI` | `urn:ietf:wg:oauth:2.0:oob` | URI sent on Trakt token refresh; must match the app settings |
| `DARKNIGHT_STATIC_DIR` | _(empty)_ | serve SPA from disk (dev) instead of embed |
| `DARKNIGHT_CORS_ORIGINS` | _(empty)_ | comma-separated allowed origins |
| `DARKNIGHT_SCAN_ON_START` | `false` | scan all libraries on boot |
| `DARKNIGHT_LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error`; `debug` traces each release + TMDB resolution |

## REST API

All routes are under `/api`.

```
GET    /health
GET    /libraries
POST   /libraries                  {name, root_path, scan_interval}
GET    /libraries/{id}
DELETE /libraries/{id}
POST   /libraries/{id}/scan        # async

GET    /movies?q=&resolution=&source=&codec=&hdr=&dolby_vision=&country=&subtitle_lang=&external_subtitle=&watched=&sort=&desc=&limit=&offset=
GET    /movies/{id}
GET    /movies/{id}/files
GET    /movies/{id}/files/{fid}    # includes audio_tracks + subtitles
GET    /collections

POST   /trakt/connect              # start OAuth device flow -> {user_code, verification_url}
GET    /trakt/status               # connection + last-sync state; polls a pending connect
POST   /trakt/sync                 # one watch-status import -> statistics
POST   /trakt/disconnect           # forget stored tokens
```

## Maintenance

Two one-shot subcommands backfill data for libraries scanned before a feature
landed. They build the same store/scanner/enricher as the server and exit when
done; run the binary with no subcommand to start the server as usual.

```bash
darknight reenrich          # re-run TMDB enrichment for movies missing
                            # original_title / original_language (add --all
                            # to re-enrich every movie); bypasses the local
                            # TMDB cache
darknight reprobe           # re-probe file releases whose cached ffprobe JSON
                            # is missing or stale and write the raw JSON back
                            # without touching the parsed/derived columns
                            # (--library ID narrows to one library; --limit N
                            #  processes at most N files for incremental batching)
```

### Bilingual title display

The original title is the primary line (falling back to the English then
Chinese localized title). The secondary line follows the original language:

| Original language | Secondary line |
|---|---|
| English | Chinese title |
| Chinese | English title |
| other | English · Chinese |

Chinese-original films reuse the original title as the Chinese title (no extra
fetch). A secondary value equal to the primary is omitted.

### ffprobe JSON cache

Each file release stores the verbatim `ffprobe` JSON alongside the parsed
columns, tagged with a `ProbeVersion`. New media-info fields can be derived
from the cached JSON without re-probing. Bump `ffprobe.ProbeVersion` when the
probe flags change; the incremental scan then re-probes stale files, and
`darknight reprobe` backfills the rest.

## Testing

```bash
go test ./...                 # backend
cd web && npm run build       # frontend type-check + build
```

The parser package includes a smoke test that runs over every top-level
directory name in `media.txt` (the real library tree) to assert no panics and
plausible output.

## What's not done (roadmap)

- Playlists / user collections CRUD (nav placeholder present)
- Watch-status / rating write API
- In-browser streaming playback (HTTP Range)
- Dashboard stats
