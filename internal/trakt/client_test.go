package trakt

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestClient wires a Client at a stub server. The returned closure yields
// the last request seen, for header assertions after a call.
func newTestClient(t *testing.T, mux *http.ServeMux) (*Client, func() *http.Request) {
	t.Helper()
	var last *http.Request
	wrap := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = r
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(wrap)
	t.Cleanup(srv.Close)
	c := New("cid", "csecret", "urn:ietf:wg:oauth:2.0:oob")
	c.BaseURL = srv.URL
	return c, func() *http.Request { return last }
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestEnabled(t *testing.T) {
	if New("", "", "").Enabled() {
		t.Fatal("empty credentials should be disabled")
	}
	if !New("cid", "", "").Enabled() {
		t.Fatal("client id alone should count as enabled")
	}
}

func TestDeviceCode(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/device/code", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, DeviceCode{
			DeviceCode: "dc123", UserCode: "ABC123",
			VerificationURL: "https://trakt.tv/activate",
			ExpiresIn:       600, Interval: 5,
		})
	})
	c, last := newTestClient(t, mux)

	dc, err := c.DeviceCode(context.Background())
	if err != nil {
		t.Fatalf("DeviceCode: %v", err)
	}
	if dc.DeviceCode != "dc123" || dc.UserCode != "ABC123" {
		t.Fatalf("bad device code: %+v", dc)
	}
	if got := last().Header.Get("trakt-api-key"); got != "cid" {
		t.Fatal("trakt-api-key header missing")
	}
	if dc.ExpiresAt().Before(time.Now()) {
		t.Fatal("device code should expire in the future")
	}
	if dc.PollInterval() != 5*time.Second {
		t.Fatalf("poll interval: %v", dc.PollInterval())
	}
}

func TestDeviceToken(t *testing.T) {
	t.Run("granted", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["code"] != "dc123" || req["client_secret"] != "csecret" {
				t.Errorf("unexpected request body: %v", req)
			}
			writeJSON(w, Token{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 7776000})
		})
		c, _ := newTestClient(t, mux)

		tok, err := c.DeviceToken(context.Background(), "dc123")
		if err != nil {
			t.Fatalf("DeviceToken: %v", err)
		}
		if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
			t.Fatalf("bad token: %+v", tok)
		}
		if tok.ExpiresAt() <= time.Now().Unix() {
			t.Fatal("token should expire in the future")
		}
	})

	t.Run("pending without json body", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest) // Trakt sometimes sends no body here
		})
		c, _ := newTestClient(t, mux)

		if _, err := c.DeviceToken(context.Background(), "dc123"); err != ErrDevicePending {
			t.Fatalf("want ErrDevicePending, got %v", err)
		}
	})

	t.Run("pending with rfc error code", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "authorization_pending"})
		})
		c, _ := newTestClient(t, mux)

		if _, err := c.DeviceToken(context.Background(), "dc123"); err != ErrDevicePending {
			t.Fatalf("want ErrDevicePending, got %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]string{"error": "expired_token"})
		})
		c, _ := newTestClient(t, mux)

		if _, err := c.DeviceToken(context.Background(), "dc123"); err != ErrDeviceExpired {
			t.Fatalf("want ErrDeviceExpired, got %v", err)
		}
	})

	t.Run("unknown device code", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
		c, _ := newTestClient(t, mux)

		if _, err := c.DeviceToken(context.Background(), "nope"); err != ErrDeviceExpired {
			t.Fatalf("want ErrDeviceExpired, got %v", err)
		}
	})
}

func TestRefreshToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["grant_type"] != "refresh_token" || req["refresh_token"] != "old-rt" {
			t.Errorf("unexpected request body: %v", req)
		}
		if req["redirect_uri"] != "urn:ietf:wg:oauth:2.0:oob" {
			t.Errorf("redirect_uri missing: %v", req)
		}
		writeJSON(w, Token{AccessToken: "at2", RefreshToken: "rt2", ExpiresIn: 7776000, CreatedAt: time.Now().Unix()})
	})
	c, _ := newTestClient(t, mux)

	tok, err := c.RefreshToken(context.Background(), "old-rt")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if tok.RefreshToken != "rt2" {
		t.Fatalf("refresh token not rotated: %+v", tok)
	}
}

