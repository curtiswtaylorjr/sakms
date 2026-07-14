package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/curtiswtaylorjr/sakms/internal/connections"
	"github.com/curtiswtaylorjr/sakms/internal/mode"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
	"github.com/curtiswtaylorjr/sakms/internal/tmdb"
)

// mediaTypeForMode maps {mode} onto TMDB's media type, the same convention
// categoriesForSearch uses for Prowlarr's Newznab categories: Series is TV,
// everything else (Movies) is the movie catalog.
func mediaTypeForMode(m mode.Mode) tmdb.MediaType {
	if m == mode.Series {
		return tmdb.TV
	}
	return tmdb.Movie
}

// discoverHandler returns TMDB's trending or popular titles for {mode}'s
// media type — a read-only proxy+normalize, nothing staged or persisted.
// Series items carry only their TMDB id here;
// resolving the TVDB id Sonarr's AddRequest actually needs is deferred to
// resolveTVDBIDHandler, called only once a user picks a specific title to
// search+grab — not eagerly for every item in a trending list, which would
// multiply this one TMDB call into one-plus-N for results nobody clicks.
func discoverHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()
		category := r.URL.Query().Get("category")
		if category == "" {
			category = "trending"
		}
		// page is TMDB's 1-based pagination cursor, backing Discover's per-row
		// "Show more". Absent/blank/invalid defaults to 1 (the first page) —
		// the pre-pagination behavior — rather than erroring, so an old client
		// or a bare first load keeps working unchanged.
		page := 1
		if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
			page = p
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		mt := mediaTypeForMode(m)
		var items []tmdb.Item
		switch category {
		case "trending":
			items, err = sess.TMDB.Trending(ctx, mt, "week", page)
		case "popular":
			items, err = sess.TMDB.Popular(ctx, mt, page)
		case "upcoming":
			// UpcomingTV is TMDB's /tv/on_the_air — the closest TV analog to
			// Upcoming Movies' "future release date" (see tmdb.UpcomingTV's
			// doc comment); TMDB has no direct TV equivalent.
			if mt == tmdb.TV {
				items, err = sess.TMDB.UpcomingTV(ctx, page)
			} else {
				items, err = sess.TMDB.UpcomingMovies(ctx, page)
			}
		case "genre":
			genreID, gerr := strconv.Atoi(r.URL.Query().Get("genreId"))
			if gerr != nil {
				http.Error(w, "genreId query parameter is required and must be an integer", http.StatusBadRequest)
				return
			}
			if mt == tmdb.TV {
				items, err = sess.TMDB.DiscoverTVByGenre(ctx, genreID, page)
			} else {
				items, err = sess.TMDB.DiscoverMoviesByGenre(ctx, genreID, page)
			}
		case "studio":
			// Studios are a movie-catalog concept (TMDB production companies) —
			// there is no TV equivalent, so this category is Movies/Adult only,
			// mirroring the mode restriction network below applies the other way.
			if m == mode.Series {
				http.Error(w, "studio browsing is not available for series — TMDB companies are a movie-only concept", http.StatusBadRequest)
				return
			}
			studioID, serr := strconv.Atoi(r.URL.Query().Get("studioId"))
			if serr != nil {
				http.Error(w, "studioId query parameter is required and must be an integer", http.StatusBadRequest)
				return
			}
			items, err = sess.TMDB.DiscoverMoviesByStudio(ctx, studioID, page)
		case "network":
			// Symmetric restriction to studio above: networks are a TV-catalog
			// concept, series only.
			if m != mode.Series {
				http.Error(w, "network browsing is only available for series", http.StatusBadRequest)
				return
			}
			networkID, nerr := strconv.Atoi(r.URL.Query().Get("networkId"))
			if nerr != nil {
				http.Error(w, "networkId query parameter is required and must be an integer", http.StatusBadRequest)
				return
			}
			items, err = sess.TMDB.DiscoverTVByNetwork(ctx, networkID, page)
		default:
			http.Error(w, fmt.Sprintf("unrecognized category %q", category), http.StatusBadRequest)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	}
}

// discoverGenresHandler returns TMDB's fixed genre list for {mode}'s media
// type (movie genres for Movies/Adult, TV genres for Series) — reference
// data for the genre-browse row's picker and a "genre" slider's FilterValue
// dropdown in the admin editor. Not paginated; TMDB's genre list is small
// and rarely changes.
func discoverGenresHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		var genres []tmdb.Genre
		if mediaTypeForMode(m) == tmdb.TV {
			genres, err = sess.TMDB.TVGenres(ctx)
		} else {
			genres, err = sess.TMDB.MovieGenres(ctx)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(genres)
	}
}

