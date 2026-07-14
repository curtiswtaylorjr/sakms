// Trakt data access — backs the Settings "Trakt" connection section (OAuth
// device-code flow) and Discover's Trakt Watchlist row (task #8).
//
// PLACEHOLDER CONTRACT (task #5, owned by worker-1, is still in progress as of
// this writing — no `internal/apidto` Trakt types exist yet). Every path and
// type below is a proposal sent to worker-1 (not yet confirmed), modeled
// directly off worker-2's internal/trakt package (Session/Store/Client):
// TraktStatusResponse mirrors Store.Configured + Tokens.Linked + ExpiresAt;
// TraktCredentialsRequest mirrors Store.SaveCredentials(clientID, *string);
// TraktDeviceStartResponse mirrors trakt.DeviceCode (device_code itself stays
// server-side, never sent to the client); TraktDevicePollResponse wraps one
// Client.PollDeviceToken attempt (not the blocking PollUntilToken);
// TraktWatchlistItem mirrors trakt.WatchlistItem exactly.
//
// THIS FILE IS THE ONLY PLACE THESE TYPES/PATHS ARE DEFINED — once task #5
// lands real `@dto` exports and real routes, only this file needs to change
// (swap the local interfaces below for `@dto` imports, adjust paths if they
// differ from the proposal). No other file should hand-duplicate these shapes.
//
// Three-state secret convention (same as ConnectionUpsertRequest.apiKey /
// Guardrail #5): clientSecret is omitted entirely to preserve the stored
// secret, sent as "" to clear it, non-empty to set it. NEVER send null.

import { api } from "./client";

// --- Placeholder types (see file doc comment above) -------------------------

export interface TraktStatusResponse {
  configured: boolean;
  linked: boolean;
  clientId?: string; // pre-fills the Settings form, same as ConnectionSummary.url — never the secret
  tokenExpiresAt?: string;
}

export interface TraktCredentialsRequest {
  clientId: string;
  clientSecret?: string;
}

export interface TraktDeviceStartResponse {
  userCode: string;
  verificationUrl: string;
  expiresIn: number;
  interval: number;
}

export interface TraktDevicePollResponse {
  linked: boolean;
  pending: boolean;
}

export interface TraktWatchlistItem {
  type: string; // "movie" | "show"
  title: string;
  year: number;
  tmdbId: number;
}

// --- Calls -------------------------------------------------------------------

export function fetchTraktStatus(): Promise<TraktStatusResponse> {
  return api<TraktStatusResponse>("/api/trakt/status");
}

// buildTraktCredentialsBody is the three-state secret gate for Trakt's
// client_secret, same shape as settings.ts's buildConnectionUpsertBody — an
// untouched secret field must be OMITTED, never sent as "".
export function buildTraktCredentialsBody(input: {
  clientId: string;
  secretTouched: boolean;
  secretValue: string;
}): TraktCredentialsRequest {
  const body: TraktCredentialsRequest = { clientId: input.clientId };
  if (input.secretTouched) body.clientSecret = input.secretValue;
  return body;
}

export function saveTraktCredentials(
  body: TraktCredentialsRequest,
): Promise<void> {
  return api<void>("/api/trakt/credentials", {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export function startTraktDeviceFlow(): Promise<TraktDeviceStartResponse> {
  return api<TraktDeviceStartResponse>("/api/trakt/device/start", {
    method: "POST",
  });
}

// pollTraktDevice makes one poll attempt. Callers drive their own interval
// timer (device.interval seconds, per Trakt's guidance) rather than blocking —
// see TraktConnectionSection in Settings.tsx.
export function pollTraktDevice(): Promise<TraktDevicePollResponse> {
  return api<TraktDevicePollResponse>("/api/trakt/device/poll", {
    method: "POST",
  });
}

export function disconnectTrakt(): Promise<void> {
  return api<void>("/api/trakt/disconnect", { method: "POST" });
}

export function fetchTraktWatchlist(): Promise<TraktWatchlistItem[]> {
  return api<TraktWatchlistItem[]>("/api/trakt/watchlist");
}