func TestWatchedMoviesPagination(t *testing.T) {
	// Real Trakt behaviour (verified against the live API): the endpoint caps
	// the page size at 250 regardless of the requested limit and reports the
	// applied limit + page count in headers. The walk must follow those
	// numbers, not the request - otherwise it stops after the first page.
	const cap = 250
	mux := http.NewServeMux()
	var full []WatchedMovie
	for i := 0; i < cap*2+3; i++ {
		full = append(full, WatchedMovie{Plays: 1, Movie: Movie{Title: "A", IDs: IDs{Trakt: int64(i)}}})
	}
	pages := map[string][]WatchedMovie{
		"1": full[:cap],
		"2": full[cap : cap*2],
		"3": full[cap*2:],
	}
	mux.HandleFunc("/users/me/watched/movies", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			t.Errorf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("limit") != "1000" {
			t.Errorf("expected limit=1000, got %q", r.URL.Query().Get("limit"))
		}
		items, ok := pages[r.URL.Query().Get("page")]
		if !ok {
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("X-Pagination-Limit", "250")
		w.Header().Set("X-Pagination-Page-Count", "3")
		writeJSON(w, items)
	})
	c, _ := newTestClient(t, mux)

	items, err := c.WatchedMovies(context.Background(), "at")
	if err != nil {
		t.Fatalf("WatchedMovies: %v", err)
	}
	if len(items) != len(full) {
		t.Fatalf("expected %d items across pages, got %d", len(full), len(items))
	}
}

func TestWatchedMoviesLastWatchedAt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/watched/movies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []WatchedMovie{{
			Plays:         3,
			LastWatchedAt: time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC),
			Movie:         Movie{Title: "The Dark Knight", Year: 2008, IDs: IDs{Trakt: 4, Slug: "the-dark-knight", IMDB: "tt0468569", TMDB: 155}},
		}})
	})
	c, _ := newTestClient(t, mux)

	items, err := c.WatchedMovies(context.Background(), "at")
	if err != nil {
		t.Fatalf("WatchedMovies: %v", err)
	}
	last := items[0]
	if last.Movie.IDs.TMDB != 155 || last.Movie.IDs.IMDB != "tt0468569" {
		t.Fatalf("bad item: %+v", last.Movie.IDs)
	}
	if last.LastWatchedUnix() != time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("bad last_watched_at: %v", last.LastWatchedAt)
	}
}

func TestWatchedMoviesStopsOnShortPage(t *testing.T) {
	// No X-Pagination-Page-Count header at all: the short-page break must
	// terminate the walk.
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/watched/movies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []WatchedMovie{{Movie: Movie{Title: "A"}}})
	})
	c, _ := newTestClient(t, mux)

	items, err := c.WatchedMovies(context.Background(), "at")
	if err != nil {
		t.Fatalf("WatchedMovies: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestLastActivities(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sync/last_activities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"all": "2024-05-01T10:00:00.000Z",
			"movies": map[string]string{
				"watched_at": "2024-05-01T09:00:00.000Z",
				"rated_at":   "2024-05-01T08:00:00.000Z",
			},
			"shows": map[string]string{"watched_at": "2024-04-01T08:00:00.000Z"},
		})
	})
	c, _ := newTestClient(t, mux)

	la, err := c.LastActivities(context.Background(), "at")
	if err != nil {
		t.Fatalf("LastActivities: %v", err)
	}
	want := "2024-05-01T09:00:00Z"
	if got := la.WatchedAtRFC3339(); got != want {
		t.Fatalf("watched_at: got %q want %q", got, want)
	}
}

func TestCurrentUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"user": map[string]interface{}{
				"username": "moviegeek",
				"name":     "Movie Geek",
				"ids":      map[string]string{"slug": "moviegeek"},
			},
		})
	})
	c, _ := newTestClient(t, mux)

	u, err := c.CurrentUser(context.Background(), "at")
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	if u.Handle() != "moviegeek" {
		t.Fatalf("handle: %q", u.Handle())
	}
}

func TestNoCredentials(t *testing.T) {
	c := New("", "", "")
	if _, err := c.DeviceCode(context.Background()); err != ErrNoClient {
		t.Fatalf("want ErrNoClient, got %v", err)
	}
	if _, err := c.WatchedMovies(context.Background(), "at"); err != ErrNoClient {
		t.Fatalf("want ErrNoClient, got %v", err)
	}
}

func TestStatusErr(t *testing.T) {
	if err := statusErr(http.StatusUnauthorized, nil); err != ErrUnauthorized {
		t.Fatalf("401: %v", err)
	}
	if err := statusErr(http.StatusNotFound, nil); err != ErrNotFound {
		t.Fatalf("404: %v", err)
	}
	err := statusErr(http.StatusForbidden, []byte(`{"error":"access_denied","error_description":"nope"}`))
	if err == nil || err.Error() != "trakt: http 403: access_denied: nope" {
		t.Fatalf("403 message: %v", err)
	}
}
