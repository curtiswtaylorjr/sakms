// Package tmdb is a client for The Movie Database's public v3 REST API —
// the catalog SAK's Discover view browses (trending/popular titles with
// poster art) before a user picks one to search+grab. TMDB is a single
// fixed public service (like TPDB or Brave), not self-hostable, but still
// modeled as a normal "url + api key" connections.Store entry, matching how
// this project already treats Brave/TPDB.
//
// A movie's TMDB id is a different id space entirely from TheTVDB's — TMDB
// covers TV shows too, but under its own id. ExternalIDs resolves a TMDB TV
// show id to its TVDB id (used by Discover once a user picks a TV show to
// search+grab, not for every item in a trending list); FindByTVDBID is the
// reverse, used by the one-time Sonarr importer since Sonarr tracks shows
// by TVDB id but SAK's library keys everything by TMDB id.
//
// SearchMovies/Trending/Popular/ExternalIDs are exercised live by this
// project's Discover flow. SeasonDetails and FindByTVDBID are NOT — their
// response shapes are modeled from TMDB's public API documentation only,
// per this project's honesty-about-unverified-assumptions convention.
package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/curtiswtaylorjr/sakms/internal/httpx"
)

// MediaType distinguishes TMDB's movie and TV catalogs, which use
// different field names for the same concepts (title vs name, release_date
// vs first_air_date) — normalized away into one Item shape below.
type MediaType string

const (
	Movie MediaType = "movie"
	TV    MediaType = "tv"
)

// Config parameterizes the client. BaseURL is normally
// https://api.themoviedb.org/3, stored explicitly (not hardcoded) the same
// way this project already treats Brave's fixed-but-configurable endpoint.
type Config struct {
	BaseURL string
	APIKey  string
}

type Client struct {
	cfg  Config
	http *http.Client
}

func New(cfg Config, httpClient *http.Client) *Client {
	return &Client{cfg: cfg, http: httpClient}
}

// do executes a GET against path (which may already contain its own query
// string), adding TMDB's v3 api_key auth param.
func (c *Client) do(ctx context.Context, path string, query url.Values, out any) error {
	if query == nil {
		query = url.Values{}
	}
	query.Set("api_key", c.cfg.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	return httpx.DoJSON(c.http, req, httpx.MaxResponseBodySize, out)
}

// Item is one browsable title, normalized across TMDB's movie and TV shapes.
type Item struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	PosterPath  string    `json:"posterPath"`
	Overview    string    `json:"overview"`
	ReleaseDate string    `json:"releaseDate"`
	VoteAverage float64   `json:"voteAverage"`
	MediaType   MediaType `json:"mediaType"`
}

