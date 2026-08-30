// Package config loads runtime configuration from environment variables with
// sensible defaults for a single-machine self-hosted deployment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime knobs.
type Config struct {
	// DatabasePath is the SQLite database file location.
	DatabasePath string
	// HTTPAddr is the address the HTTP server listens on.
	HTTPAddr string
	// TMDBAPIKey enables TMDB metadata enrichment when non-empty. Without it
	// the app runs in offline mode using only filename + .nfo info.
	TMDBAPIKey string
	// TMDBLanguage is the preferred locale for TMDB text (overview, titles).
	// This is the primary fetch language; the original title + original
	// language come back regardless of locale.
	TMDBLanguage string
	// TMDBLanguageAlt is the secondary locale used to fetch the localized
	// title for the bilingual display (e.g. the Chinese title when the
	// primary is English). Defaults to zh-CN.
	TMDBLanguageAlt string
	// TraktClientID and TraktClientSecret are the trakt.tv OAuth app
	// credentials (create one at https://trakt.tv/oauth/applications). Both
	// must be set to enable the Trakt watch-status sync.
	TraktClientID     string
	TraktClientSecret string
	// TraktRedirectURI is sent on Trakt token refresh, which requires the URI
	// registered in the app settings. The device flow has no real redirect,
	// so the out-of-band placeholder is the default.
	TraktRedirectURI string
	// StaticDir is the directory served for the SPA. Empty = no static
	// serving (used in dev where the frontend runs separately).
	StaticDir string
	// CORSOrigins, when set, enables CORS for cross-origin dev setups.
	CORSOrigins []string
	// ScanOnStart runs a scan of every library when the server boots.
	ScanOnStart bool
	// LogLevel controls slog verbosity: "debug", "info", "warn" or "error".
	// "debug" surfaces the per-release scan + TMDB resolution trace.
	LogLevel string
}

// Load reads configuration from environment variables. Before reading them it
// loads a .env file (if present) and applies its values as defaults, so real
// environment variables still take precedence. The .env file is searched for
// in the current directory or next to the executable; an explicit location can
// be set with DARKNIGHT_ENV_FILE.
func Load() (*Config, error) {
	for _, p := range envFileLocations(os.Getenv("DARKNIGHT_ENV_FILE")) {
		if err := loadDotEnv(p); err != nil {
			return nil, fmt.Errorf("load env file %s: %w", p, err)
		}
	}
	c := &Config{
		DatabasePath:      envStr("DARKNIGHT_DB", ".data/darknight.db"),
		HTTPAddr:          envStr("DARKNIGHT_ADDR", ":8080"),
		TMDBAPIKey:        envStr("TMDB_API_KEY", ""),
		TMDBLanguage:      envStr("TMDB_LANGUAGE", "en-US"),
		TMDBLanguageAlt:   envStr("TMDB_LANGUAGE_ALT", "zh-CN"),
		TraktClientID:     envStr("TRAKT_CLIENT_ID", ""),
		TraktClientSecret: envStr("TRAKT_CLIENT_SECRET", ""),
		TraktRedirectURI:  envStr("TRAKT_REDIRECT_URI", "urn:ietf:wg:oauth:2.0:oob"),
		StaticDir:         envStr("DARKNIGHT_STATIC_DIR", ""),
		ScanOnStart:       envBool("DARKNIGHT_SCAN_ON_START", false),
		CORSOrigins:       envList("DARKNIGHT_CORS_ORIGINS"),
		LogLevel:          envStr("DARKNIGHT_LOG_LEVEL", "info"),
	}
	return c, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envList(key string) []string {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// String is used for at-startup logging.
func (c *Config) String() string {
	return fmt.Sprintf("db=%s addr=%s tmdb=%v lang=%s/%s trakt=%v static=%q scanOnStart=%v log=%s",
		c.DatabasePath, c.HTTPAddr, c.TMDBAPIKey != "", c.TMDBLanguage, c.TMDBLanguageAlt,
		c.TraktClientID != "" && c.TraktClientSecret != "",
		c.StaticDir, c.ScanOnStart, c.LogLevel)
}
