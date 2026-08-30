package traktsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/trakt"
)

const (
	accessToken  = "test-access-token"
	refreshToken = "test-refresh-token"
)

// fakeTrakt is a stub Trakt API with programmable watched data and call
// counters for the incremental-skip and refresh paths.
type fakeTrakt struct {
	client      *trakt.Client
	watched     atomic.Value // []trakt.WatchedMovie
	watchedAt   atomic.Value // string, movies.watched_at
	granted     atomic.Bool  // device flow approved?
	refreshed   atomic.Int32
	watchedReqs atomic.Int32
}

func newFakeTrakt(t *testing.T, watched []trakt.WatchedMovie, watchedAt string) *fakeTrakt {
	f := &fakeTrakt{}
	f.watched.Store(watched)
	f.watchedAt.Store(watchedAt)
	mux := http.NewServeMux()

	mux.HandleFunc("/oauth/device/code", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, trakt.DeviceCode{
			DeviceCode: "dc", UserCode: "AB12",
			VerificationURL: "https://trakt.tv/activate",
			ExpiresIn:       600, Interval: 1,
		})
	})
	mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
		if !f.granted.Load() {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(w, trakt.Token{AccessToken: accessToken, RefreshToken: refreshToken, ExpiresIn: 7776000})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		f.refreshed.Add(1)
		writeJSON(w, trakt.Token{AccessToken: "refreshed-at", RefreshToken: "refreshed-rt", ExpiresIn: 7776000})
	})
	mux.HandleFunc("/users/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+accessToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"user": map[string]any{"username": "moviegeek", "ids": map[string]string{"slug": "moviegeek"}},
		})
	})
	mux.HandleFunc("/sync/last_activities", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"movies": map[string]string{"watched_at": f.watchedAt.Load().(string)},
		})
	})
	mux.HandleFunc("/users/me/watched/movies", func(w http.ResponseWriter, r *http.Request) {
		f.watchedReqs.Add(1)
		tok := r.Header.Get("Authorization")
		if tok != "Bearer "+accessToken && tok != "Bearer refreshed-at" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, f.watched.Load().([]trakt.WatchedMovie))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	f.client = trakt.New("cid", "csecret", "urn:ietf:wg:oauth:2.0:oob")
	f.client.BaseURL = srv.URL
	return f
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newSyncer(t *testing.T, f *fakeTrakt) *Syncer {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, f.client, nil)
}

