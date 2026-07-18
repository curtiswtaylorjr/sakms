// Dedup — the staged scan→propose→apply DEDUPLICATION queue, ported verbatim
// from the vanilla-JS frontend (internal/web/static/index.html's renderDedup).
// Layout, top to bottom: Scan button → one CARD per duplicate group. Each card
// shows the group's title + status pill and a candidate table (Keep radio /
// Label / Path / Resolution / Codec / Bitrate) with the quality winner
// pre-selected; a pending card's actions are Apply (keep the selected radio,
// delete the rest) / Keep All (keep everything) / Dismiss.
//
// Structurally DIFFERENT from Rename and Purge (verified against the old
// frontend, do NOT "align" them). Rename/Purge render one flat row per proposal;
// Dedup renders a GROUP of candidate files per proposal, because a duplicate is
// inherently a set, not a single item. The keeper-vs-duplicate distinction — the
// whole point of Dedup — is the `winner` flag: exactly one candidate per group is
// the "tracked copy" the group keeps, shown as the pre-checked Keep radio; every
// other row is a duplicate the Apply removes. What a duplicate MEANS differs by
// mode (Movies: TMDB id; Series: show/season/episode; Adult: box/scene_id), but
// that lives entirely in the backend Scan — this view is identical across modes,
// exactly as the single mode-agnostic renderDedup was.
//
// Unit of action is the GROUP: each pending card resolves with its OWN explicit
// Apply/Keep All — one group per click. (Removing multiple losers inside one
// group is that one group's atomic resolution, verbatim backend behavior —
// dedup.ApplyLibrary* deletes every non-kept candidate in a single call; there
// is no single-candidate removal endpoint.)
//
// Bulk apply — the one bounded exception to the project's original "one item at
// a time, no apply-everything" rule (a deliberate, documented reversal; see
// ROADMAP.md's Bulk apply entry and the top-level CLAUDE.md's amended
// engineering-conventions note). Pending cards carry a selection checkbox
// alongside their existing keep-radio table, plus a select-all-pending toggle;
// with a non-empty selection an "Apply Selected (N)" button posts one
// apply-batch covering exactly those already-reviewed groups, applied
// sequentially server-side with skip-and-continue. Per group the batch sends an
// explicit keepIndex ONLY when the operator changed that group's radio (an
// explicit keepSel override); otherwise the item omits keepIndex and the backend
// keeps its own auto-winner. This is NOT a queue-wide resolve-all and does not
// change how any single group still resolves via its own Apply.
//
// keepIndex is an ARRAY INDEX into the proposal's `candidates` in received
// order. Candidates are rendered in exactly that order and NEVER sorted — the
// index sent must line up with proposals.Proposal.Candidates or Apply deletes
// the wrong file (dedup.ApplyLibrary indexes p.Candidates[keepIndex] directly).