// discoverStudiosHandler serves tmdb.KnownStudios — a fixed, static seed
// list requiring no TMDB call — backing the "browse by studio" row and the
// admin slider editor's studio picker. Global, not mode-scoped: the same
// list regardless of which mode's Discover screen is asking.
func discoverStudiosHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tmdb.KnownStudios)
	}
}

// discoverNetworksHandler is discoverStudiosHandler's direct sibling for
// tmdb.KnownNetworks.
func discoverNetworksHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tmdb.KnownNetworks)
	}
}

// discoverKeywordsHandler proxies TMDB's /search/keyword — the admin slider
// editor's way of resolving free-typed keyword text into the numeric TMDB
// id a "keyword" slider's FilterValue actually stores (see tmdb.Keyword's
// doc comment for why keywords, unlike genre/studio/network, have no fixed
// seed list). Global like discoverStudiosHandler/discoverNetworksHandler —
// keyword search isn't mode-specific, so this always builds a Movies-mode
// session purely to reach the shared "tmdb" connection (see
// tmdbSearchHandler's doc comment: sess.TMDB is populated identically for
// every mode).
func discoverKeywordsHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "q query parameter is required", http.StatusBadRequest)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, mode.Movies)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		keywords, err := sess.TMDB.SearchKeywords(ctx, query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(keywords)
	}
}

// tmdbSearchHandler is a thin TMDB title-search proxy (mirrors
// discoverHandler's session/media-type handling) for Rename's manual
// override/re-pick workflow (see internal/api/proposals.go's
// repickProposalHandler) — the search box an operator uses to find the
// correct title when Scan's automatic match (confidence-scored or not, see
// internal/rename/confidence.go) picked wrong, or scored too low to
// auto-accept. Movies/Series only, enforced by an explicit mode check
// below — mode.Build's buildSearchPipeline populates sess.TMDB from the one
// global "tmdb" connection for EVERY mode, Adult included (unlike this
// handler's sibling repickProposalHandler, which has its own Movies/Series
// guard for a different reason — refusing to re-pick Adult's foreignId-based
// proposals), so relying on "sess.TMDB is nil for Adult" here would be false
// and let Adult calls return real-but-useless movie results.
func tmdbSearchHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		if m != mode.Movies && m != mode.Series {
			http.Error(w, "tmdb-search is only supported for movies/series", http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "q query parameter is required", http.StatusBadRequest)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		var items []tmdb.Item
		if m == mode.Series {
			items, err = sess.TMDB.SearchTV(ctx, query)
		} else {
			items, err = sess.TMDB.SearchMovies(ctx, query)
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
	}
}

// posterHandler resolves a Movies/Series library card's poster art lazily,
// per card, keyed by tmdbId. SAK's library caches TMDBID/Year but no poster
// path, so the existing-library row on Discover fetches each visible card's
// poster on demand (one bounded call per rendered card) rather than the list
// endpoint doing an unbounded N+1 lookup for the whole library up front,
// exactly the N+1 discoverHandler's own doc warns against. Movies/Series
// only — Adult scenes carry their own image inline from TPDB.
func posterHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		if m != mode.Movies && m != mode.Series {
			http.Error(w, "poster lookup is only supported for movies/series", http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		tmdbID, err := strconv.Atoi(r.URL.Query().Get("tmdbId"))
		if err != nil {
			http.Error(w, "tmdbId query parameter is required and must be an integer", http.StatusBadRequest)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		var posterPath string
		if m == mode.Series {
			details, err := sess.TMDB.TVDetails(ctx, tmdbID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			posterPath = details.PosterPath
		} else {
			details, err := sess.TMDB.MovieDetails(ctx, tmdbID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			posterPath = details.PosterPath
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"posterPath": posterPath})
	}
}

// resolveTVDBIDHandler resolves a TMDB TV show id to its TVDB id — the one
// extra call needed before grabbing a Series title discovered via TMDB,
// since Sonarr's AddRequest wants a TVDB id, a different id space entirely
// (see internal/tmdb's package doc for why).
func resolveTVDBIDHandler(httpClient *http.Client, connStore *connections.Store, settingsStore *settings.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := mode.Mode(r.PathValue("mode"))
		ctx := r.Context()
		tmdbID, err := strconv.Atoi(r.URL.Query().Get("tmdbId"))
		if err != nil {
			http.Error(w, "tmdbId query parameter is required and must be an integer", http.StatusBadRequest)
			return
		}

		sess, err := mode.Build(ctx, connStore, settingsStore, httpClient, m)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if sess.TMDB == nil {
			http.Error(w, "tmdb isn't configured yet — add it in Settings first", http.StatusBadRequest)
			return
		}

		tvdbID, err := sess.TMDB.ExternalIDs(ctx, tmdbID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"tvdbId": tvdbID})
	}
}
