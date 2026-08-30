// Package traktsync imports trakt.tv watch status into the local library.
//
// The sync is one-way (Trakt -> watch_status) and additive: a Trakt entry can
// mark a local movie watched and advance its last_played_at, but never
// un-watches a movie and never touches progress or rating. Movies watched on
// Trakt but absent from the library are counted as unmatched and ignored -
// this is a library manager, not a Trakt mirror.
//
// Matching is id-exact: tmdb_id first, then imdb_id. The OAuth device flow
// runs between BeginConnect and PollConnect; its single-use device code lives
// in memory only.
package traktsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/moviegeek/darknight/internal/store"
	"github.com/moviegeek/darknight/internal/trakt"
)

// maxUnmatchedTitles caps the sample of unmatched titles kept in the result.
const maxUnmatchedTitles = 50

var (
	// ErrNotConnected is returned by Sync when no OAuth pair is stored.
	ErrNotConnected = errors.New("trakt 未连接")

	// ErrNoPendingConnect is returned by PollConnect when no device flow is
	// in flight. Not an error the UI needs to surface.
	ErrNoPendingConnect = errors.New("trakt: no pending connect")
)

// Syncer orchestrates the Trakt integration: OAuth connect, and the
// watch-status import. Safe for concurrent use.
type Syncer struct {
	Store  *store.Store
	Client *trakt.Client
	Logger *slog.Logger

	// pending is the in-flight device-flow authorization between
	// BeginConnect and a successful PollConnect. Device codes are single-use
	// and short-lived, so memory is the right scope.
	mu      sync.Mutex
	pending *pendingConnect
}

type pendingConnect struct {
	deviceCode string
	info       ConnectInfo
	interval   time.Duration
	expiresAt  time.Time
	lastPollAt time.Time
}

// New returns a ready Syncer. A nil client or missing credentials yield a
// syncer whose methods report disabled - callers hide the feature then.
func New(st *store.Store, cl *trakt.Client, log *slog.Logger) *Syncer {
	if log == nil {
		log = slog.Default()
	}
	return &Syncer{Store: st, Client: cl, Logger: log}
}

// Enabled reports whether Trakt credentials are configured.
func (s *Syncer) Enabled() bool { return s != nil && s.Client.Enabled() }

// ConnectInfo is what the UI shows during the device flow: the code to enter
// and the page to enter it on.
type ConnectInfo struct {
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresAt       int64  `json:"expires_at"` // unix seconds
	Interval        int    `json:"interval"`   // poll interval, seconds
}

// BeginConnect starts the OAuth device flow: it asks Trakt for a device code
// and remembers it for PollConnect. Any previous pending flow is replaced.
func (s *Syncer) BeginConnect(ctx context.Context) (*ConnectInfo, error) {
	dc, err := s.Client.DeviceCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("trakt device code: %w", err)
	}
	info := ConnectInfo{
		UserCode:        dc.UserCode,
		VerificationURL: dc.VerificationURL,
		ExpiresAt:       dc.ExpiresAt().Unix(),
		Interval:        int(dc.PollInterval().Seconds()),
	}
	s.mu.Lock()
	s.pending = &pendingConnect{
		deviceCode: dc.DeviceCode,
		info:       info,
		interval:   dc.PollInterval(),
		expiresAt:  dc.ExpiresAt(),
	}
	s.mu.Unlock()
	return &info, nil
}

// PollConnect exchanges the pending device code for tokens once the user has
// approved it at the activation page. Returns ErrDevicePending until then.
// The device code is single-use, so polling happens at most once per Trakt's
// advertised interval; a terminal error clears the pending flow.
func (s *Syncer) PollConnect(ctx context.Context) error {
	s.mu.Lock()
	p := s.pending
	if p != nil && time.Since(p.lastPollAt) < p.interval {
		s.mu.Unlock()
		return trakt.ErrDevicePending
	}
	if p != nil {
		p.lastPollAt = time.Now()
	}
	s.mu.Unlock()
	if p == nil {
		return ErrNoPendingConnect
	}
	if time.Now().After(p.expiresAt) {
		s.clearPending()
		return trakt.ErrDeviceExpired
	}

	tok, err := s.Client.DeviceToken(ctx, p.deviceCode)
	if errors.Is(err, trakt.ErrDevicePending) {
		return err
	}
	s.clearPending()
	if err != nil {
		return err
	}

	// remember whose history we are syncing (best effort - /users/settings
	// failing must not fail the connect)
	username := ""
	if user, uerr := s.Client.CurrentUser(ctx, tok.AccessToken); uerr == nil && user != nil {
		username = user.Handle()
	}
	if err := s.Store.SaveTraktAuth(ctx, username, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt()); err != nil {
		return err
	}
	s.Logger.Info("trakt connected", "username", username)
	return nil
}

