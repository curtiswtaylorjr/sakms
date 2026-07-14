package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/curtiswtaylorjr/sakms/internal/trakt"
)

// This file is self-contained by design (task #9): every handler here is a
// plain, already-package-local `api` function, so wiring it into the real
// mux (internal/api/handler.go, owned by task #5) is a one-line
// mux.HandleFunc call with no import needed — same package, no apidto
// dependency either (this file's request/response shapes are local structs
// mirroring the existing ConnectionTestRequest/Result convention; apidto
// mirrors them separately for TS codegen, same as every other connection
// type already does). See this package's connections.go/handler.go for the
// precedent this file follows.
//
// Every handler below builds its own *trakt.Client per request from
// whatever credentials are currently in trakt.Store, rather than holding a
// long-lived *trakt.Client/*trakt.Session — client_id/secret can change at
// any time via traktSaveCredentialsHandler, and a stale cached Client would
// silently keep using the old pair.

// testTrakt is TestConnection's "trakt" case content — mirrors
// testTMDB/testOllama/etc.'s shape exactly (same ConnectionTestResult
// return type, already defined in connections.go). Trakt's
// ConnectionTestRequest has no dedicated client_id field, so by convention
// the existing generic APIKey field carries client_id here (client_secret
// isn't needed — Ping only validates client_id against a public,
// non-OAuth endpoint). baseURL is threaded through explicitly (rather than
// reaching for trakt.DefaultBaseURL internally) so tests can point it at a
// fake server; production wiring passes trakt.DefaultBaseURL.
func testTrakt(ctx context.Context, httpClient *http.Client, baseURL, clientID string) ConnectionTestResult {
	c := trakt.New(trakt.Config{BaseURL: baseURL, ClientID: clientID}, httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

// traktCredentialsRequest is PUT /api/connections/trakt's body — same
// three-state ClientSecret convention as upsertConnectionRequest.APIKey in
// handler.go (nil = preserve stored secret, "" = clear, non-empty = set).
type traktCredentialsRequest struct {
	ClientID     string  `json:"clientId"`
	ClientSecret *string `json:"clientSecret,omitempty"`
}

// traktSaveCredentialsHandler persists the operator-entered Trakt
// application (client_id/client_secret). Doesn't touch any linked account's
// tokens — see trakt.Store.SaveCredentials.
func traktSaveCredentialsHandler(store *trakt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req traktCredentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ClientID == "" {
			http.Error(w, "clientId is required", http.StatusBadRequest)
			return
		}
		if err := store.SaveCredentials(r.Context(), req.ClientID, req.ClientSecret); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// traktConnectionSummary is GET /api/connections/trakt's response — safe to
// expose in full: ClientID is not secret (Trakt sends it as a plain header
// on every request), but ClientSecret/AccessToken/RefreshToken never appear
// here, only HasClientSecret. Mirrors connections.Summary's
// never-round-trip-the-real-secret convention.
type traktConnectionSummary struct {
	Configured      bool   `json:"configured"`
	Linked          bool   `json:"linked"`
	ClientID        string `json:"clientId,omitempty"`
	HasClientSecret bool   `json:"hasClientSecret"`
	TokenExpiresAt  string `json:"tokenExpiresAt,omitempty"`
}

// traktConnectionSummaryHandler returns the current Trakt connection state
// for Settings to render (configured/linked/expiry), never the real
// secret or tokens. An unconfigured connection is not an error — it
// returns the zero-value summary (Configured: false).
func traktConnectionSummaryHandler(store *trakt.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := store.Get(r.Context())
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, trakt.ErrNotConfigured) {
			json.NewEncoder(w).Encode(traktConnectionSummary{})
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		summary := traktConnectionSummary{
			Configured:      true,
			Linked:          conn.Tokens.Linked(),
			ClientID:        conn.ClientID,
			HasClientSecret: conn.ClientSecret != "",
		}
		if !conn.ExpiresAt.IsZero() {
			summary.TokenExpiresAt = conn.ExpiresAt.UTC().Format(time.RFC3339)
		}
		json.NewEncoder(w).Encode(summary)
	}
}

// traktDeviceFlow holds the one in-flight device-code authorization (if
// any) between a start call and however many status polls the frontend
// makes — necessary because the device flow is inherently two-step
// (RequestDeviceCode, then repeated PollDeviceToken) and the frontend
// can't be trusted/expected to hold the device_code itself across polls.
// A single mutex-guarded field is correct, not a premature simplification,
// because this project is single-operator/single-connection throughout
// (CLAUDE.md) — there is never more than one Trakt account being linked at
// a time. The zero value (&traktDeviceFlow{}) is ready to use.
type traktDeviceFlow struct {
	mu     sync.Mutex
	device *trakt.DeviceCode
}

// newTraktDeviceFlow is a constructor for clarity at the call site (handler.go
// wiring); the zero value would work identically.
func newTraktDeviceFlow() *traktDeviceFlow {
	return &traktDeviceFlow{}
}

// errNoTraktDeviceFlow is returned by traktDeviceFlow.status when the
// frontend polls status before ever calling start (or after the server
// restarted and lost the in-memory pending code) — the frontend's response
// should prompt the operator to start over.
var errNoTraktDeviceFlow = errors.New("trakt: no device authorization in progress; start one first")

func (f *traktDeviceFlow) start(ctx context.Context, client *trakt.Client) (*trakt.DeviceCode, error) {
	dc, err := client.RequestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.device = dc
	f.mu.Unlock()
	return dc, nil
}

// traktDeviceStatus is one of the four values traktDeviceStatusHandler's
// JSON response reports.
type traktDeviceStatus string

const (
	traktDeviceStatusPending traktDeviceStatus = "pending"
	traktDeviceStatusLinked  traktDeviceStatus = "linked"
	traktDeviceStatusExpired traktDeviceStatus = "expired"
	traktDeviceStatusDenied  traktDeviceStatus = "denied"
)

// status makes exactly one PollDeviceToken attempt against whatever device
// code is currently pending (never loops/sleeps itself — this handler is
// non-blocking by design per task #9, so polling cadence is the frontend's
// job, e.g. an interval timer calling the status route every few seconds).
// On success, tokens are saved via store and the pending code is cleared.
// On a terminal outcome (expired/denied), the pending code is cleared too,
// so a subsequent poll correctly reports errNoTraktDeviceFlow instead of
// re-polling a dead code. Pending/slow-down leaves the code in place for
// the next poll.
func (f *traktDeviceFlow) status(ctx context.Context, client *trakt.Client, store *trakt.Store) (traktDeviceStatus, error) {
	f.mu.Lock()
	dc := f.device
	f.mu.Unlock()
	if dc == nil {
		return "", errNoTraktDeviceFlow
	}

	tok, err := client.PollDeviceToken(ctx, dc.DeviceCode)
	switch {
	case err == nil:
		if serr := store.SaveTokens(ctx, tok.AccessToken, tok.RefreshToken, tok.ExpiresAt); serr != nil {
			return "", fmt.Errorf("saving trakt tokens: %w", serr)
		}
		f.clear()
		return traktDeviceStatusLinked, nil
	case errors.Is(err, trakt.ErrAuthorizationPending), errors.Is(err, trakt.ErrSlowDown):
		return traktDeviceStatusPending, nil
	case errors.Is(err, trakt.ErrDeviceCodeExpired):
		f.clear()
		return traktDeviceStatusExpired, nil
	case errors.Is(err, trakt.ErrDeviceCodeDenied), errors.Is(err, trakt.ErrDeviceCodeNotFound), errors.Is(err, trakt.ErrDeviceCodeUsed):
		f.clear()
		return traktDeviceStatusDenied, nil
	default:
		return "", err
	}
}

func (f *traktDeviceFlow) clear() {
	f.mu.Lock()
	f.device = nil
	f.mu.Unlock()
}

// traktClientFromStore loads the currently-stored credentials and builds a
// *trakt.Client from them. Returns trakt.ErrNotConfigured unchanged if
// nothing has been saved yet, so callers can 412 with a clear message
// instead of a generic 500.
func traktClientFromStore(ctx context.Context, store *trakt.Store, httpClient *http.Client, baseURL string) (*trakt.Client, error) {
	conn, err := store.Get(ctx)
	if err != nil {
		return nil, err
	}
	return trakt.New(trakt.Config{BaseURL: baseURL, ClientID: conn.ClientID, ClientSecret: conn.ClientSecret}, httpClient), nil
}

// traktDeviceStartResponse is POST /api/connections/trakt/device's response
// — everything the frontend needs to show the operator (a code to enter and
// a URL to visit) and to know how often to poll status. DeviceCode itself
// (the secret the server polls with) is deliberately NOT included — the
// frontend never needs it, since status polling is server-side.
type traktDeviceStartResponse struct {
	UserCode        string `json:"userCode"`
	VerificationURL string `json:"verificationUrl"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// traktDeviceStartHandler starts a new device-code authorization. Returns
// 412 Precondition Failed if no client_id/secret has been saved yet — there's
// nothing to authorize against.
func traktDeviceStartHandler(store *trakt.Store, flow *traktDeviceFlow, httpClient *http.Client, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		client, err := traktClientFromStore(ctx, store, httpClient, baseURL)
		if errors.Is(err, trakt.ErrNotConfigured) {
			http.Error(w, "trakt is not configured yet", http.StatusPreconditionFailed)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dc, err := flow.start(ctx, client)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(traktDeviceStartResponse{
			UserCode:        dc.UserCode,
			VerificationURL: dc.VerificationURL,
			ExpiresIn:       dc.ExpiresIn,
			Interval:        dc.Interval,
		})
	}
}

// traktDeviceStatusResponse is GET /api/connections/trakt/device's response.
type traktDeviceStatusResponse struct {
	Status string `json:"status"` // "pending" | "linked" | "expired" | "denied"
}

// traktDeviceStatusHandler makes one poll attempt and reports the outcome.
// Returns 409 Conflict if no flow is in progress (start wasn't called, or
// the pending code was already resolved/cleared by an earlier poll).
func traktDeviceStatusHandler(store *trakt.Store, flow *traktDeviceFlow, httpClient *http.Client, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		client, err := traktClientFromStore(ctx, store, httpClient, baseURL)
		if errors.Is(err, trakt.ErrNotConfigured) {
			http.Error(w, "trakt is not configured yet", http.StatusPreconditionFailed)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		status, err := flow.status(ctx, client, store)
		if errors.Is(err, errNoTraktDeviceFlow) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(traktDeviceStatusResponse{Status: string(status)})
	}
}

// traktWatchlistItem mirrors apidto.DiscoverItem's wire shape field-for-field
// so the frontend can render Trakt watchlist entries through the same card
// component as a regular Discover row. PosterPath/Overview/VoteAverage are
// always blank/zero — Trakt's watchlist only returns title/year/TMDB id, no
// artwork/overview/rating (see internal/trakt.WatchlistItem) — enriching
// these via a per-item TMDB details call is a deliberate non-goal here (an
// N-item watchlist would mean N extra TMDB calls per page load); task #5/#8
// should decide whether that enrichment belongs client-side, server-side, or
// not at all. ReleaseDate is only ever "YYYY-01-01" (year-only, Trakt
// doesn't give an exact date) when Year is known, else blank.
type traktWatchlistItem struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	PosterPath  string  `json:"posterPath"`
	Overview    string  `json:"overview"`
	ReleaseDate string  `json:"releaseDate"`
	VoteAverage float64 `json:"voteAverage"`
	MediaType   string  `json:"mediaType"`
}

// traktWatchlistHandler returns the linked account's watchlist, mapped to
// DiscoverItem's shape. Not configured or not yet linked both degrade to an
// empty list (not an error) — the watchlist row simply has nothing to show
// until Settings' connection summary reports Configured/Linked; a 4xx here
// would just be extra error-handling the frontend doesn't need for a
// read-only row. Any other failure (e.g. Trakt itself erroring) is a real
// 502, since that's an actual fetch failure worth surfacing.
func traktWatchlistHandler(store *trakt.Store, httpClient *http.Client, baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		client, err := traktClientFromStore(ctx, store, httpClient, baseURL)
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, trakt.ErrNotConfigured) {
			json.NewEncoder(w).Encode([]traktWatchlistItem{})
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		session := trakt.NewSession(store, client)
		items, err := session.Watchlist(ctx)
		if errors.Is(err, trakt.ErrNotLinked) {
			json.NewEncoder(w).Encode([]traktWatchlistItem{})
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out := make([]traktWatchlistItem, len(items))
		for i, it := range items {
			mediaType := "movie"
			if it.Type == "show" {
				mediaType = "tv"
			}
			releaseDate := ""
			if it.Year > 0 {
				releaseDate = fmt.Sprintf("%04d-01-01", it.Year)
			}
			out[i] = traktWatchlistItem{ID: it.TMDBID, Title: it.Title, ReleaseDate: releaseDate, MediaType: mediaType}
		}
		json.NewEncoder(w).Encode(out)
	}
}