func seed(t *testing.T, s *Syncer, m model.Movie) int64 {
	t.Helper()
	if err := s.Store.UpsertMovieSeed(context.Background(), &m); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	var id int64
	var err error
	switch {
	case m.TMDBID != 0:
		err = s.Store.DB.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ?`, m.TMDBID).Scan(&id)
	case m.IMDBID != "":
		err = s.Store.DB.QueryRow(`SELECT id FROM movies WHERE imdb_id = ?`, m.IMDBID).Scan(&id)
	default:
		t.Fatal("seed movie needs tmdb or imdb id")
	}
	if err != nil {
		t.Fatalf("find movie: %v", err)
	}
	return id
}

// waitPollInterval sleeps past the fake's 1s device-poll interval: PollConnect
// throttles to Trakt's advertised interval, so back-to-back polls in one test
// must respect it just like the real frontend does.
func waitPollInterval() { time.Sleep(1100 * time.Millisecond) }

func TestSyncNotConnected(t *testing.T) {
	f := newFakeTrakt(t, nil, "2024-01-01T00:00:00Z")
	syncer := newSyncer(t, f)
	if _, err := syncer.Sync(context.Background()); err != ErrNotConnected {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
}

func TestSyncImportsWatched(t *testing.T) {
	now := time.Now().Unix()
	f := newFakeTrakt(t, []trakt.WatchedMovie{
		// matched by tmdb id, new
		{Plays: 1, LastWatchedAt: time.Unix(now-100, 0), Movie: trakt.Movie{Title: "New", Year: 2020, IDs: trakt.IDs{TMDB: 101}}},
		// matched by imdb id only (tmdb unknown to trakt)
		{Plays: 2, LastWatchedAt: time.Unix(now-200, 0), Movie: trakt.Movie{Title: "By Imdb", Year: 2021, IDs: trakt.IDs{IMDB: "tt9999"}}},
		// not in the library
		{Plays: 1, LastWatchedAt: time.Unix(now-300, 0), Movie: trakt.Movie{Title: "Not Owned", Year: 2022, IDs: trakt.IDs{TMDB: 999}}},
		// no last_watched_at at all
		{Plays: 1, Movie: trakt.Movie{Title: "No Time", Year: 2023, IDs: trakt.IDs{TMDB: 103}}},
		// local row newer than trakt's timestamp
		{Plays: 1, LastWatchedAt: time.Unix(now-10, 0), Movie: trakt.Movie{Title: "Ahead", IDs: trakt.IDs{TMDB: 104}}},
		// local row older than trakt's timestamp
		{Plays: 1, LastWatchedAt: time.Unix(now-10, 0), Movie: trakt.Movie{Title: "Behind", IDs: trakt.IDs{TMDB: 105}}},
	}, "2024-05-01T09:00:00Z")
	syncer := newSyncer(t, f)
	ctx := context.Background()

	idNew := seed(t, syncer, model.Movie{Title: "New", Year: 2020, TMDBID: 101})
	idIMDB := seed(t, syncer, model.Movie{Title: "By Imdb", Year: 2021, IMDBID: "tt9999"})
	idNoTime := seed(t, syncer, model.Movie{Title: "No Time", Year: 2023, TMDBID: 103})
	idAhead := seed(t, syncer, model.Movie{Title: "Ahead", Year: 2019, TMDBID: 104})
	idBehind := seed(t, syncer, model.Movie{Title: "Behind", Year: 2018, TMDBID: 105})

	// pre-existing local watched rows: one newer than trakt, one older
	if err := syncer.Store.MarkWatched(ctx, []store.WatchedMark{
		{MovieID: idAhead, LastPlayedAt: now},
		{MovieID: idBehind, LastPlayedAt: now - 9999},
	}); err != nil {
		t.Fatalf("pre-mark: %v", err)
	}

	// connect first
	if _, err := syncer.BeginConnect(ctx); err != nil {
		t.Fatalf("begin connect: %v", err)
	}
	f.granted.Store(true)
	if err := syncer.PollConnect(ctx); err != nil {
		t.Fatalf("poll connect: %v", err)
	}

	res, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Total != 6 || res.Matched != 5 || res.Unmatched != 1 {
		t.Fatalf("counts: %+v", res)
	}
	if res.NewlyWatched != 3 || res.AlreadyWatched != 1 || res.TimestampAdvanced != 1 {
		t.Fatalf("breakdown: %+v", res)
	}
	if len(res.UnmatchedTitles) != 1 || res.UnmatchedTitles[0] != "Not Owned" {
		t.Fatalf("unmatched titles: %v", res.UnmatchedTitles)
	}

	// verify persisted rows
	statuses, _ := syncer.Store.ListWatchStatus(ctx)
	if ws := statuses[idNew]; ws.Status != "watched" || ws.LastPlayedAt != now-100 {
		t.Fatalf("new row: %+v", ws)
	}
	if ws := statuses[idIMDB]; ws.Status != "watched" || ws.LastPlayedAt != now-200 {
		t.Fatalf("imdb row: %+v", ws)
	}
	if ws := statuses[idNoTime]; ws.Status != "watched" || ws.LastPlayedAt != 0 {
		t.Fatalf("no-time row: %+v", ws)
	}
	if ws := statuses[idAhead]; ws.LastPlayedAt != now {
		t.Fatalf("ahead row clobbered: %+v", ws)
	}
	if ws := statuses[idBehind]; ws.LastPlayedAt != now-10 {
		t.Fatalf("behind row not advanced: %+v", ws)
	}

	// state persisted for change detection
	st, _ := syncer.Store.GetTraktState(ctx)
	if st.RemoteWatchedAt != "2024-05-01T09:00:00Z" || st.LastSyncAt == 0 {
		t.Fatalf("sync state: %+v", st)
	}
	if st.Username != "moviegeek" {
		t.Fatalf("username: %q", st.Username)
	}
}

func TestSyncSkipsWhenNothingChanged(t *testing.T) {
	f := newFakeTrakt(t, []trakt.WatchedMovie{
		{Plays: 1, Movie: trakt.Movie{Title: "A", IDs: trakt.IDs{TMDB: 101}}},
	}, "2024-05-01T09:00:00Z")
	syncer := newSyncer(t, f)
	ctx := context.Background()

	// seed a completed sync whose result matched everything
	if err := syncer.Store.SaveTraktAuth(ctx, "u", accessToken, refreshToken, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Store.SaveTraktSync(ctx, "2024-05-01T09:00:00Z", `{"total":1,"unmatched":0}`, time.Now().Unix()-100); err != nil {
		t.Fatal(err)
	}

	res, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !res.Skipped {
		t.Fatalf("expected skip: %+v", res)
	}
	if got := f.watchedReqs.Load(); got != 0 {
		t.Fatalf("watched list should not be fetched on skip, got %d calls", got)
	}

	// a remote change un-skips it
	f.watchedAt.Store("2024-06-01T09:00:00Z")
	res, err = syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync after change: %v", err)
	}
	if res.Skipped || res.Total != 1 {
		t.Fatalf("expected full sync: %+v", res)
	}
	if got := f.watchedReqs.Load(); got != 1 {
		t.Fatalf("watched list fetches: %d", got)
	}
}

func TestSyncRerunsWhenPreviousRunHadUnmatched(t *testing.T) {
	// Remote unchanged, but the previous result had unmatched entries: local
	// library changes could still claim them, so no skip.
	f := newFakeTrakt(t, []trakt.WatchedMovie{
		{Plays: 1, Movie: trakt.Movie{Title: "A", IDs: trakt.IDs{TMDB: 101}}},
		{Plays: 1, Movie: trakt.Movie{Title: "Not Owned", IDs: trakt.IDs{TMDB: 999}}},
	}, "2024-05-01T09:00:00Z")
	syncer := newSyncer(t, f)
	ctx := context.Background()
	seed(t, syncer, model.Movie{Title: "A", Year: 2020, TMDBID: 101})

	if err := syncer.Store.SaveTraktAuth(ctx, "u", accessToken, refreshToken, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Store.SaveTraktSync(ctx, "2024-05-01T09:00:00Z", `{"total":2,"unmatched":1}`, time.Now().Unix()-100); err != nil {
		t.Fatal(err)
	}

	res, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Skipped {
		t.Fatalf("should not skip with prior unmatched entries: %+v", res)
	}
	if res.Total != 2 || res.Unmatched != 1 {
		t.Fatalf("result: %+v", res)
	}
}

func TestSyncRefreshesExpiredToken(t *testing.T) {
	f := newFakeTrakt(t, []trakt.WatchedMovie{
		{Plays: 1, Movie: trakt.Movie{Title: "A", IDs: trakt.IDs{TMDB: 101}}},
	}, "2024-05-01T09:00:00Z")
	syncer := newSyncer(t, f)
	ctx := context.Background()

	// stored token is already expired
	if err := syncer.Store.SaveTraktAuth(ctx, "u", "stale-at", "stale-rt", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	res, err := syncer.Sync(ctx)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("result: %+v", res)
	}
	if got := f.refreshed.Load(); got != 1 {
		t.Fatalf("expected one refresh, got %d", got)
	}
	// rotated pair persisted
	st, _ := syncer.Store.GetTraktState(ctx)
	if st.AccessToken != "refreshed-at" || st.RefreshToken != "refreshed-rt" {
		t.Fatalf("tokens not rotated: %+v", st)
	}
}

func TestConnectFlow(t *testing.T) {
	f := newFakeTrakt(t, nil, "2024-05-01T09:00:00Z")
	syncer := newSyncer(t, f)
	ctx := context.Background()

	if _, err := syncer.BeginConnect(ctx); err != nil {
		t.Fatalf("begin connect: %v", err)
	}

	// not approved yet -> pending
	if err := syncer.PollConnect(ctx); err != trakt.ErrDevicePending {
		t.Fatalf("want pending, got %v", err)
	}
	st, _ := syncer.Store.GetTraktState(ctx)
	if st.Connected() {
		t.Fatal("should not be connected before approval")
	}

	// approve -> next poll (past the interval) connects
	f.granted.Store(true)
	waitPollInterval()
	if err := syncer.PollConnect(ctx); err != nil {
		t.Fatalf("poll connect: %v", err)
	}
	st, _ = syncer.Store.GetTraktState(ctx)
	if !st.Connected() || st.Username != "moviegeek" {
		t.Fatalf("state after connect: %+v", st)
	}

	// no pending flow left
	if err := syncer.PollConnect(ctx); err != ErrNoPendingConnect {
		t.Fatalf("want ErrNoPendingConnect, got %v", err)
	}

	// disconnect clears auth but keeps history
	if err := syncer.Store.SaveTraktSync(ctx, "x", "{}", 5); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Disconnect(ctx); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	st, _ = syncer.Store.GetTraktState(ctx)
	if st.Connected() {
		t.Fatal("still connected after disconnect")
	}
	if st.LastSyncAt != 5 {
		t.Fatalf("history lost: %+v", st)
	}
}

func TestStatusReportsState(t *testing.T) {
	f := newFakeTrakt(t, nil, "2024-05-01T09:00:00Z")
	syncer := newSyncer(t, f)
	ctx := context.Background()

	// configured + fresh device flow -> status shows the pending code
	info, err := syncer.BeginConnect(ctx)
	if err != nil {
		t.Fatalf("begin connect: %v", err)
	}
	st, err := syncer.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Configured || st.Connected || st.Pending == nil || st.Pending.UserCode != info.UserCode {
		t.Fatalf("pending status: %+v", st)
	}

	// after approval, status drives the connect to completion (polling
	// respects the device interval, so wait past it)
	f.granted.Store(true)
	waitPollInterval()
	st, err = syncer.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !st.Connected || st.Username != "moviegeek" || st.Pending != nil {
		t.Fatalf("connected status: %+v", st)
	}

	// last result round-trips
	if err := syncer.Store.SaveTraktSync(ctx, "x",
		`{"total":7,"matched":6,"newly_watched":3}`, 42); err != nil {
		t.Fatal(err)
	}
	st, _ = syncer.Status(ctx)
	if st.LastSyncAt != 42 || st.LastResult == nil || st.LastResult.Total != 7 || st.LastResult.NewlyWatched != 3 {
		t.Fatalf("last result: %+v", st.LastResult)
	}
}

func TestDisabledSyncer(t *testing.T) {
	syncer := New(nil, trakt.New("", "", ""), nil)
	if syncer.Enabled() {
		t.Fatal("empty credentials should be disabled")
	}
	st, err := syncer.Status(context.Background())
	if err != nil {
		t.Fatalf("status on disabled syncer: %v", err)
	}
	if st.Configured {
		t.Fatal("status should report unconfigured")
	}
}
