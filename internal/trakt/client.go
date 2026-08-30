// Package trakt is a minimal client for the trakt.tv REST API.
//
// It is intentionally narrow: only what the watch-status sync needs is
// modelled - the OAuth 2 device authorization flow (the user activates at
// trakt.tv/activate, no redirect URI involved), token refresh, the user's
// watched-movie list, and /sync/last_activities. Access tokens are passed per
// call; persistence and refresh bookkeeping live with the caller
// (internal/traktsync + the trakt_state table).
//
// API docs: https://trakt.docs.apiary.io/
package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BaseURL is the API root.
const BaseURL = "https://api.trakt.tv"

// Device flow + refresh constants. Trakt's device codes live ~10 minutes and
// access tokens ~90 days.
const (
	deviceCodeTTL  = 10 * time.Minute
	tokenLeadTime  = 5 * time.Minute // refresh this long before expiry
	defaultRefresh = 90 * 24 * time.Hour
)

var (
	// ErrNoClient is returned when the client has no app credentials
	// configured. Callers should treat this as "trakt disabled" and skip.
	ErrNoClient = errors.New("trakt: no client credentials configured")

	// ErrDevicePending means the user has not approved the device code yet;
	// the caller should keep polling at the advertised interval.
	ErrDevicePending = errors.New("trakt: authorization pending")

	// ErrDeviceExpired means the device code (or the grant behind it) is no
	// longer usable and the authorization flow must be restarted.
	ErrDeviceExpired = errors.New("trakt: device code expired or invalid")

	// ErrUnauthorized means the access token was rejected - expired or
	// revoked. The caller should try a refresh or re-connect.
	ErrUnauthorized = errors.New("trakt: unauthorized")

	// ErrNotFound mirrors 404s on data endpoints.
	ErrNotFound = errors.New("trakt: not found")
)

// Client is a trakt.tv HTTP client. The zero value is not usable - construct
// one with New.
type Client struct {
	ClientID     string
	ClientSecret string
	// RedirectURI is sent on token refresh, where Trakt requires the URI
	// registered in the app settings. Device-flow apps have no real redirect;
	// the out-of-band placeholder is the documented stand-in.
	RedirectURI string
	BaseURL     string // override for testing; defaults to BaseURL
	HTTP        *http.Client
	// Logger, when set, receives debug traces of each API request. Nil =
	// silent (the default for tests).
	Logger *slog.Logger
}

// New returns a ready client. Empty clientID/secret yields a client whose
// methods return ErrNoClient - safe to call, no-op sync.
func New(clientID, clientSecret, redirectURI string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURI:  redirectURI,
		BaseURL:      BaseURL,
		HTTP:         &http.Client{Timeout: 15 * time.Second},
	}
}

// Enabled reports whether Trakt credentials are configured.
func (c *Client) Enabled() bool { return c != nil && c.ClientID != "" }

func (c *Client) debug(msg string, args ...any) {
	if c.Logger != nil {
		c.Logger.Debug(msg, args...)
	}
}

// ---------- OAuth device flow ----------

// DeviceCode is the response of POST /oauth/device/code: the pair shown to the
// user (user_code + verification_url) and the device_code exchanged for tokens
// once the user approves.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"` // seconds
	Interval        int    `json:"interval"`   // minimum poll interval, seconds
}

// ExpiresAt returns when the device code stops being usable.
func (d *DeviceCode) ExpiresAt() time.Time {
	ttl := deviceCodeTTL
	if d.ExpiresIn > 0 {
		ttl = time.Duration(d.ExpiresIn) * time.Second
	}
	return time.Now().Add(ttl)
}

// PollInterval returns the minimum delay between token polls.
func (d *DeviceCode) PollInterval() time.Duration {
	if d.Interval > 0 {
		return time.Duration(d.Interval) * time.Second
	}
	return 5 * time.Second
}

// Token is an OAuth access + refresh token pair. Refresh tokens are
// single-use: each refresh returns a new one that replaces the old.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"` // seconds, ~90 days
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"` // unix seconds
}

