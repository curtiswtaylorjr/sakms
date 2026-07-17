// Collections data access — wraps GET /api/modes/movies/collections, which
// returns all library_collections rows with a count of movies in each.
// Movies-only: the backend 400s for any other mode (Series/Adult have no
// TMDB collection concept).

import { api } from "./client";
import type { CollectionSummary } from "@dto";

export type { CollectionSummary };

// fetchCollections lists every TMDB collection that has at least one tracked
// movie, sorted by name, with a count of tracked movies per collection.
export function fetchCollections(): Promise<CollectionSummary[]> {
  return api<CollectionSummary[]>("/api/modes/movies/collections");
}