import {
  type Component,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import type { ApplyBatchItem, ApplyBatchResponse } from "@dto";
import type { Mode } from "../api/discover";
import {
  type Candidate,
  type Proposal,
  type ProposalStatus,
  applyBatch,
  applyKeep,
  applyKeepAll,
  dismissProposal,
  fetchDedupProposals,
  scanDedup,
} from "../api/dedup";
import {
  BatchResultSummary,
  Button,
  ErrorText,
  ModeTabs,
  Muted,
  StatusPill,
} from "../components/ui";
import { useBulkSelection, useWorkflowActions } from "./workflowHooks";

// winnerIndex returns the index of the group's flagged keeper, defaulting to 0
// when none is flagged (mirrors the backend's own winnerIndex fallback).
const winnerIndex = (candidates: Candidate[]): number => {
  const i = candidates.findIndex((c) => c.winner);
  return i >= 0 ? i : 0;
};

// fmtBitrate renders bitRate as "N kbps" (verbatim from the old frontend's
// fmtBytes) — blank for a missing/zero bitrate.
const fmtBitrate = (bitRate: number | undefined): string =>
  bitRate ? `${Math.round(bitRate / 1000)} kbps` : "";

// fmtSimilarity renders a phash similarity score [0.0–1.0] as a percentage
// string, e.g. "95% similar".
const fmtSimilarity = (s: number): string => `${Math.round(s * 100)}% similar`;

// similarityLabel returns a short confidence descriptor for the given phash
// similarity score. Matches the thresholds from the phash-primary spec.
const similarityLabel = (s: number): string => {
  if (s >= 0.9) return "high confidence duplicate";
  if (s >= 0.7) return "likely duplicate";
  return "possible duplicate — review carefully";
};

// DedupView is one mode's duplicate-group review queue. Keyed on props.mode so
// the resource refetches when the shell switches tabs.
const DedupView: Component<{ mode: Mode }> = (props) => {
  const [proposals, { refetch }] = createResource(
    () => props.mode,
    (m) => fetchDedupProposals(m),
  );
  // keepSel maps a proposal id → the operator's chosen keep index. Absent means
  // "use the group's flagged winner" (the pre-checked radio). Cleared on refetch
  // and mode switch so a stale selection never leaks onto a re-scanned queue.
  const [keepSel, setKeepSel] = createSignal<Record<number, number>>({});
  // Bulk selection of Pending groups + the last "Apply Selected" outcome. The
  // selection and keepSel clear together on mode-change/scan/act; batchResult
  // persists past act (so its own summary survives) but clears on the next
  // scan, mode-change, or new batch.
  const selection = useBulkSelection();
  const [batchResult, setBatchResult] = createSignal<ApplyBatchResponse | null>(
    null,
  );

  // Both scan and act clear keepSel and the selection on success — stale radio
  // selections or checkbox picks must not survive a queue refresh or mode switch
  // in either direction. act clears the selection but NOT batchResult, so a
  // batch's own summary survives the act that produced it.
  const { actionError, scanning, scan, act } = useWorkflowActions(
    () => props.mode,
    {
      resetOnModeChange: () => {
        setKeepSel({});
        selection.clear();
        setBatchResult(null);
      },
      scanFn: scanDedup,
      resetAfterScan: () => {
        setKeepSel({});
        selection.clear();
        setBatchResult(null);
      },
      resetAfterAct: () => {
        setKeepSel({});
        selection.clear();
      },
      refetch,
    },
  );

  // selectedKeep is the effective keep index for a group: the operator's radio
  // choice if made, else the group's flagged winner. Always a real number
  // (including 0) so applyKeep never drops a literal-0 index.
  const selectedKeep = (p: Proposal): number => {
    const chosen = keepSel()[p.id];
    return chosen ?? winnerIndex(p.candidates ?? []);
  };

  // pendingIds are the groups selectable/batchable — only Pending cards resolve.
  const pendingIds = (): number[] =>
    (proposals() ?? []).filter((p) => p.status === "pending").map((p) => p.id);
  const allPendingSelected = (): boolean => {
    const ids = pendingIds();
    return ids.length > 0 && ids.every((id) => selection.has(id));
  };
  const toggleSelectAll = (): void => {
    if (allPendingSelected()) selection.clear();
    else selection.selectAll(pendingIds());
  };
  const titleOf = (id: number): string => {
    const p = (proposals() ?? []).find((x) => x.id === id);
    return p ? p.title || p.sourceName || "" : "";
  };
  // applySelected posts ONE apply-batch for the selected Pending groups. Per
  // group, keepIndex rides along ONLY when the operator overrode that group's
  // radio (keepSel has an explicit entry) — otherwise the item omits keepIndex
  // and the backend keeps its own auto-winner. keepSel()[id] may legitimately be
  // 0, so the presence check is `!== undefined`, never a falsy test.
  const applySelected = (): void => {
    const overrides = keepSel();
    const items: ApplyBatchItem[] = [...selection.selected()].map((id) => {
      const chosen = overrides[id];
      return chosen === undefined ? { id } : { id, keepIndex: chosen };
    });
    if (items.length === 0) return;
    setBatchResult(null);
    void act(async () => {
      setBatchResult(await applyBatch(items));
    });
  };

  return (
    <div>
      <div class="flex items-center gap-3">
        <Button variant="primary" onClick={() => void scan(props.mode)} disabled={scanning()}>
          {scanning() ? "Scanning…" : "Scan"}
        </Button>
        <Show when={pendingIds().length > 0}>
          <label class="flex items-center gap-2 text-sm text-muted">
            <input
              type="checkbox"
              aria-label="Select all pending"
              checked={allPendingSelected()}
              onChange={toggleSelectAll}
            />
            Select all pending
          </label>
        </Show>
        <Show when={selection.size() > 0}>
          <Button variant="primary" onClick={applySelected}>
            Apply Selected ({selection.size()})
          </Button>
        </Show>
      </div>

      <Show when={actionError()}>
        <ErrorText>{actionError()}</ErrorText>
      </Show>
      <Show when={batchResult()}>
        {(res) => <BatchResultSummary result={res()} titleOf={titleOf} />}
      </Show>

      <Show when={proposals.error}>
        <ErrorText>{(proposals.error as Error)?.message}</ErrorText>
      </Show>
      <Show
        when={!proposals.loading}
        fallback={<Muted class="mt-4">Loading…</Muted>}
      >
        <Show
          when={proposals() && proposals()!.length > 0}
          fallback={
            <Muted class="mt-4">
              No duplicate groups yet — click Scan.
            </Muted>
          }
        >
          <div class="mt-4 flex flex-col gap-4">
            <For each={proposals()}>
              {(p) => {
                const status = () => p.status as ProposalStatus;
                const candidates = () => p.candidates ?? [];
                const radioName = `keep-${p.id}`;
                return (
                  <div class="rounded-xl border border-border bg-surface p-4">
                    <div class="flex items-center gap-2">
                      <Show when={status() === "pending"}>
                        <input
                          type="checkbox"
                          aria-label={`Select ${p.title || p.sourceName || ""}`}
                          checked={selection.has(p.id)}
                          onChange={() => selection.toggle(p.id)}
                        />
                      </Show>
                      <strong class="text-fg">
                        {p.title || p.sourceName || ""}
                      </strong>
                      <StatusPill status={p.status} />
                      <Show when={(p.pHashSimilarity ?? 0) > 0}>
                        <span class="text-xs text-muted">
                          {fmtSimilarity(p.pHashSimilarity!)} ·{" "}
                          {similarityLabel(p.pHashSimilarity!)}
                        </span>
                      </Show>
                    </div>
                    <div class="mt-3 overflow-x-auto">
                      <table class="w-full text-left text-sm">
                        <thead>
                          <tr class="border-b border-border text-xs uppercase tracking-wide text-muted">
                            <th class="px-2 py-2 font-medium">Keep</th>
                            <th class="px-2 py-2 font-medium">Label</th>
                            <th class="px-2 py-2 font-medium">Path</th>
                            <th class="px-2 py-2 font-medium">Resolution</th>
                            <th class="px-2 py-2 font-medium">Codec</th>
                            <th class="px-2 py-2 font-medium">Bitrate</th>
                          </tr>
                        </thead>
                        <tbody>
                          <For each={candidates()}>
                            {(c, i) => (
                              <tr class="border-b border-border/60 align-top">
                                <td class="px-2 py-2">
                                  <input
                                    type="radio"
                                    name={radioName}
                                    value={i()}
                                    checked={selectedKeep(p) === i()}
                                    aria-label={`Keep ${c.label}`}
                                    onChange={() =>
                                      setKeepSel((prev) => ({
                                        ...prev,
                                        [p.id]: i(),
                                      }))
                                    }
                                  />
                                </td>
                                <td class="px-2 py-2 text-fg">{c.label}</td>
                                <td class="px-2 py-2 text-muted">{c.path}</td>
                                <td class="px-2 py-2 text-muted">
                                  {c.resolution ? `${c.resolution}p` : ""}
                                </td>
                                <td class="px-2 py-2 text-muted">
                                  {c.codec || ""}
                                </td>
                                <td class="px-2 py-2 text-muted">
                                  {fmtBitrate(c.bitRate)}
                                </td>
                              </tr>
                            )}
                          </For>
                        </tbody>
                      </table>
                    </div>
                    <Show when={status() === "pending"}>
                      <div class="mt-3 flex flex-wrap gap-2">
                        <Button
                          variant="primary"
                          onClick={() =>
                            void act(() => applyKeep(p.id, selectedKeep(p)))
                          }
                        >
                          Apply
                        </Button>
                        <Button
                          onClick={() => void act(() => applyKeepAll(p.id))}
                        >
                          Keep All
                        </Button>
                        <Button
                          class="!bg-danger !text-accent-fg"
                          onClick={() =>
                            void act(() => dismissProposal(p.id))
                          }
                        >
                          Dismiss
                        </Button>
                      </div>
                    </Show>
                  </div>
                );
              }}
            </For>
          </div>
        </Show>
      </Show>
    </div>
  );
};

// Dedup is the mode-switching shell: tab bar (Movies/Series/Adult) over the
// matching duplicate-group review queue.
export const Dedup: Component = () => {
  const [mode, setMode] = createSignal<Mode>("movies");
  return (
    <div>
      <ModeTabs current={mode} onSelect={setMode} />
      <DedupView mode={mode()} />
    </div>
  );
};
