// Package server wires the API, scanner, and (optionally) embedded frontend
// assets into a single http.Server.
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/moviegeek/darknight/internal/api"
	"github.com/moviegeek/darknight/internal/config"
	"github.com/moviegeek/darknight/web"
)

// New builds the root HTTP handler.
//
// Serving precedence for the SPA:
//  1. If cfg.StaticDir points at a real directory (dev mode), serve from disk.
//  2. Otherwise, if the embedded web/dist contains an index.html, serve from
//     the embedded FS (production single-binary mode).
//  3. Otherwise, do not serve any static assets (API-only mode).
func New(cfg *config.Config, apih *api.API, log *slog.Logger) http.Handler {
	r := chi.NewRouter()

	if len(cfg.CORSOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   cfg.CORSOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Content-Type"},
			AllowCredentials: false,
		}))
	}

	r.Route("/api", func(r chi.Router) {
		r.Mount("/", apih.Router())
	})

	if handler := pickStaticHandler(cfg, log); handler != nil {
		r.NotFound(handler)
	}

	return r
}

// pickStaticHandler chooses a static / SPA handler based on what's available.
func pickStaticHandler(cfg *config.Config, log *slog.Logger) http.HandlerFunc {
	// 1. explicit StaticDir (dev)
	if cfg.StaticDir != "" {
		if _, err := os.Stat(cfg.StaticDir); err == nil {
			log.Info("serving static assets from disk", "dir", cfg.StaticDir)
			return spaHandler(http.Dir(cfg.StaticDir), "")
		}
		log.Warn("static dir not found, SPA not served from disk", "dir", cfg.StaticDir)
	}

	// 2. embedded web/dist (production)
	sub, err := fs.Sub(web.Dist, "dist")
	if err == nil {
		if f, err := sub.Open("index.html"); err == nil {
			_ = f.Close()
			log.Info("serving embedded frontend assets")
			return spaHandler(http.FS(sub), "")
		}
	}
	return nil
}

// spaHandler serves files from root and falls back to index.html for any path
// that is not a real file, so client-side routes work on refresh. /api/* paths
// are left to return 404 (they are mounted explicitly above).
func spaHandler(root http.FileSystem, _ string) http.HandlerFunc {
	fileServer := http.FileServer(root)
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		clean := filepath.Clean(r.URL.Path)
		if clean == "/" {
			clean = "/index.html"
		}
		f, err := root.Open(clean)
		if err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}