func (s *Syncer) clearPending() {
	s.mu.Lock()
	s.pending = nil
	s.mu.Unlock()
}

// Result is the outcome of one sync run, persisted as JSON for the settings UI.
type Result struct {
	Total             int      `json:"total"`              // watched entries on Trakt
	Matched           int      `json:"matched"`            // matched a local movie
	NewlyWatched      int      `json:"newly_watched"`      // rows written (new or upgraded)
	TimestampAdvanced int      `json:"timestamp_advanced"` // existing watched rows moved forward
	AlreadyWatched    int      `json:"already_watched"`    // already watched, nothing to do
	Unmatched         int      `json:"unmatched"`          // on Trakt, not in the library
	UnmatchedTitles   []string `json:"unmatched_titles,omitempty"`
	Skipped           bool     `json:"skipped"` // no remote change since last sync
	At                int64    `json:"at"`      // unix seconds
}

// Status is the /api/trakt/status payload.
type Status struct {
	Configured bool         `json:"configured"`
	Connected  bool         `json:"connected"`
	Username   string       `json:"username"`
	Pending    *ConnectInfo `json:"pending,omitempty"`
	LastSyncAt int64        `json:"last_sync_at"`
	LastResult *Result      `json:"last_result,omitempty"`
}

// Sync runs one watch-status import. When /sync/last_activities reports no
// change to watched movies since the last successful run, it returns early
// with Skipped set.
func (s *Syncer) Sync(ctx context.Context) (*Result, error) {
	st, err := s.Store.GetTraktState(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Connected() {
		return nil, ErrNotConnected
	}
	accessToken, err := s.accessToken(ctx, st)
	if err != nil {
		return nil, err
	}

	la, err := s.Client.LastActivities(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("trakt last activities: %w", err)
	}
	res := &Result{At: time.Now().Unix()}
	// Skip only when the remote watched set is unchanged AND the last run
	// matched every entry it saw. With unmatched entries around, local
	// library changes (new or newly-matched movies) could still claim them,
	// so a full re-run is the safe default.
	if st.LastSyncAt > 0 && la.WatchedAtRFC3339() == st.RemoteWatchedAt && prevAllMatched(st.LastSyncResult) {
		res.Skipped = true
		if err := s.persist(ctx, la, res); err != nil {
			return nil, err
		}
		s.Logger.Debug("trakt sync skipped", "remote_watched_at", st.RemoteWatchedAt)
		return res, nil
	}

	watched, err := s.Client.WatchedMovies(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("trakt watched movies: %w", err)
	}
	res.Total = len(watched)

	byTMDB, byIMDB, err := s.Store.MovieIDByExternalID(ctx)
	if err != nil {
		return nil, err
	}
	statuses, err := s.Store.ListWatchStatus(ctx)
	if err != nil {
		return nil, err
	}

	var marks []store.WatchedMark
	for i := range watched {
		w := &watched[i]
		id := matchMovie(w, byTMDB, byIMDB)
		if id == 0 {
			res.Unmatched++
			if len(res.UnmatchedTitles) < maxUnmatchedTitles && w.Movie.Title != "" {
				res.UnmatchedTitles = append(res.UnmatchedTitles, w.Movie.Title)
			}
			continue
		}
		res.Matched++

		ts := w.LastWatchedUnix()
		if cur, ok := statuses[id]; ok && cur.Status == "watched" {
			if ts > cur.LastPlayedAt {
				res.TimestampAdvanced++
			} else {
				res.AlreadyWatched++
				continue
			}
		} else {
			res.NewlyWatched++
		}
		marks = append(marks, store.WatchedMark{MovieID: id, LastPlayedAt: ts})
	}

	if err := s.Store.MarkWatched(ctx, marks); err != nil {
		return nil, err
	}
	if err := s.persist(ctx, la, res); err != nil {
		return nil, err
	}

	s.Logger.Info("trakt sync done",
		"total", res.Total, "matched", res.Matched,
		"newly_watched", res.NewlyWatched, "timestamp_advanced", res.TimestampAdvanced,
		"already_watched", res.AlreadyWatched, "unmatched", res.Unmatched)
	return res, nil
}

// matchMovie resolves a Trakt watched entry to a local movie id: tmdb_id
// first, then imdb_id. 0 = not in the library.
func matchMovie(w *trakt.WatchedMovie, byTMDB map[int64]int64, byIMDB map[string]int64) int64 {
	if id := byTMDB[w.Movie.IDs.TMDB]; id != 0 {
		return id
	}
	if w.Movie.IDs.IMDB != "" {
		return byIMDB[strings.ToLower(w.Movie.IDs.IMDB)]
	}
	return 0
}

// prevAllMatched reports whether the previous result JSON shows every Trakt
// entry matched a local movie. A missing or unparseable result counts as
// not-all-matched so the conservative path is a full sync.
func prevAllMatched(resultJSON string) bool {
	if resultJSON == "" {
		return false
	}
	var r Result
	if err := json.Unmarshal([]byte(resultJSON), &r); err != nil {
		return false
	}
	return r.Unmatched == 0
}

// persist records the change-detection snapshot and the result summary.
func (s *Syncer) persist(ctx context.Context, la *trakt.LastActivities, res *Result) error {
	resJSON, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return s.Store.SaveTraktSync(ctx, la.WatchedAtRFC3339(), string(resJSON), res.At)
}

// accessToken returns a usable access token, refreshing first when the stored
// one is at or near expiry. Refresh tokens are single-use; both columns are
// replaced together.
func (s *Syncer) accessToken(ctx context.Context, st *store.TraktState) (string, error) {
	if st.TokenExpiresAt == 0 || st.TokenExpiresAt > time.Now().Add(5*time.Minute).Unix() {
		return st.AccessToken, nil
	}
	s.Logger.Info("trakt token expired, refreshing")
	tok, err := s.Client.RefreshToken(ctx, st.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("trakt token 刷新失败，请断开后重新连接: %w", err)
	}
	if err := s.Store.SaveTraktAuth(ctx, st.Username, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt()); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// Disconnect forgets the stored OAuth pair. Sync history stays for reference.
func (s *Syncer) Disconnect(ctx context.Context) error {
	s.clearPending()
	if err := s.Store.ClearTraktAuth(ctx); err != nil {
		return err
	}
	s.Logger.Info("trakt disconnected")
	return nil
}

// Status reports the connection and last-sync state. When a device flow is
// pending it first polls Trakt once (respecting the interval), so a client
// that keeps calling Status drives the connect to completion.
func (s *Syncer) Status(ctx context.Context) (*Status, error) {
	out := &Status{Configured: s.Enabled()}
	if !out.Configured {
		return out, nil
	}
	if err := s.PollConnect(ctx); err != nil && !errors.Is(err, trakt.ErrDevicePending) &&
		!errors.Is(err, ErrNoPendingConnect) {
		s.Logger.Warn("trakt poll connect", "err", err)
	}

	st, err := s.Store.GetTraktState(ctx)
	if err != nil {
		return nil, err
	}
	out.Connected = st.Connected()
	out.Username = st.Username
	out.LastSyncAt = st.LastSyncAt
	if st.LastSyncResult != "" {
		var r Result
		if err := json.Unmarshal([]byte(st.LastSyncResult), &r); err == nil {
			out.LastResult = &r
		}
	}
	s.mu.Lock()
	if s.pending != nil {
		p := s.pending.info
		out.Pending = &p
	}
	s.mu.Unlock()
	return out, nil
}
