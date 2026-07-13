// Purge workflow data access (Stage 3). Ported verbatim from the vanilla-JS
// frontend (internal/web/static/index.html's renderPurge). Purge is the staged
// scan→propose→apply DELETE queue keyed off an editable tag allowlist: Scan
// matches each mode's allowlist against every tracked item's tags and enqueues
// one delete proposal per match, the operator reviews the queue, and every
// mutating action — Apply (Delete) / Dismiss on a proposal, and add/remove on
// the allowlist itself — acts on EXACTLY ONE item. No bulk path exists anywhere
// (proposals OR allowlist), matching the old frontend and the project's
// no-bulk-action invariant.
//
// Unlike Rename, Purge has NO re-pick / give-back / draft: a proposal is only
// ever Applied (delete the file + drop the record) or Dismissed. Its proposal
// wire shape is the shared @dto Proposal, of which Purge reads only
// Title/Status/RootFolderPath/Reason. The allowlist crosses the wire as a bare
// string[] (no named response DTO); the add body is the generated
// AllowlistAddRequest.
//
// Every call goes through api() (src/api/client.ts) so it inherits the session
// cookie and the global 401 → re-boot session-expiry fallback.

import { api } from "./client";
import type { Proposal, AllowlistAddRequest } from "@dto";
import type { Mode, ProposalStatus } from "./discover";

export type { Proposal };
// ProposalStatus is the single shared narrowing (see discover.ts); re-exported
// so screens keep importing it from their workflow's api module. Purge only
// ever produces pending, then applied/dismissed.
export type { ProposalStatus };

// scanPurge runs Purge's propose-phase for one mode: the backend matches the
// mode's allowlist against every tracked item's tags and replaces the mode's
// pending queue with what it finds. One POST, no body; the caller re-fetches.
export function scanPurge(mode: Mode): Promise<void> {
  return api<void>(`/api/modes/${mode}/purge/scan`, { method: "POST" });
}

// fetchPurgeProposals lists the Purge review queue for one mode (every status;
// only pending rows expose actions).
export function fetchPurgeProposals(mode: Mode): Promise<Proposal[]> {
  return api<Proposal[]>(`/api/modes/${mode}/purge/proposals`);
}

// applyProposal commits exactly one pending proposal — for Purge this deletes
// the tracked item's file and drops its library record. The single destructive
// "do it" action; the empty body mirrors the vanilla frontend's applyProposal.
// Defined locally (not imported from rename.ts) so Purge stays fully
// self-contained on the shared /api/proposals route.
export function applyProposal(id: number): Promise<unknown> {
  return api(`/api/proposals/${id}/apply`, {
    method: "POST",
    body: JSON.stringify({}),
  });
}

// dismissProposal drops one proposal from the queue without deleting anything.
export function dismissProposal(id: number): Promise<unknown> {
  return api(`/api/proposals/${id}/dismiss`, { method: "POST" });
}

// fetchAllowlist returns one mode's current Purge tag allowlist — a bare array
// of tag names (no wrapping object).
export function fetchAllowlist(mode: Mode): Promise<string[]> {
  return api<string[]>(`/api/modes/${mode}/purge/allowlist`);
}

// addAllowlistTag adds exactly one tag rule. Adding a tag already present is
// not an error server-side.
export function addAllowlistTag(mode: Mode, tag: string): Promise<unknown> {
  const body: AllowlistAddRequest = { tag };
  return api(`/api/modes/${mode}/purge/allowlist`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// removeAllowlistTag removes exactly one tag rule (path-only, no body).
export function removeAllowlistTag(mode: Mode, tag: string): Promise<unknown> {
  return api(`/api/modes/${mode}/purge/allowlist/${encodeURIComponent(tag)}`, {
    method: "DELETE",
  });
}
