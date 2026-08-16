package metadata

import (
	"context"
	"fmt"

	"github.com/moviegeek/darknight/internal/model"
)

// ApplyMatch commits an accepted TMDB match for m and returns the row id that
// ends up owning the files (which may differ from m.ID) plus whether a merge
// happened.
//
// The tmdb_id may already belong to another row - typically the enriched row a
// release was orphaned from when its parsed title failed to match (case,
// punctuation, romaji vs original). movies.tmdb_id is UNIQUE, so writing it
// onto m would fail with a constraint error; instead the files move onto the
// owner row and the duplicate shell is dropped.
//
// All three match entry points (scan, `darknight rematch`, POST
// /matches/rematch) must go through here, otherwise they hit the UNIQUE
// constraint and silently leave duplicates behind.
func (e *Enricher) ApplyMatch(ctx context.Context, m *model.Movie, tmdbID int64, score int) (int64, bool, error) {
	if tmdbID == 0 {
		return m.ID, false, fmt.Errorf("apply match: tmdb_id is zero")
	}

	// merge into the row that already owns this tmdb_id
	if owner, err := e.Store.FindMovieByTMDB(ctx, tmdbID); err == nil && owner.ID != m.ID {
		moved, err := e.Store.AttachMovieFiles(ctx, m.ID, owner.ID)
		if err != nil {
			return m.ID, false, fmt.Errorf("attach files to owner %d: %w", owner.ID, err)
		}
		if err := e.Store.DeleteMovieIfEmpty(ctx, m.ID); err != nil {
			e.Logger.Warn("drop merged seed row", "movie_id", m.ID, "err", err)
		}
		if err := e.Store.SetMovieMatch(ctx, owner.ID, tmdbID, model.MatchStatusMatched, score, ""); err != nil {
			e.Logger.Warn("set match on owner", "movie_id", owner.ID, "err", err)
		}
		e.Store.SetMatchCandidates(ctx, owner.ID, nil)
		e.Logger.Info("match merged into existing movie",
			"from", m.Title, "into", owner.Title, "movie_id", owner.ID,
			"tmdb_id", tmdbID, "files_moved", moved)
		return owner.ID, true, nil
	}

	// no conflict: enrich and stamp this row
	m.TMDBID = tmdbID
	if e.Enabled() {
		if _, err := e.EnrichMovie(ctx, m); err != nil {
			e.Logger.Warn("enrich after match", "movie", m.Title, "err", err)
		}
	}
	if err := e.Store.SetMovieMatch(ctx, m.ID, tmdbID, model.MatchStatusMatched, score, ""); err != nil {
		return m.ID, false, fmt.Errorf("set match: %w", err)
	}
	e.Store.SetMatchCandidates(ctx, m.ID, nil)
	return m.ID, false, nil
}