// ExpiresAt returns the absolute expiry computed from the server timestamps,
// falling back to now + ExpiresIn when CreatedAt is missing.
func (t *Token) ExpiresAt() int64 {
	if t.CreatedAt > 0 {
		return t.CreatedAt + int64(t.ExpiresIn)
	}
	return time.Now().Add(defaultRefresh).Unix()
}

// DeviceCode starts the device authorization flow.
func (c *Client) DeviceCode(ctx context.Context) (*DeviceCode, error) {
	var dc DeviceCode
	err := c.postJSON(ctx, "/oauth/device/code",
		map[string]string{"client_id": c.ClientID}, &dc)
	if err != nil {
		return nil, err
	}
	return &dc, nil
}

// DeviceToken polls POST /oauth/device/token for the pending device code. It
// returns ErrDevicePending until the user approves, and ErrDeviceExpired when
// the code (or grant) can no longer be used.
//
// Trakt's 400 responses here are unreliable - the body may not be valid JSON
// (the official playground poller works around the same thing), so pending vs.
// terminal states are classified by status code first and body contents only
// when they parse.
func (c *Client) DeviceToken(ctx context.Context, deviceCode string) (*Token, error) {
	status, _, body, err := c.do(ctx, http.MethodPost, "/oauth/device/token", "",
		map[string]string{
			"code":          deviceCode,
			"client_id":     c.ClientID,
			"client_secret": c.ClientSecret,
		})
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusOK:
		var tok Token
		if err := json.Unmarshal(body, &tok); err != nil {
			return nil, fmt.Errorf("trakt: decode device token: %w", err)
		}
		return &tok, nil
	case status == http.StatusNotFound:
		return nil, ErrDeviceExpired
	case status == http.StatusBadRequest:
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		// "expired_token" / "access_denied" / "invalid_grant" are terminal;
		// anything else (or an unparseable body) means "keep polling".
		for _, terminal := range []string{"expired", "denied", "invalid"} {
			if strings.Contains(e.Error, terminal) {
				return nil, ErrDeviceExpired
			}
		}
		return nil, ErrDevicePending
	default:
		return nil, statusErr(status, body)
	}
}

// RefreshToken exchanges a refresh token for a new token pair. The old
// refresh token is consumed; the caller must persist the new one.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*Token, error) {
	var tok Token
	err := c.postJSON(ctx, "/oauth/token", map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     c.ClientID,
		"client_secret": c.ClientSecret,
		"redirect_uri":  c.RedirectURI,
	}, &tok)
	if err != nil {
		return nil, err
	}
	return &tok, nil
}

// ---------- user data ----------

// IDs are the cross-service identifiers Trakt attaches to every movie. imdb
// and tmdb can be absent (null) on Trakt's side.
type IDs struct {
	Trakt int64  `json:"trakt"`
	Slug  string `json:"slug"`
	IMDB  string `json:"imdb"`
	TMDB  int64  `json:"tmdb"`
}

// Movie is the movie summary embedded in sync responses.
type Movie struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   IDs    `json:"ids"`
}

