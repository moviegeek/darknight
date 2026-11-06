package store_test

import (
	"context"
	"testing"

	"github.com/moviegeek/darknight/internal/model"
	"github.com/moviegeek/darknight/internal/store"
)

func TestAddListDeleteWatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	id := seedMovie(t, s, model.Movie{Title: "Watch Me", Year: 2020, TMDBID: 9001})

	p1, err := s.AddWatch(ctx, id, 1_700_000_000, false)
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	p2, err := s.AddWatch(ctx, id, 1_700_100_000, true)
	if err != nil {
		t.Fatalf("add second: %v", err)
	}

	plays, err := s.ListWatchHistory(ctx, id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plays) != 2 {
		t.Fatalf("plays = %d, want 2", len(plays))
	}
	if plays[0].ID != p2.ID || !plays[0].HasTime {
		t.Fatalf("newest play = %+v, want id=%d has_time", plays[0], p2.ID)
	}
	if plays[1].ID != p1.ID || plays[1].HasTime {
		t.Fatalf("older play = %+v, want id=%d date-only", plays[1], p1.ID)
	}

	ws, err := s.GetWatchStatus(ctx, id)
	if err != nil || ws == nil || ws.Status != "watched" || ws.LastPlayedAt != 1_700_100_000 {
		t.Fatalf("status = %+v err=%v", ws, err)
	}

	// Default delete removes the most recent play.
	if err := s.DeleteWatch(ctx, id, 0); err != nil {
		t.Fatalf("delete latest: %v", err)
	}
	plays, _ = s.ListWatchHistory(ctx, id)
	if len(plays) != 1 || plays[0].ID != p1.ID {
		t.Fatalf("after default delete: %+v", plays)
	}
	ws, _ = s.GetWatchStatus(ctx, id)
	if ws == nil || ws.LastPlayedAt != 1_700_000_000 {
		t.Fatalf("status after partial delete: %+v", ws)
	}

	if err := s.DeleteWatch(ctx, id, p1.ID); err != nil {
		t.Fatalf("delete last: %v", err)
	}
	plays, _ = s.ListWatchHistory(ctx, id)
	if len(plays) != 0 {
		t.Fatalf("want empty history, got %+v", plays)
	}
	ws, _ = s.GetWatchStatus(ctx, id)
	if ws != nil {
		t.Fatalf("want status cleared, got %+v", ws)
	}
}

func TestListWatchHistoryStatusOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	id := seedMovie(t, s, model.Movie{Title: "Trakt Only", Year: 2021, TMDBID: 9002})

	if err := s.MarkWatched(ctx, []store.WatchedMark{{MovieID: id, LastPlayedAt: 99}}); err != nil {
		t.Fatalf("mark: %v", err)
	}
	plays, err := s.ListWatchHistory(ctx, id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plays) != 1 || plays[0].ID != 0 || plays[0].Source != "status" || plays[0].WatchedAt != 99 {
		t.Fatalf("synthetic play = %+v", plays)
	}
	if err := s.DeleteWatch(ctx, id, 0); err != nil {
		t.Fatalf("clear status: %v", err)
	}
	plays, _ = s.ListWatchHistory(ctx, id)
	if len(plays) != 0 {
		t.Fatalf("want empty after clear, got %+v", plays)
	}
}