// rawResult covers both TMDB's movie and TV result shapes in one struct —
// Title/Name and ReleaseDate/FirstAirDate are mutually exclusive depending
// on which endpoint returned it, normalized into Item by normalize below.
type rawResult struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	PosterPath   string  `json:"poster_path"`
	Overview     string  `json:"overview"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	MediaType    string  `json:"media_type"`
}

type listResponse struct {
	Results []rawResult `json:"results"`
}

func normalize(r rawResult, fallbackType MediaType) Item {
	item := Item{
		ID: r.ID, PosterPath: r.PosterPath, Overview: r.Overview, VoteAverage: r.VoteAverage,
		MediaType: fallbackType,
	}
	if r.MediaType != "" {
		item.MediaType = MediaType(r.MediaType)
	}
	if item.MediaType == TV {
		item.Title = r.Name
		item.ReleaseDate = r.FirstAirDate
	} else {
		item.Title = r.Title
		item.ReleaseDate = r.ReleaseDate
	}
	return item
}

// Trending returns TMDB's trending titles for mt over timeWindow ("day" or
// "week").
func (c *Client) Trending(ctx context.Context, mt MediaType, timeWindow string) ([]Item, error) {
	var resp listResponse
	if err := c.do(ctx, fmt.Sprintf("/trending/%s/%s", mt, timeWindow), nil, &resp); err != nil {
		return nil, err
	}
	return normalizeAll(resp.Results, mt), nil
}

// Popular returns TMDB's currently popular titles for mt.
func (c *Client) Popular(ctx context.Context, mt MediaType) ([]Item, error) {
	var resp listResponse
	if err := c.do(ctx, fmt.Sprintf("/%s/popular", mt), nil, &resp); err != nil {
		return nil, err
	}
	return normalizeAll(resp.Results, mt), nil
}

func normalizeAll(raw []rawResult, mt MediaType) []Item {
	out := make([]Item, len(raw))
	for i, r := range raw {
		out[i] = normalize(r, mt)
	}
	return out
}

// SearchMovies searches TMDB's movie catalog by title — the title-lookup
// Rename/Dedup's Movies-library code path uses instead of Servarr's own
// Lookup, since eliminating Radarr for Movies means there's no *arr app's
// TVDB/TMDB search proxy sitting between SAK and TMDB anymore (see
// internal/library's package doc).
func (c *Client) SearchMovies(ctx context.Context, query string) ([]Item, error) {
	q := url.Values{}
	q.Set("query", query)
	var resp listResponse
	if err := c.do(ctx, "/search/movie", q, &resp); err != nil {
		return nil, err
	}
	return normalizeAll(resp.Results, Movie), nil
}

// SearchTV searches TMDB's TV catalog by title — the show-title lookup
// Rename's Series-library code path and the Sonarr importer use, direct
// sibling of SearchMovies.
func (c *Client) SearchTV(ctx context.Context, query string) ([]Item, error) {
	q := url.Values{}
	q.Set("query", query)
	var resp listResponse
	if err := c.do(ctx, "/search/tv", q, &resp); err != nil {
		return nil, err
	}
	return normalizeAll(resp.Results, TV), nil
}

type externalIDsResponse struct {
	TVDBID int `json:"tvdb_id"`
}

// ExternalIDs resolves a TMDB TV show id to its TVDB id — 0 if TMDB doesn't
// have one on file for this show (rare, but possible for very new or
// obscure titles).
func (c *Client) ExternalIDs(ctx context.Context, tmdbTVID int) (tvdbID int, err error) {
	var resp externalIDsResponse
	if err := c.do(ctx, fmt.Sprintf("/tv/%d/external_ids", tmdbTVID), nil, &resp); err != nil {
		return 0, err
	}
	return resp.TVDBID, nil
}

// SeasonEpisode is one episode as TMDB's season-details endpoint reports
// it — enough to record a canonical episode row even before any file for
// it exists on disk (see internal/library's Episode).
type SeasonEpisode struct {
	EpisodeNumber int    `json:"episodeNumber"`
	Name          string `json:"name"`
	AirDate       string `json:"airDate"`
}

type seasonEpisodeRaw struct {
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	AirDate       string `json:"air_date"`
}

type seasonDetailsResponse struct {
	Episodes []seasonEpisodeRaw `json:"episodes"`
}

// SeasonDetails returns every episode TMDB knows about for one season of a
// TV show — hits /tv/{id}/season/{season_number}. Unlike SearchMovies/
// ExternalIDs (already exercised live by Discover), this shape is modeled
// from TMDB's public documentation, not yet confirmed against a live call
// — flagged per this project's honesty-about-unverified-assumptions
// convention.
func (c *Client) SeasonDetails(ctx context.Context, tmdbTVID, seasonNumber int) ([]SeasonEpisode, error) {
	var resp seasonDetailsResponse
	path := fmt.Sprintf("/tv/%d/season/%d", tmdbTVID, seasonNumber)
	if err := c.do(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]SeasonEpisode, len(resp.Episodes))
	for i, e := range resp.Episodes {
		out[i] = SeasonEpisode{EpisodeNumber: e.EpisodeNumber, Name: e.Name, AirDate: e.AirDate}
	}
	return out, nil
}

type findResponse struct {
	TVResults []rawResult `json:"tv_results"`
}

// FindByTVDBID resolves a TVDB id to its TMDB TV show id — the reverse of
// ExternalIDs, needed by the one-time Sonarr importer (Sonarr tracks shows
// by TVDB id; SAK's library keys everything by TMDB id). ok is false if
// TMDB has no TV match on file for tvdbID. Hits /find/{tvdb_id}?
// external_source=tvdb_id — like SeasonDetails, this shape is modeled from
// TMDB's public documentation, not yet confirmed against a live call.
func (c *Client) FindByTVDBID(ctx context.Context, tvdbID int) (tmdbID int, ok bool, err error) {
	q := url.Values{}
	q.Set("external_source", "tvdb_id")
	var resp findResponse
	if err := c.do(ctx, fmt.Sprintf("/find/%d", tvdbID), q, &resp); err != nil {
		return 0, false, err
	}
	if len(resp.TVResults) == 0 {
		return 0, false, nil
	}
	return resp.TVResults[0].ID, true, nil
}
