// Trakt watch-status sync endpoints. All handlers live in this file so the
// feature stays self-contained; Router() in api.go mounts them as
// /api/trakt/*.
package api

import (
	"errors"
	"net/http"

	"github.com/moviegeek/darknight/internal/traktsync"
)

// traktSyncer returns the syncer when Trakt credentials are configured.
func (a *API) traktSyncer() *traktsync.Syncer {
	if a.TraktSync == nil || !a.TraktSync.Enabled() {
		return nil
	}
	return a.TraktSync
}

// traktConnect starts the OAuth device flow and returns the code + activation
// URL to show the user.
func (a *API) traktConnect(w http.ResponseWriter, r *http.Request) {
	s := a.traktSyncer()
	if s == nil {
		writeError(w, http.StatusServiceUnavailable,
			"trakt not configured (set TRAKT_CLIENT_ID / TRAKT_CLIENT_SECRET)")
		return
	}
	info, err := s.BeginConnect(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// traktStatus reports connection + last-sync state. It also advances a
// pending device flow (polling Trakt once per interval), so the frontend
// drives the connect to completion by polling this endpoint.
func (a *API) traktStatus(w http.ResponseWriter, r *http.Request) {
	if a.TraktSync == nil {
		writeJSON(w, http.StatusOK, traktsync.Status{})
		return
	}
	st, err := a.TraktSync.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// traktSync runs one watch-status import and returns the statistics.
func (a *API) traktSync(w http.ResponseWriter, r *http.Request) {
	s := a.traktSyncer()
	if s == nil {
		writeError(w, http.StatusServiceUnavailable,
			"trakt not configured (set TRAKT_CLIENT_ID / TRAKT_CLIENT_SECRET)")
		return
	}
	res, err := s.Sync(r.Context())
	if errors.Is(err, traktsync.ErrNotConnected) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// traktDisconnect forgets the stored OAuth pair.
func (a *API) traktDisconnect(w http.ResponseWriter, r *http.Request) {
	s := a.traktSyncer()
	if s == nil {
		writeError(w, http.StatusServiceUnavailable,
			"trakt not configured (set TRAKT_CLIENT_ID / TRAKT_CLIENT_SECRET)")
		return
	}
	if err := s.Disconnect(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
