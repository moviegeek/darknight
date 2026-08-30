package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
)

func seedMovie(t *testing.T, s *store.Store, m model.Movie) int64 {
	t.Helper()
	if err := s.UpsertMovieSeed(context.Background(), &m); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	var id int64
	var err error
	switch {
	case m.TMDBID != 0:
		err = s.DB.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ?`, m.TMDBID).Scan(&id)
	case m.IMDBID != "":
		err = s.DB.QueryRow(`SELECT id FROM movies WHERE imdb_id = ?`, m.IMDBID).Scan(&id)
	default:
		t.Fatal("seed movie needs tmdb or imdb id")
	}
	if err != nil {
		t.Fatalf("find seeded movie: %v", err)
	}
	return id
}

func TestTraktStateRoundtrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	st, err := s.GetTraktState(ctx)
	if err != nil {
		t.Fatalf("initial state: %v", err)
	}
	if st.Connected() {
		t.Fatal("fresh state should not be connected")
	}

	if err := s.SaveTraktAuth(ctx, "moviegeek", "at1", "rt1", 42); err != nil {
		t.Fatalf("save auth: %v", err)
	}
	st, _ = s.GetTraktState(ctx)
	if !st.Connected() || st.Username != "moviegeek" || st.RefreshToken != "rt1" || st.TokenExpiresAt != 42 {
		t.Fatalf("auth not stored: %+v", st)
	}

	if err := s.SaveTraktSync(ctx, "2024-05-01T09:00:00Z", `{"total":3}`, 100); err != nil {
		t.Fatalf("save sync: %v", err)
	}
	st, _ = s.GetTraktState(ctx)
	if st.RemoteWatchedAt != "2024-05-01T09:00:00Z" || st.LastSyncAt != 100 || st.LastSyncResult != `{"total":3}` {
		t.Fatalf("sync state not stored: %+v", st)
	}

	if err := s.ClearTraktAuth(ctx); err != nil {
		t.Fatalf("clear auth: %v", err)
	}
	st, _ = s.GetTraktState(ctx)
	if st.Connected() {
		t.Fatal("cleared state should not be connected")
	}
	// sync history survives a disconnect
	if st.LastSyncAt != 100 {
		t.Fatalf("sync history lost on disconnect: %+v", st)
	}
}

func TestMarkWatchedUnionSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	idNew := seedMovie(t, s, model.Movie{Title: "New Film", Year: 2020, TMDBID: 101})
	idFresh := seedMovie(t, s, model.Movie{Title: "Fresh Watch", Year: 2021, TMDBID: 102})
	idAhead := seedMovie(t, s, model.Movie{Title: "Ahead Locally", Year: 2022, TMDBID: 103})
	idRated := seedMovie(t, s, model.Movie{Title: "Rated Locally", Year: 2023, TMDBID: 104})

	// pre-existing local state: a watched row with a NEWER timestamp, a
	// watched row with an OLDER timestamp, and a watched+rated row
	now := timeNow()
	if err := s.MarkWatched(ctx, []store.WatchedMark{
		{MovieID: idAhead, LastPlayedAt: now},
		{MovieID: idRated, LastPlayedAt: now - 1000},
	}); err != nil {
		t.Fatalf("pre-seed watch rows: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE watch_status SET rating = 9 WHERE movie_id = ?`, idRated); err != nil {
		t.Fatalf("set rating: %v", err)
	}

	if err := s.MarkWatched(ctx, []store.WatchedMark{
		{MovieID: idNew, LastPlayedAt: now - 50},
		{MovieID: idAhead, LastPlayedAt: now - 100},  // older than local
		{MovieID: idRated, LastPlayedAt: now - 2000}, // older than local
	}); err != nil {
		t.Fatalf("mark watched: %v", err)
	}

	statuses, err := s.ListWatchStatus(ctx)
	if err != nil {
		t.Fatalf("list watch status: %v", err)
	}

	// new row inserted as watched
	if ws := statuses[idNew]; ws.Status != "watched" || ws.LastPlayedAt != now-50 {
		t.Fatalf("new movie: %+v", ws)
	}
	// local newer timestamp survives an older trakt one
	if ws := statuses[idAhead]; ws.LastPlayedAt != now {
		t.Fatalf("newer local timestamp clobbered: %+v", ws)
	}
	// rating is never touched by the sync
	if ws := statuses[idRated]; ws.Rating != 9 {
		t.Fatalf("rating clobbered: %+v", ws)
	}
	// idFresh untouched (no mark)
	if _, ok := statuses[idFresh]; ok {
		t.Fatal("unmarked movie should have no watch row")
	}
}

func TestMarkWatchedUpgradesWatching(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := seedMovie(t, s, model.Movie{Title: "In Progress", Year: 2020, TMDBID: 201})

	if _, err := s.DB.Exec(`
		INSERT INTO watch_status (movie_id, status, progress, last_played_at, updated_at)
		VALUES (?, 'watching', 0.4, 111, 111)`, id); err != nil {
		t.Fatalf("seed watching row: %v", err)
	}

	if err := s.MarkWatched(ctx, []store.WatchedMark{{MovieID: id, LastPlayedAt: 222}}); err != nil {
		t.Fatalf("mark watched: %v", err)
	}

	var status string
	var progress float64
	var lastPlayed int64
	if err := s.DB.QueryRow(`
		SELECT status, progress, last_played_at FROM watch_status WHERE movie_id = ?`, id).
		Scan(&status, &progress, &lastPlayed); err != nil {
		t.Fatalf("read watch row: %v", err)
	}
	if status != "watched" || progress != 0.4 || lastPlayed != 222 {
		t.Fatalf("watching not upgraded: status=%s progress=%v last=%d", status, progress, lastPlayed)
	}
}

func TestMovieIDByExternalID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	idTMDB := seedMovie(t, s, model.Movie{Title: "By TMDB", Year: 2001, TMDBID: 301})
	idIMDB := seedMovie(t, s, model.Movie{Title: "By IMDB Only", Year: 2002, IMDBID: "tt9999"})
	if _, err := s.DB.Exec(`INSERT INTO movies (title, sort_title, year) VALUES ('No IDs', 'no ids', 2003)`); err != nil {
		t.Fatalf("seed no-id movie: %v", err)
	}

	byTMDB, byIMDB, err := s.MovieIDByExternalID(ctx)
	if err != nil {
		t.Fatalf("external ids: %v", err)
	}
	if byTMDB[301] != idTMDB {
		t.Fatalf("tmdb lookup: %v", byTMDB[301])
	}
	if byIMDB["tt9999"] != idIMDB {
		t.Fatalf("imdb lookup: %v", byIMDB["tt9999"])
	}
	if len(byTMDB) != 1 || len(byIMDB) != 1 {
		t.Fatalf("unexpected lut sizes: %d/%d", len(byTMDB), len(byIMDB))
	}
}

// timeNow keeps the timestamp source in one place so tests compare consistently.
func timeNow() int64 { return time.Now().Unix() }
