// Package apidto is sakms's curated, exported request/response DTO
// boundary for the future frontend's generated TypeScript API client (see
// .omc/plans/frontend-redesign-seerr.md, Stage 0 / Guardrail #4).
//
// Why this package exists: today's handlers decode/encode JSON using
// unexported request structs (e.g. internal/api's upsertConnectionRequest)
// and raw internal/domain structs (e.g. tmdb.Item, connections.Summary)
// encoded directly. A source-parsing codegen tool CAN see unexported types
// if pointed at internal/api directly, but that would emit a TypeScript
// type for every internal handler struct across the whole package — far
// more than a frontend client should ever import, and it would silently
// change shape every time an unrelated handler's internal request struct
// changed. This package is the deliberate alternative: a small, hand-picked,
// EXPORTED set of types that mirror only what a frontend actually needs to
// send/receive, kept in one place a codegen tool can be pointed at in
// isolation (see internal/apidto/gen).
//
// Scope (Stage 0 / Stage 1 only — see README.md "Scope grows per stage"):
// auth boot (setup/login/status/mode/OIDC config/API key management) and
// Discover's read-only surface (poster items + availability badges), the
// exact set Stage 1's toolchain slice consumes. Stages 2-4 add their own
// DTOs here as their frontend work lands — this file is a starting point,
// not a final inventory, per Guardrail #4.
//
// These types are currently PARALLEL COPIES of the shapes already produced
// by internal/api's handlers (authStatusResponse, oidcStatusResponse,
// tmdb.Item, etc.) and internal/auth's APIKeyStatus. Stage 0 defines them
// but does not wire any handler to use them — no frontend exists yet to
// prove a wiring change against, and touching the auth handlers here would
// add lockout risk for zero benefit (see README.md). Stage 1 is expected to
// converge the real handlers onto these exact types, at which point the
// parallel definitions in internal/api collapse into a single source of
// truth here.
//
// Field names, JSON tags, and types below match the current wire format
// exactly (same lowerCamelCase JSON keys the existing handlers already
// emit) so that a future Stage-1 handler swap is a type substitution, not a
// wire-format change.
//
// IMPORTANT — three-state optional-secret fields (Guardrail #5): see
// ConnectionUpsertRequest.APIKey's doc comment and README.md's "Three-state
// secret mapping rule" section before generating or consuming a TypeScript
// client for any *string field in this package.
package apidto

// --- Auth boot: setup, login, status --------------------------------------

// SetupRequest is the body of POST /api/auth/setup — SAK's one-time,
// first-run login bootstrap. Mode selects the auth strategy ("password" is
// the default when Mode is omitted, "oidc", or "none").
// AcknowledgeInsecure must be true to select Mode "none". The four
// OIDC* fields are required together, and only meaningful, when
// Mode == "oidc".
type SetupRequest struct {
	Username            string `json:"username"`
	Password            string `json:"password"`
	Mode                string `json:"mode"`
	AcknowledgeInsecure bool   `json:"acknowledgeInsecure"`
	OIDCIssuerURL       string `json:"oidcIssuerUrl,omitempty"`
	OIDCClientID        string `json:"oidcClientId,omitempty"`
	OIDCClientSecret    string `json:"oidcClientSecret,omitempty"`
	OIDCRedirectURL     string `json:"oidcRedirectUrl,omitempty"`
}

// SetupResponse is returned by POST /api/auth/setup only for "oidc"-mode
// setup (empty body / 204 for "password"/"none"). Exactly one of APIKey or
// APIKeyNote is populated: APIKey is a one-time break-glass credential
// revealed ONCE, never retrievable again; APIKeyNote is present instead
// when SAKMS_API_KEY is set via environment (no settings-managed key is
// minted in that case — the env value IS the break-glass credential).
type SetupResponse struct {
	APIKey     string `json:"apiKey,omitempty"`
	APIKeyNote string `json:"apiKeyNote,omitempty"`
}

// LoginRequest is the body of POST /api/auth/login — only meaningful when
// the active auth mode is "password" (checked server-side; "oidc" logs in
// via a full-page redirect to /api/auth/oidc/login instead, and "none" has
// no login step).
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AuthStatusResponse is GET /api/auth/status's response — the one call the
// frontend's boot sequence makes before it knows anything else about the
// instance, deciding between the setup wizard, the login screen, and the
// app (see Guardrail #2's 3-way boot branch). Mode is one of "password",
// "oidc", or "none".
type AuthStatusResponse struct {
	Configured    bool   `json:"configured"`
	Authenticated bool   `json:"authenticated"`
	Mode          string `json:"mode"`
}

