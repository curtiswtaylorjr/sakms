// Rename workflow data access (Stage 3). The staged scan→propose→apply review
// queue: Scan enqueues proposals server-side, the operator reviews each row, and
// every mutating action (Apply / Give back / Re-pick / Dismiss) acts on EXACTLY
// ONE already-listed proposal — no bulk path exists, matching the vanilla-JS
// frontend this ports (internal/web/static/index.html's renderRename). Every
// call goes through api() (src/api/client.ts) so it inherits the session cookie
// and the global 401 → re-boot session-expiry fallback. Response shapes are the
// generated DTOs (@dto), never hand-duplicated (plan Guardrail #4).

import { api } from "./client";
import type { DiscoverItem, Proposal, RepickRequest } from "@dto";
import type { Mode, ProposalStatus } from "./discover";

export type { Proposal, RepickRequest };
// ProposalStatus is the single shared narrowing (see discover.ts); re-exported
// so screens keep importing it from their workflow's api module.
export type { ProposalStatus };

// scanRename kicks off a fresh scan for one mode. The backend replaces the
// mode's pending/unmatched queue with what it finds; the caller then re-fetches
// the proposal list. One POST, no body.
export function scanRename(mode: Mode): Promise<void> {
  return api<void>(`/api/modes/${mode}/rename/scan`, { method: "POST" });
}

// fetchProposals lists the Rename review queue for one mode (every status —
// applied/dismissed rows show too, with their actions gated off by status).
export function fetchProposals(mode: Mode): Promise<Proposal[]> {
  return api<Proposal[]>(`/api/modes/${mode}/rename/proposals`);
}

// applyProposal commits exactly one pending proposal — the single mutating
// "do it" action. The empty body mirrors the vanilla frontend's applyProposal
// (an optional candidate-pick body is a Dedup concern, unused by Rename).
export function applyProposal(id: number): Promise<unknown> {
  return api(`/api/proposals/${id}/apply`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

// dismissProposal drops one proposal from the queue without acting on the file.
export function dismissProposal(id: number): Promise<unknown> {
  return api(`/api/proposals/${id}/dismiss`, { method: "POST" });
}

// submitDraft ("Give back") hands one unmatched proposal back to the community
// databases as a draft. Succeeds once per proposal — the server records a
// draftId so it can't be submitted twice (the button then renders "Given back"
// and disables).
export function submitDraft(id: number): Promise<unknown> {
  return api(`/api/proposals/${id}/submit-draft`, { method: "POST" });
}

// tmdbSearch backs Re-pick's search box: a thin TMDB title search (Movies/Series
// only). Results ARE tmdb.Item, whose wire shape is exactly DiscoverItem, so the
// generated DiscoverItem type is reused rather than duplicated.
export function tmdbSearch(mode: Mode, query: string): Promise<DiscoverItem[]> {
  return api<DiscoverItem[]>(
    `/api/modes/${mode}/tmdb-search?q=${encodeURIComponent(query)}`,
  );
}

// repickProposal re-points one proposal at a NEW TMDB match the operator chose
// from tmdbSearch's results — never the proposal's current tmdbId.
export function repickProposal(
  id: number,
  req: RepickRequest,
): Promise<unknown> {
  return api(`/api/proposals/${id}/repick`, {
    method: "POST",
    body: JSON.stringify(req),
  });
}
