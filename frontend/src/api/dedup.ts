// Dedup workflow data access (Stage 3). Ported verbatim from the vanilla-JS
// frontend (internal/web/static/index.html's renderDedup). Dedup is the staged
// scan→propose→apply DEDUPLICATION queue: Scan finds content identified twice
// (an already-tracked copy plus one or more orphan files that resolve to the
// SAME identity) and enqueues one proposal PER DUPLICATE GROUP, each carrying
// the group's candidate files with the quality winner pre-flagged. The operator
// reviews each group and resolves EXACTLY ONE group per Apply click — there is
// no apply-all/resolve-all path across the queue, matching the old frontend and
// the project's no-bulk-action invariant.
//
// Structurally DIFFERENT from Rename and Purge (verified against the old
// frontend, do NOT "align" them): a Rename/Purge proposal is a single flat row
// acted on with an empty body; a Dedup proposal is a GROUP of candidate files
// (proposal.candidates), and Apply carries a body identifying which candidate to
// keep. What a "duplicate" is differs by MODE at Scan time only — Movies group
// by TMDB id, Series by (show, season, episode), Adult by (box, scene_id) — but
// the wire shape and this client are mode-agnostic: every mode returns the same
// group-of-candidates Proposal and resolves through the same /api/proposals
// route (proposal.workflow, set at Scan time, dispatches to the right backend
// Apply — dedup.ApplyLibrary / ApplyLibrarySeries / ApplyLibraryAdult).
//
// Every call goes through api() (src/api/client.ts) so it inherits the session
// cookie and the global 401 → re-boot session-expiry fallback. Response/request
// shapes are the generated DTOs (@dto), never hand-duplicated (plan Guardrail #4).

import { api } from "./client";
import type { Candidate, DedupApplyRequest, Proposal } from "@dto";
import type { Mode, ProposalStatus } from "./discover";

export type { Candidate, Proposal };
// ProposalStatus is the single shared narrowing (see discover.ts); re-exported
// so screens keep importing it from their workflow's api module. Dedup only
// ever produces pending, then applied/dismissed.
export type { ProposalStatus };

// scanDedup runs Dedup's propose-phase for one mode: the backend scans the
// mode's library root, groups duplicates, and replaces the mode's pending queue
// with what it finds. One POST, no body; the caller re-fetches.
export function scanDedup(mode: Mode): Promise<void> {
  return api<void>(`/api/modes/${mode}/dedup/scan`, { method: "POST" });
}

// fetchDedupProposals lists the Dedup review queue for one mode (every status;
// only pending groups expose actions). Each proposal carries a `candidates`
// group with one `winner` flagged.
export function fetchDedupProposals(mode: Mode): Promise<Proposal[]> {
  return api<Proposal[]>(`/api/modes/${mode}/dedup/proposals`);
}

// applyKeep resolves one duplicate group by KEEPING candidate `keepIndex` (an
// array index into that proposal's `candidates`, in received order) and deleting
// every other file in the group. keepIndex is threaded through as a real number
// even when it is 0 — the group's winner may sit at index 0, or the operator may
// pick candidate 0, and dropping a literal 0 would make the backend silently
// fall back to its auto-winner and delete the wrong file (dedup.ApplyLibrary
// indexes p.Candidates[keepIndex] directly). Resolves exactly one proposal id.
export function applyKeep(id: number, keepIndex: number): Promise<unknown> {
  const body: DedupApplyRequest = { keepIndex };
  return api(`/api/proposals/${id}/apply`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// applyKeepAll resolves one duplicate group by keeping EVERY candidate and
// deleting nothing — the conservative "these aren't really duplicates" escape
// hatch ("Keep All"). keepIndex is omitted entirely so the backend reads it as
// nil, not 0.
export function applyKeepAll(id: number): Promise<unknown> {
  const body: DedupApplyRequest = { keepAll: true };
  return api(`/api/proposals/${id}/apply`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// dismissProposal drops one duplicate group from the queue without deleting
// anything (leaves both copies on disk, unresolved).
export function dismissProposal(id: number): Promise<unknown> {
  return api(`/api/proposals/${id}/dismiss`, { method: "POST" });
}
