// Command darknight is the single binary of the moviegeek media library: it
// opens the SQLite database, runs migrations, exposes the REST API, serves the
// embedded web UI, and (optionally) scans libraries on start.
//
// Two one-shot maintenance subcommands are dispatched before the server starts:
//
//	darknight reenrich  re-run TMDB enrichment to backfill bilingual titles
//	darknight reprobe   re-probe files to backfill the cached ffprobe JSON
//
// Run with no subcommand (or an unknown one) to start the server as usual.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moviegeek/darknight/internal/api"
	"github.com/moviegeek/darknight/internal/config"
	"github.com/moviegeek/darknight/internal/ffprobe"
	"github.com/moviegeek/darknight/internal/metadata"
	"github.com/moviegeek/darknight/internal/scanner"
	"github.com/moviegeek/darknight/internal/server"
	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/tmdb"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subcommand dispatch: `reenrich` / `reprobe` run a one-shot maintenance
	// task instead of starting the server.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "reenrich":
			os.Exit(runReenrich(ctx, os.Args[2:]))
		case "reprobe":
			os.Exit(runReprobe(ctx, os.Args[2:]))
		}
	}

	rt, err := buildRuntime(ctx)
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stdout, nil)).Error("startup", "err", err)
		os.Exit(1)
	}
	defer rt.st.Close()

	apih := api.New(rt.st, rt.sc, rt.log, rt.enricher)
	handler := server.New(rt.cfg, apih, rt.log)

	if rt.cfg.ScanOnStart {
		go scanAll(ctx, rt.st, rt.sc, rt.log)
	}

	srv := &http.Server{
		Addr:              rt.cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// graceful shutdown on SIGINT / SIGTERM
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		rt.log.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
		cancel()
	}()

	rt.log.Info("listening", "addr", rt.cfg.HTTPAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		rt.log.Error("server", "err", err)
		os.Exit(1)
	}
}

// runtime bundles the long-lived dependencies built from config. Shared by the
// server path and the maintenance subcommands so they construct the store,
// scanner and enricher identically.
type runtime struct {
	cfg      *config.Config
	log      *slog.Logger
	st       *store.Store
	sc       *scanner.Scanner
	enricher *metadata.Enricher
}

// buildRuntime loads config, opens the store, runs migrations, and wires the
// scanner + TMDB enricher. The caller is responsible for closing st.
func buildRuntime(ctx context.Context) (*runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(cfg.LogLevel)}))
	slog.SetDefault(log)
	log.Info("config", "cfg", cfg.String())

	st, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	sc := scanner.New(st, log)
	// wire TMDB enrichment when an API key is configured; otherwise the app
	// runs in offline mode using only filename + .nfo metadata.
	tmdbClient := tmdb.New(cfg.TMDBAPIKey, cfg.TMDBLanguage, cfg.TMDBLanguageAlt)
	enricher := metadata.New(tmdbClient, st, log)
	if enricher.Enabled() {
		log.Info("tmdb enrichment enabled", "language", cfg.TMDBLanguage, "alt", cfg.TMDBLanguageAlt)
	} else {
		log.Info("tmdb enrichment disabled (set TMDB_API_KEY to enable)")
	}
	sc.Enricher = enricher
	return &runtime{cfg: cfg, log: log, st: st, sc: sc, enricher: enricher}, nil
}

// runReenrich re-runs TMDB enrichment for movies missing the bilingual /
// original-title fields (or every movie with --all), using the force path that
// bypasses the local TMDB cache. It is the one-shot backfill for movies
// enriched before bilingual titles landed.
func runReenrich(ctx context.Context, args []string) int {
	rt, err := buildRuntime(ctx)
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stdout, nil)).Error("startup", "err", err)
		return 1
	}
	defer rt.st.Close()
	if !rt.enricher.Enabled() {
		rt.log.Error("reenrich requires TMDB enrichment (set TMDB_API_KEY)")
		return 1
	}
	all := false
	for _, a := range args {
		if a == "--all" {
			all = true
		}
	}

	movies, err := rt.st.ListMovies(ctx, store.ListMoviesOpts{})
	if err != nil {
		rt.log.Error("list movies", "err", err)
		return 1
	}
	done, skipped, failed := 0, 0, 0
	for i := range movies {
		m := &movies[i]
		if !all && m.OriginalTitle != "" && m.OriginalLanguage != "" {
			skipped++
			continue
		}
		refreshed, err := rt.enricher.EnrichMovieForce(ctx, m)
		if err != nil {
			rt.log.Warn("reenrich", "title", m.Title, "err", err)
			failed++
			continue
		}
		done++
		rt.log.Info("reenriched", "title", m.Title, "tmdb_id", m.TMDBID, "refreshed", refreshed)
	}
	rt.log.Info("reenrich done", "total", len(movies), "enriched", done, "skipped", skipped, "failed", failed)
	return 0
}

