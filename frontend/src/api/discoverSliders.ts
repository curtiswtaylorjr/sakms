// Admin custom Discover slider data access (task #7) — CRUD + reorder for
// operator-defined rows (Seerr's CreateSlider/DiscoverSliderEdit equivalent),
// plus the reference-list lookups (genres/studios/networks/keywords) the
// editor's filter-value picker needs. Every call goes through api()
// (src/api/client.ts) so it inherits the session cookie and the global 401 →
// re-boot session-expiry fallback. Request/response shapes are the generated
// DTOs (@dto), never hand-duplicated.

import { api } from "./client";
import type {
  Genre,
  Keyword,
  Network,
  Slider,
  SliderReorderRequest,
  SliderUpsertRequest,
  Studio,
} from "@dto";
import type { Mode } from "./discover";

export type {
  Genre,
  Keyword,
  Network,
  Slider,
  SliderUpsertRequest,
  Studio,
};

// FILTER_TYPES mirrors internal/discoversliders.FilterType's fixed enum,
// display order matches the fixed-feed/reference-lookup split below (fixed
// feeds first, then the four reference-list-backed types).
export const FILTER_TYPES = [
  "upcoming",
  "trending",
  "popular",
  "genre",
  "keyword",
  "studio",
  "network",
] as const;
export type FilterType = (typeof FILTER_TYPES)[number];

export const TARGETS = ["movie", "tv", "mixed"] as const;
export type SliderTarget = (typeof TARGETS)[number];

// FILTER_NEEDS_VALUE mirrors discoversliders.filterValueRequired exactly —
// the three fixed feeds take no filter_value; the other four are only valid
// paired with a resolved reference id (see discoversliders.Store's validate).
export const FILTER_NEEDS_VALUE: Record<FilterType, boolean> = {
  upcoming: false,
  trending: false,
  popular: false,
  genre: true,
  keyword: true,
  studio: true,
  network: true,
};

// FILTER_TYPE_LABELS is the human-readable label for each FilterType, shown
// in the editor's filter-type select and the slider list.
export const FILTER_TYPE_LABELS: Record<FilterType, string> = {
  upcoming: "Upcoming",
  trending: "Trending",
  popular: "Popular",
  genre: "Genre",
  keyword: "Keyword",
  studio: "Studio",
  network: "Network",
};

export function fetchSliders(): Promise<Slider[]> {
  return api<Slider[]>("/api/discover/sliders");
}

export function createSlider(body: SliderUpsertRequest): Promise<Slider> {
  return api<Slider>("/api/discover/sliders", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export function updateSlider(
  id: number,
  body: SliderUpsertRequest,
): Promise<Slider> {
  return api<Slider>(`/api/discover/sliders/${id}`, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export function deleteSlider(id: number): Promise<void> {
  return api<void>(`/api/discover/sliders/${id}`, { method: "DELETE" });
}

// reorderSliders sends the FULL new display order in one call — ids must
// cover every existing slider exactly once (Store.Reorder's requirement),
// never a partial/per-item bulk mutation.
export function reorderSliders(ids: number[]): Promise<void> {
  const body: SliderReorderRequest = { ids };
  return api<void>("/api/discover/sliders/reorder", {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// fetchGenres backs the genre picker — Movies/Series only (Adult has no TMDB
// genre concept), mirroring the movie-vs-TV genre-list split
// discoverGenresHandler already makes for the Discover screen itself.
export function fetchGenres(mode: Exclude<Mode, "adult">): Promise<Genre[]> {
  return api<Genre[]>(`/api/modes/${mode}/discover/genres`);
}

export function fetchStudios(): Promise<Studio[]> {
  return api<Studio[]>("/api/discover/studios");
}

export function fetchNetworks(): Promise<Network[]> {
  return api<Network[]>("/api/discover/networks");
}

export function fetchKeywords(query: string): Promise<Keyword[]> {
  return api<Keyword[]>(`/api/discover/keywords?q=${encodeURIComponent(query)}`);
}