// AuthModeResponse is GET /api/auth/mode's response.
type AuthModeResponse struct {
	Mode string `json:"mode"`
}

// AuthModeRequest is PUT /api/auth/mode's body — switches the ALREADY
// authenticated operator's active auth mode. AcknowledgeInsecure must be
// true to switch into "none" (mirrors SetupRequest's same field for the
// first-run case).
type AuthModeRequest struct {
	Mode                string `json:"mode"`
	AcknowledgeInsecure bool   `json:"acknowledgeInsecure"`
}

// --- OIDC config (post-first-run Settings switch) --------------------------

// OIDCStatusResponse is GET /api/auth/oidc's response. HasSecret reports
// whether a client secret is currently stored; the secret itself is NEVER
// returned (mirrors ConnectionSummary's HasAPIKey/KeySuffix pattern for the
// same reason — see README.md).
type OIDCStatusResponse struct {
	IssuerURL   string `json:"issuerUrl"`
	ClientID    string `json:"clientId"`
	RedirectURL string `json:"redirectUrl"`
	HasSecret   bool   `json:"hasSecret"`
}

// OIDCConfigRequest is PUT /api/auth/oidc's body — sets/replaces the
// OIDC provider config for an already-configured instance. Unlike
// ConnectionUpsertRequest.APIKey, ClientSecret here is a plain (non-pointer)
// required field: every PUT to this endpoint must supply the full config,
// there is no "leave secret unchanged" partial-update mode for OIDC config
// today.
type OIDCConfigRequest struct {
	IssuerURL    string `json:"issuerUrl"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RedirectURL  string `json:"redirectUrl"`
}

// --- API key management -----------------------------------------------------

// APIKeyStatusResponse is GET /api/apikey's response. Source is one of
// "env" (SAKMS_API_KEY active), "settings" (a regenerated key is stored),
// or "none" (no key exists). The key itself is never returned here — only
// KeySuffix, matching ConnectionSummary's masked-display convention.
type APIKeyStatusResponse struct {
	HasKey    bool   `json:"hasKey"`
	KeySuffix string `json:"keySuffix,omitempty"`
	Source    string `json:"source"`
}

// APIKeyRegenerateResponse is POST /api/apikey/regenerate's response — the
// one place the full API key crosses the API boundary. Shown once, never
// retrievable again afterward (same one-shot-reveal contract as
// SetupResponse.APIKey).
type APIKeyRegenerateResponse struct {
	APIKey    string `json:"apiKey"`
	KeySuffix string `json:"keySuffix"`
}

// --- Setup wizard status -----------------------------------------------------

// ModeStatus reports what's configured for one mode (Mode is one of
// "movies", "series", "adult") — enough for the setup wizard to know which
// steps are already done and skip past them.
type ModeStatus struct {
	Mode           string `json:"mode"`
	Available      bool   `json:"available"`
	ArrConfigured  bool   `json:"arrConfigured"`
	AllowlistCount int    `json:"allowlistCount"`
}

// SetupStatusResponse is GET /api/setup/status's response — a pure read
// model over what's already configured, driving whether the setup wizard
// shows itself at all and which of its steps it can skip.
type SetupStatusResponse struct {
	Modes              []ModeStatus `json:"modes"`
	JellyfinConfigured bool         `json:"jellyfinConfigured"`
	OllamaConfigured   bool         `json:"ollamaConfigured"`
	Dismissed          bool         `json:"dismissed"`
	AnyConfigured      bool         `json:"anyConfigured"`
}

// DismissSetupRequest is PUT /api/setup/dismissed's body.
type DismissSetupRequest struct {
	Dismissed bool `json:"dismissed"`
}

// --- Discover (read-only) ----------------------------------------------------

// DiscoverItem is one TMDB trending/popular result for Movies/Series
// Discover (GET /api/modes/{mode}/discover) — mirrors tmdb.Item's exact
// wire shape. MediaType is "movie" or "tv".
type DiscoverItem struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	PosterPath  string  `json:"posterPath"`
	Overview    string  `json:"overview"`
	ReleaseDate string  `json:"releaseDate"`
	VoteAverage float64 `json:"voteAverage"`
	MediaType   string  `json:"mediaType"`
}

// AdultDiscoverItem is one TPDB scene result for Adult Discover
// (GET /api/modes/adult/discover) — scene-shaped, not title-shaped (Studio
// substitutes for a studio/site name). Date is TPDB's release date string,
// unparsed. Image is the scene thumbnail URL served from TPDB's own image CDN
// (cdn.theporndb.net); it is frequently empty (many scenes have no art), so
// the client must render a text-only card when blank and route non-empty
// values through the image proxy (GET /api/images/proxy?url=), never
// hot-linking TPDB directly (plan Decision #7). DurationSeconds is the
// scene's pre-grab runtime in seconds (see internal/tpdbrest.Scene.Duration
// for sourcing/confidence: documented-shape + corroborated by two
// independent sources, not live-confirmed against a real TPDB instance); it
// may be 0 (unknown), which the auto-grab bitrate scorer (Stage 2) must
// treat as "skip the pre-grab bitrate check," never a real zero-length
// runtime or a divide-by-zero input.
type AdultDiscoverItem struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	Studio          string `json:"studio"`
	Date            string `json:"date"`
	Image           string `json:"image"`
	DurationSeconds int    `json:"durationSeconds"`
}

// AvailabilityResponse is GET /api/modes/{mode}/availability's response —
// backs a Discover card's availability badge (Stage 1 scope: "poster cards
// with availability badges" per the plan). CheckedAt is an RFC3339Nano
// timestamp of when the probe ran.
type AvailabilityResponse struct {
	Available    bool   `json:"available"`
	ReleaseCount int    `json:"releaseCount"`
	CheckedAt    string `json:"checkedAt"`
}

// --- Connections (reference implementation of the three-state secret rule) -

// ConnectionSummary is one entry of GET /api/connections's response — what's
// safe to expose about a configured connection: never the secret itself,
// only whether one is set and its last 4 characters (masked display).
// Included here alongside ConnectionUpsertRequest even though the
// Connections/Settings UI itself isn't built until Stage 4 — see
// ConnectionUpsertRequest's doc comment for why.
type ConnectionSummary struct {
	Service   string `json:"service"`
	URL       string `json:"url"`
	Username  string `json:"username,omitempty"`
	HasAPIKey bool   `json:"hasApiKey"`
	KeySuffix string `json:"keySuffix,omitempty"`
	UpdatedAt string `json:"updatedAt"`
}

// ConnectionUpsertRequest is PUT /api/connections/{service}'s body —
// included in Stage 0's curated set (ahead of Stage 4, when Settings'
// actual frontend lands) specifically BECAUSE it carries the single most
// safety-critical mapping rule in this whole DTO boundary (Guardrail #5)
// and needed to be proven against the chosen codegen tool before that tool
// choice was finalized, not discovered as a surprise in Stage 4.
//
// APIKey is a pointer so the three states a client MUST be able to express
// survive the JSON round-trip (json.Decode sets it accordingly; omitempty
// only affects marshaling, never decoding):
//
//   - field ABSENT from the request body entirely (nil)  → preserve the
//     stored secret. The server never sends the real secret back to a
//     client (see ConnectionSummary above — only HasAPIKey/KeySuffix are
//     exposed), so an untouched, blank key input MUST be omitted from the
//     JSON body, never sent as "".
//   - field present as ""  (&"")                          → explicitly
//     clear the stored secret (e.g. switching to a service that needs
//     none, like Ollama).
//   - field present, non-empty  (&"sk-...")                → set/replace
//     the stored secret.
//
// TypeScript CANNOT express this three-way distinction as a type — both
// a field absent entirely and a field present with an empty-string value
// collapse to the same `string | undefined` a source-parsing generator
// emits for a Go *string.
// See README.md's "Three-state secret mapping rule" section for the
// generated TypeScript shape and the load-bearing prose rule a frontend
// MUST follow by convention (not by the type system) when building this
// request body.
type ConnectionUpsertRequest struct {
	URL      string  `json:"url"`
	Username string  `json:"username,omitempty"`
	APIKey   *string `json:"apiKey,omitempty"`
}

// --- Stage 2: auto-grab (Discover becomes mutating) ------------------------

// Grab mirrors internal/grabs.Grab's exact wire shape — the record SAK keeps
// for one release it has sent to a download client. Exposed here so the
// frontend's Grabs view and the auto-grab response share one generated
// TypeScript type instead of hand-duplicating the shape.
//
// FlaggedForReview / FlagReason are the ADVISORY post-grab mislabel signal
// (set by checkImportHandler via internal/autograb.RuntimeMismatch, Movies
// only for now): they do NOT mean the import failed — the import already
// succeeded — only that the imported file's runtime looked inconsistent with
// its metadata and a human might want to eyeball it. The Grabs view must say
// so in its copy, never present the flag as an error.
type Grab struct {
	ID               int64  `json:"id"`
	Mode             string `json:"mode"`
	Title            string `json:"title"`
	TMDBID           int    `json:"tmdbId,omitempty"`
	TVDBID           int    `json:"tvdbId,omitempty"`
	SeasonNumber     int    `json:"seasonNumber,omitempty"`
	EpisodeNumber    int    `json:"episodeNumber,omitempty"`
	SeasonSpecified  bool   `json:"seasonSpecified,omitempty"`
	QualityProfileID int    `json:"qualityProfileId,omitempty"`
	Indexer          string `json:"indexer"`
	Protocol         string `json:"protocol"`
	DownloadClient   string `json:"downloadClient"`
	ClientRef        string `json:"clientRef,omitempty"`
	Status           string `json:"status"`
	RootFolderPath   string `json:"rootFolderPath"`
	FlaggedForReview bool   `json:"flaggedForReview,omitempty"`
	FlagReason       string `json:"flagReason,omitempty"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

// AutoGrabRequest is POST /api/modes/{mode}/autograb's body — Discover's
// one-click unattended grab trigger for exactly one title/scene. Which fields
// matter is mode-specific:
//   - Movies:  TMDBID (drives the id-scoped Prowlarr search AND the TMDB
//     runtime lookup the bitrate scorer needs) + Title.
//   - Series:  TMDBID + Title + SeasonNumber/EpisodeNumber/SeasonSpecified —
//     the picker's selection ("one click PER season/episode selection", per
//     the plan's per-mode nuance). No per-episode runtime exists pre-grab
//     today, so Series candidates all grade as unknown-bitrate and the call
//     returns the manual fallback list rather than auto-grabbing (documented
//     behavior, not a bug). SeasonSpecified must be threaded through so a
//     deliberate Season-0/Specials grab isn't misread as "no season picked"
//     (see grabs.Grab.SeasonSpecified / checkImportHandler).
//   - Adult:   Title + Studio (the free-text Prowlarr query, mirroring
//     availability.CheckAdultScene) + DurationSeconds (TPDB's pre-grab
//     runtime → the scorer's RuntimeSeconds; 0 = unknown, handled neutrally).
type AutoGrabRequest struct {
	Title           string `json:"title"`
	TMDBID          int    `json:"tmdbId,omitempty"`
	Studio          string `json:"studio,omitempty"`
	SeasonNumber    int    `json:"seasonNumber,omitempty"`
	EpisodeNumber   int    `json:"episodeNumber,omitempty"`
	SeasonSpecified bool   `json:"seasonSpecified,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

// AutoGrabCandidate is one graded release in an auto-grab manual-fallback list
// (AutoGrabResponse.Candidates). It pairs the grade (Status = why it did/didn't
// auto-qualify, Score = the bitrate ranking key) with the exact release
// identity the frontend needs to grab it manually via
// POST /api/modes/{mode}/search/grab — one release per click, never a batch.
// Status is one of internal/autograb.Status's values ("qualified",
// "below-floor", "mislabeled", "low-seeders", "unknown-bitrate",
// "unknown-resolution").
type AutoGrabCandidate struct {
	Title       string  `json:"title"`
	Indexer     string  `json:"indexer"`
	Protocol    string  `json:"protocol"`
	DownloadURL string  `json:"downloadUrl"`
	Size        int64   `json:"size"`
	Seeders     int     `json:"seeders"`
	Status      string  `json:"status"`
	Score       float64 `json:"score"`
	ImpliedMbps float64 `json:"impliedMbps"`
	FloorMbps   float64 `json:"floorMbps"`
	Qualified   bool    `json:"qualified"`
}

// AutoGrabResponse is POST /api/modes/{mode}/autograb's result — exactly one
// of two outcomes:
//   - Grabbed == true:  a release cleared every gate and was sent to the
//     download client. Grab is the recorded grab (also visible in the Grabs
//     view); Candidates is empty.
//   - Grabbed == false (Fallback == true): nothing auto-qualified —
//     Candidates is the ranked manual pick list (best bitrate score first,
//     the SAME score that gated auto-grab), each row labeled with why it
//     didn't qualify. The operator picks exactly one to grab; Grab is nil.
//
// Message is a short human summary for the UI.
type AutoGrabResponse struct {
	Grabbed    bool                `json:"grabbed"`
	Fallback   bool                `json:"fallback"`
	Message    string              `json:"message"`
	Grab       *Grab               `json:"grab,omitempty"`
	Candidates []AutoGrabCandidate `json:"candidates,omitempty"`
}
