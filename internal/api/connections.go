// Package api implements SAK's HTTP API.
package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/bravesearch"
	"github.com/curtiswtaylorjr/sakms/internal/jellyfin"
	"github.com/curtiswtaylorjr/sakms/internal/nzbget"
	"github.com/curtiswtaylorjr/sakms/internal/ollama"
	"github.com/curtiswtaylorjr/sakms/internal/prowlarr"
	"github.com/curtiswtaylorjr/sakms/internal/qbittorrent"
	"github.com/curtiswtaylorjr/sakms/internal/stashapi"
	"github.com/curtiswtaylorjr/sakms/internal/stashbox"
	"github.com/curtiswtaylorjr/sakms/internal/tmdb"
	"github.com/curtiswtaylorjr/sakms/internal/tpdbrest"
	"github.com/curtiswtaylorjr/sakms/internal/trakt"
)

// ConnectionTestRequest is enough to construct a client and make one real,
// read-only call against it — the same thing Settings' "Test connection"
// button does. Nothing here is persisted.
type ConnectionTestRequest struct {
	Service  string `json:"service"` // "ollama" | "stash" | "jellyfin" | "stashdb" | "fansdb" | "tpdb" | "brave" | "prowlarr" | "qbittorrent" | "nzbget" | "tmdb" | "trakt"
	URL      string `json:"url"`
	Username string `json:"username,omitempty"` // only qbittorrent/nzbget use this
	APIKey   string `json:"apiKey,omitempty"`
}

// ConnectionTestResult reports whether the test call succeeded. A false OK
// with a populated Error is the normal, expected shape for "wrong URL" or
// "wrong key" — not a server-side failure.
type ConnectionTestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// TestConnection dispatches req to the client for its Service and makes one
// lightweight call to confirm the URL/key actually work. Every branch uses a
// real, already-existing endpoint — StashDB/FansDB's protocol-level "me"
// query, TPDB's own /scenes endpoint with minimal params, and Brave's actual
// search endpoint (it has no separate lightweight check, so this is a real,
// billable query, same as any other Brave call) — never a guessed one.
// There is deliberately no "radarr" or "sonarr" case: Movies/Series own their
// own libraries now instead of proxying Radarr/Sonarr (see internal/library's
// package doc), so there's nothing left in SAK that would ever test a
// Radarr/Sonarr connection.
func TestConnection(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	switch req.Service {
	case "ollama":
		return testOllama(ctx, httpClient, req)
	case "stash":
		return testStash(ctx, httpClient, req)
	case "jellyfin":
		return testJellyfin(ctx, httpClient, req)
	case "stashdb", "fansdb":
		// Fixed public stash-box endpoints — the URL is the hardcoded per-name
		// constant, never req.URL (the UI collects no URL for these).
		endpoint, _ := stashbox.URLForBox(req.Service)
		return testStashBox(ctx, httpClient, req, endpoint)
	case "tpdb":
		return testTPDB(ctx, httpClient, req)
	case "brave":
		return testBrave(ctx, httpClient, req)
	case "prowlarr":
		return testProwlarr(ctx, httpClient, req)
	case "qbittorrent":
		return testQBittorrent(ctx, httpClient, req)
	case "nzbget":
		return testNZBGet(ctx, httpClient, req)
	case "tmdb":
		return testTMDB(ctx, httpClient, req)
	case "trakt":
		// Trakt has no dedicated client_id field on ConnectionTestRequest, so
		// by convention (see trakt.go's testTrakt doc comment) the generic
		// APIKey field carries client_id here — client_secret isn't needed,
		// Ping only validates client_id against a public endpoint.
		return testTrakt(ctx, httpClient, trakt.DefaultBaseURL, req.APIKey)
	default:
		return ConnectionTestResult{Error: fmt.Sprintf("unsupported service %q", req.Service)}
	}
}

func testOllama(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := ollama.New(req.URL, "", httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

// testStash expects req.URL to already point at Stash's GraphQL endpoint
// (e.g. "http://host:9999/graphql"), matching stashapi.Config.URL.
func testStash(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := stashapi.New(stashapi.Config{URL: req.URL, APIKey: req.APIKey}, httpClient)
	if _, err := c.AllTags(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

// testJellyfin expects req.URL to point at Jellyfin's base URL (e.g.
// "https://jf.zaena.us") — req.APIKey is a Jellyfin API key, matching
// jellyfin.Config.
func testJellyfin(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := jellyfin.New(jellyfin.Config{URL: req.URL, APIKey: req.APIKey}, httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

// testStashBox covers both StashDB and FansDB — same stash-box protocol,
// ApiKey-header auth (as opposed to TPDB's GraphQL endpoint, which is
// Bearer-authed and reached separately via testTPDB). endpoint is the fixed
// per-name public GraphQL URL (stashbox.StashDBURL / FansDBURL), not a
// user-supplied value — StashDB/FansDB collect no URL in Settings.
func testStashBox(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest, endpoint string) ConnectionTestResult {
	c := stashbox.New(stashbox.Config{Endpoint: endpoint, APIKey: req.APIKey, IsBearer: false, HasVoteField: true}, httpClient)
	if _, err := c.Me(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

// testTPDB uses TPDB's REST endpoint (req.URL is the REST base, e.g.
// "https://api.theporndb.net") rather than its GraphQL endpoint — REST is
// what identify's actual text-search fallback uses day to day, so that's
// the connection worth confirming here.
func testTPDB(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	// Fixed public REST base — hardcoded, never req.URL (no URL collected).
	c := tpdbrest.New(tpdbrest.DefaultBaseURL, req.APIKey, httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

// testBrave costs one real query against the account's search quota — Brave
// has no free lightweight way to verify a key, and pretending otherwise
// would be misleading (see the Settings design's cost-visibility note for
// Brave specifically).
func testBrave(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := bravesearch.New(req.URL, req.APIKey, httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

func testProwlarr(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := prowlarr.New(prowlarr.Config{BaseURL: req.URL, APIKey: req.APIKey}, httpClient)
	if _, err := c.Search(ctx, "", nil); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

func testQBittorrent(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := qbittorrent.New(qbittorrent.Config{BaseURL: req.URL, Username: req.Username, Password: req.APIKey}, httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

func testNZBGet(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	c := nzbget.New(nzbget.Config{BaseURL: req.URL, Username: req.Username, Password: req.APIKey}, httpClient)
	if err := c.Ping(ctx); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}

func testTMDB(ctx context.Context, httpClient *http.Client, req ConnectionTestRequest) ConnectionTestResult {
	// Fixed public base URL — hardcoded, never req.URL (no URL collected).
	c := tmdb.New(tmdb.Config{BaseURL: tmdb.DefaultBaseURL, APIKey: req.APIKey}, httpClient)
	if _, err := c.Popular(ctx, tmdb.Movie, 1); err != nil {
		return ConnectionTestResult{Error: err.Error()}
	}
	return ConnectionTestResult{OK: true}
}
