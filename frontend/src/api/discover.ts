// Discover data access — the read-only slice of SAK's Discover surface this
// wave ships (poster/scene lists + per-card availability badges). Every call
// goes through api() (src/api/client.ts) so it inherits the session cookie and
// the global 401 → re-boot session-expiry fallback. Request/response shapes are
// the generated DTOs (@dto), never hand-duplicated (plan Guardrail #4).

import { api } from "./client";
import type {
  AdultDiscoverItem,
  AvailabilityResponse,
  DiscoverItem,
} from "@dto";

export type { AdultDiscoverItem, AvailabilityResponse, DiscoverItem };

// Mode is the three top-level libraries. Movies/Series share the TMDB
// title-shaped Discover path; Adult is scene-shaped (TPDB).
export type Mode = "movies" | "series" | "adult";

// ProposalStatus narrows the DTO's `status: string` to the four lifecycle
// values proposals.Status emits. The single shared definition for every review
// workflow (Rename/Purge/Dedup), which each re-export — the same shared-narrow
// pattern as Mode, kept out of apidto so the generated DTO stays a minimal wire
// mirror.
export type ProposalStatus = "pending" | "unmatched" | "applied" | "dismissed";

// DiscoverCategory selects which TMDB list a Movies/Series row renders. These
// are the only two the backend's discoverHandler accepts (trending | popular).
export type DiscoverCategory = "trending" | "popular";

// TMDB_POSTER_BASE / TMDB_HERO_BASE build a full image.tmdb.org URL from a
// bare posterPath (e.g. "/abc.jpg"). The browser never requests these hosts
// directly — proxyImage() wraps them so every byte flows through the Go image
// proxy (plan Decision #7). w342 is the grid poster size the old frontend used
// (internal/web/static/index.html:882); w780 is the larger hero size.
const TMDB_POSTER_BASE = "https://image.tmdb.org/t/p/w342";
const TMDB_HERO_BASE = "https://image.tmdb.org/t/p/w780";

// proxyImage rewrites an absolute upstream image URL into a same-origin image
// proxy request. This is the ONLY way images reach the DOM in this app: an
// <img src> must be proxyImage(...)'d, never the raw upstream URL. Returns ""
// for a blank input so callers can Show/skip a missing thumbnail.
export function proxyImage(rawURL: string): string {
  if (!rawURL) return "";
  return "/api/images/proxy?url=" + encodeURIComponent(rawURL);
}

// tmdbPoster / tmdbHero turn a TMDB posterPath into a proxied grid/hero image
// URL. A blank posterPath yields "" (no image), which the card renders as a
// text-only fallback.
export function tmdbPoster(posterPath: string): string {
  if (!posterPath) return "";
  return proxyImage(TMDB_POSTER_BASE + posterPath);
}

export function tmdbHero(posterPath: string): string {
  if (!posterPath) return "";
  return proxyImage(TMDB_HERO_BASE + posterPath);
}

// fetchDiscover returns one TMDB category (trending/popular) for Movies/Series.
export function fetchDiscover(
  mode: Exclude<Mode, "adult">,
  category: DiscoverCategory,
): Promise<DiscoverItem[]> {
  return api<DiscoverItem[]>(
    `/api/modes/${mode}/discover?category=${category}`,
  );
}

// fetchAdultDiscover returns one page of TPDB's scene catalog (plain browse),
// or a title search when query is non-empty.
export function fetchAdultDiscover(query?: string): Promise<AdultDiscoverItem[]> {
  const q = query?.trim();
  const path = q
    ? `/api/modes/adult/discover?q=${encodeURIComponent(q)}`
    : `/api/modes/adult/discover`;
  return api<AdultDiscoverItem[]>(path);
}

// fetchTitleAvailability probes whether a release exists for a Movies/Series
// title (tmdbId-keyed). Backs a poster card's availability badge.
export function fetchTitleAvailability(
  mode: Exclude<Mode, "adult">,
  tmdbId: number,
): Promise<AvailabilityResponse> {
  return api<AvailabilityResponse>(
    `/api/modes/${mode}/availability?tmdbId=${tmdbId}`,
  );
}

// fetchAdultAvailability probes an Adult scene's availability. Adult has no
// tmdbId — its identity is studio+title (see internal/api/availability.go), so
// the badge probe takes those instead.
export function fetchAdultAvailability(
  studio: string,
  title: string,
): Promise<AvailabilityResponse> {
  const params = new URLSearchParams({ studio: studio || "", title });
  return api<AvailabilityResponse>(
    `/api/modes/adult/availability?${params.toString()}`,
  );
}
