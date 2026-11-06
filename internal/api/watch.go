package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/moviegeek/darknight/internal/store"
)

type addWatchReq struct {
	WatchedAt int64 `json:"watched_at"` // unix seconds; required
	HasTime   bool  `json:"has_time"`   // true when a clock time was supplied
}

// listWatches returns every viewing of a movie (newest first). Status-only
// Trakt marks appear as a single synthetic entry with id=0.
func (a *API) listWatches(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := a.Store.GetMovie(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "movie not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	plays, err := a.Store.ListWatchHistory(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plays)
}

// addWatch records a manual viewing and marks the movie watched.
func (a *API) addWatch(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req addWatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.WatchedAt <= 0 {
		writeError(w, http.StatusBadRequest, "watched_at required")
		return
	}
	if _, err := a.Store.GetMovie(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "movie not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	play, err := a.Store.AddWatch(r.Context(), id, req.WatchedAt, req.HasTime)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, play)
}

// deleteWatch removes one viewing. Path param wid=0 (or "latest") deletes the
// most recent play; otherwise the specific history id is removed.
func (a *API) deleteWatch(w http.ResponseWriter, r *http.Request) {
	movieID, err := parseInt64(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	widRaw := chi.URLParam(r, "wid")
	var playID int64
	if widRaw == "" || widRaw == "latest" {
		playID = 0
	} else {
		playID, err = parseInt64(r, "wid")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := a.Store.DeleteWatch(r.Context(), movieID, playID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "watch not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