// WatchedMovie is one entry of the user's watched-movie list.
type WatchedMovie struct {
	Plays         int       `json:"plays"`
	LastWatchedAt time.Time `json:"last_watched_at"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
	Movie         Movie     `json:"movie"`
}

// LastWatchedUnix returns the last watch time as unix seconds, 0 when unknown.
func (w *WatchedMovie) LastWatchedUnix() int64 {
	if w.LastWatchedAt.IsZero() {
		return 0
	}
	return w.LastWatchedAt.Unix()
}

// WatchedMovies fetches the full paginated watched-movie list of the
// authorized user. Trakt caps the page size below the requested limit (250
// for this endpoint as of 2026), so the walk must use the server-reported
// X-Pagination-Limit as the effective page size and X-Pagination-Page-Count
// as the stop signal; a short page relative to the effective limit is the
// fallback when the headers are absent.
func (c *Client) WatchedMovies(ctx context.Context, accessToken string) ([]WatchedMovie, error) {
	const requested = 1000 // per-page ask; the server may lower it
	var all []WatchedMovie
	effective := requested
	for page := 1; page <= 100; page++ {
		var items []WatchedMovie
		path := "/users/me/watched/movies?page=" + strconv.Itoa(page) +
			"&limit=" + strconv.Itoa(requested)
		hdr, err := c.get(ctx, path, accessToken, &items)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)

		// The limit the server actually applied - the requested one may have
		// been capped, and comparing item counts against the request would
		// end the walk after one page.
		if v := hdr.Get("X-Pagination-Limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				effective = n
			}
		}
		if v := hdr.Get("X-Pagination-Page-Count"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && page >= n {
				break
			}
		}
		if len(items) < effective {
			break
		}
	}
	return all, nil
}

// LastActivities is the subset of /sync/last_activities we care about: the
// timestamp of the last change to the user's watched movies. One request is
// enough to know whether a sync has anything to do.
type LastActivities struct {
	Movies struct {
		WatchedAt time.Time `json:"watched_at"`
	} `json:"movies"`
}

// WatchedAtRFC3339 returns the movies.watched_at timestamp formatted for
// stable storage/compare (millisecond noise dropped).
func (la *LastActivities) WatchedAtRFC3339() string {
	if la.Movies.WatchedAt.IsZero() {
		return ""
	}
	return la.Movies.WatchedAt.UTC().Format(time.RFC3339)
}

// LastActivities fetches /sync/last_activities.
func (c *Client) LastActivities(ctx context.Context, accessToken string) (*LastActivities, error) {
	var la LastActivities
	if _, err := c.get(ctx, "/sync/last_activities", accessToken, &la); err != nil {
		return nil, err
	}
	return &la, nil
}

// User is the account summary returned by /users/settings.
type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	IDs      struct {
		Slug string `json:"slug"`
	} `json:"ids"`
}

// Handle returns the best display handle: username, falling back to the slug.
func (u *User) Handle() string {
	if u.Username != "" {
		return u.Username
	}
	return u.IDs.Slug
}

// CurrentUser fetches the authorized user's account summary (used once at
// connect time to remember whose history we are syncing).
func (c *Client) CurrentUser(ctx context.Context, accessToken string) (*User, error) {
	var resp struct {
		User User `json:"user"`
	}
	if _, err := c.get(ctx, "/users/settings", accessToken, &resp); err != nil {
		return nil, err
	}
	return &resp.User, nil
}

// ---------- transport ----------

// get performs an authorized GET, decoding v, and returns the response
// headers (pagination bookkeeping lives there).
func (c *Client) get(ctx context.Context, path, accessToken string, v interface{}) (http.Header, error) {
	status, hdr, body, err := c.do(ctx, http.MethodGet, path, accessToken, nil)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, statusErr(status, body)
	}
	if err := decode(body, path, v); err != nil {
		return nil, err
	}
	return hdr, nil
}

// postJSON performs an unauthenticated POST with a JSON body, decoding v.
func (c *Client) postJSON(ctx context.Context, path string, body, v interface{}) error {
	status, _, respBody, err := c.do(ctx, http.MethodPost, path, "", body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return statusErr(status, respBody)
	}
	return decode(respBody, path, v)
}

// do is the raw transport: it sends the trakt headers plus an optional bearer
// token and returns status, headers and body. Only transport-level failures
// (network, context) produce errors; HTTP statuses are the caller's business.
func (c *Client) do(ctx context.Context, method, path, accessToken string, body interface{}) (int, http.Header, []byte, error) {
	if !c.Enabled() {
		return 0, nil, nil, ErrNoClient
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.ClientID)
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	c.debug("trakt: response", "path", path, "status", resp.StatusCode, "bytes", len(respBody))
	return resp.StatusCode, resp.Header, respBody, nil
}

// statusErr maps a non-2xx response to an error, surfacing Trakt's
// {"error": "..."} message when present.
func statusErr(status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	}
	var e struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &e)
	msg := strings.TrimSpace(e.Error)
	if e.ErrorDescription != "" {
		if msg != "" {
			msg += ": "
		}
		msg += e.ErrorDescription
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		return fmt.Errorf("trakt: http %d", status)
	}
	return fmt.Errorf("trakt: http %d: %s", status, msg)
}

func decode(body []byte, path string, v interface{}) error {
	if v == nil {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("trakt: decode %s: %w", path, err)
	}
	return nil
}