// runReprobe re-probes file releases whose ffprobe JSON cache is missing or
// stale (captured under a different ProbeVersion) and writes the raw JSON back
// without touching the parsed/derived columns. It is the one-shot backfill for
// files scanned before the ffprobe cache existed.
func runReprobe(ctx context.Context, args []string) int {
	rt, err := buildRuntime(ctx)
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stdout, nil)).Error("startup", "err", err)
		return 1
	}
	defer rt.st.Close()

	// optional flags: --library ID limits to one library; --limit N processes
	// at most N files this run (incremental batching over a large library).
	var libraryID int64
	limit := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--library":
			if i+1 < len(args) {
				if id, err := strconv.ParseInt(args[i+1], 10, 64); err == nil {
					libraryID = id
				}
				i++
			}
		case "--limit":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil && n >= 0 {
					limit = n
				}
				i++
			}
		}
	}

	// resolve library roots so we can reconstruct absolute file paths.
	libs, err := rt.st.ListLibraries(ctx)
	if err != nil {
		rt.log.Error("list libraries", "err", err)
		return 1
	}
	roots := make(map[int64]string, len(libs))
	for _, l := range libs {
		roots[l.ID] = l.RootPath
	}

	files, err := rt.st.ListMovieFilesForReprobe(ctx, ffprobe.ProbeVersion)
	if err != nil {
		rt.log.Error("list files for reprobe", "err", err)
		return 1
	}
	rt.log.Info("reprobe", "files", len(files), "version", ffprobe.ProbeVersion,
		"library", libraryID, "limit", limit)
	done, failed := 0, 0
	for i := range files {
		mf := files[i]
		if libraryID != 0 && mf.LibraryID != libraryID {
			continue
		}
		root := roots[mf.LibraryID]
		if root == "" {
			rt.log.Warn("reprobe: unknown library", "movie_file_id", mf.ID, "library_id", mf.LibraryID)
			failed++
			continue
		}
		path := filepath.Join(root, mf.DirPath, mf.FileName)
		_, raw, err := ffprobe.Probe(ctx, path)
		if err != nil {
			rt.log.Warn("reprobe ffprobe", "file", path, "err", err)
			failed++
			continue
		}
		if err := rt.st.UpdateFFProbeCache(ctx, mf.ID, string(raw), ffprobe.ProbeVersion, time.Now().Unix()); err != nil {
			rt.log.Warn("reprobe update", "file", path, "err", err)
			failed++
			continue
		}
		done++
		if limit > 0 && done >= limit {
			break
		}
		if done%50 == 0 {
			rt.log.Info("reprobe progress", "done", done, "total", len(files))
		}
	}
	rt.log.Info("reprobe done", "total", len(files), "probed", done, "failed", failed)
	return 0
}

// parseLogLevel maps a config string ("debug"/"info"/"warn"/"error") to a
// slog.Level, defaulting to info for any unrecognised value.
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// scanAll scans every library once, sequentially. Used at startup when
// DARKNIGHT_SCAN_ON_START is set.
func scanAll(ctx context.Context, st *store.Store, sc *scanner.Scanner, log *slog.Logger) {
	libs, err := st.ListLibraries(ctx)
	if err != nil {
		log.Error("list libraries for startup scan", "err", err)
		return
	}
	for _, lib := range libs {
		stats, err := sc.ScanLibrary(ctx, &lib)
		if err != nil {
			log.Error("scan library", "library", lib.Name, "err", err)
			continue
		}
		log.Info("scan library done", "library", lib.Name,
			"added", stats.Added, "updated", stats.Updated,
			"unchanged", stats.Unchanged, "removed", stats.Removed, "errors", stats.Errors)
	}
}
