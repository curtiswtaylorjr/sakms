// Rename — the staged scan→propose→apply review queue, ported from the
// vanilla-JS frontend (internal/web/static/index.html's renderRename), with one
// deliberate enhancement on top of the port: mode-specific columns (Wade-approved
// follow-up, see .omc/handoffs/stage-3-rename.md — the old frontend's single
// generic table never surfaced these, and an earlier wave correctly declined to
// add them without an explicit decision). Scan enqueues proposals; the operator
// reviews a table of them and acts on each row via its own single-item button.
//
// Bulk apply — the one bounded exception to the project's original "one item at
// a time, no apply-everything" rule (a deliberate, documented reversal; see
// ROADMAP.md's Bulk apply entry and the top-level CLAUDE.md's amended
// engineering-conventions note). Pending rows carry a selection checkbox plus a
// select-all-pending header toggle; with a non-empty selection an "Apply
// Selected (N)" button posts one apply-batch request covering exactly those
// already-reviewed rows, which the backend applies sequentially with
// skip-and-continue and reports per item. This is NOT a queue-wide apply-all,
// and it does not change how any single row still applies one at a time via its
// own Apply button.
//
// Table shape:
//   - Shared columns, every mode: Source / Title / Status / Root Folder /
//     Reason / Actions.
//   - Movies additionally show Year.
//   - Series additionally show Year / Season / Episode.
//   - Adult additionally show Studio / Date / PHash (truncated with a title
//     attribute for the full value — proposals.Proposal's PHash is a long
//     scheme-tagged hex string, not something to render in full inline).
//   Extra columns are only ever ADDED for a mode, never removed from the
//   shared set — Source/Title/Status/Root Folder/Reason/Actions stay present
//   and in the same relative order across all three modes.
//   - Apply shows on a `pending` row; Give back on an `unmatched` row (any mode,
//     even though it is Adult-give-back-semantic); Re-pick on pending/unmatched
//     for Movies/Series only; Dismiss on pending/unmatched.
//   - Re-pick opens a single shared search panel below the table, auto-searches
//     the prefilled title on open, and sends the NEWLY chosen tmdbId (never the
//     proposal's current one).

import {
  type Component,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import type { Mode } from "../api/discover";
import type { ApplyBatchItem, ApplyBatchResponse } from "@dto";
import {
  type Proposal,
  type ProposalStatus,
  applyBatch,
  applyProposal,
  dismissProposal,
  fetchProposals,
  repickProposal,
  scanRename,
  submitDraft,
  tmdbSearch,
} from "../api/rename";
import {
  BatchResultSummary,
  Button,
  ErrorText,
  ModeTabs,
  Muted,
  StatusPill,
  yearOf,
} from "../components/ui";
import { useBulkSelection, useWorkflowActions } from "./workflowHooks";

// shortHash renders the PHash column value — the full scheme-tagged hash is
// too long to usefully show inline, so the cell shows a short prefix and the
// full value lives in the title attribute (hover) for anyone who needs it.
function shortHash(hash: string): string {
  return hash.length > 12 ? `${hash.slice(0, 12)}…` : hash;
}

// episodeDisplay renders the Episode column: a plain number for the
// ordinary single-episode case, or "N-M" (e.g. "1-2") for a logical-
// episode-split proposal (extraEpisodeNumbers non-empty) — so an operator
// sees BOTH episodes Apply will actually create before approving it,
// rather than only the primary number with the bundled one silently
// implied.
function episodeDisplay(
  episodeNumber?: number,
  extraEpisodeNumbers?: number[],
): string {
  if (episodeNumber == null) return "";
  if (!extraEpisodeNumbers || extraEpisodeNumbers.length === 0) {
    return String(episodeNumber);
  }
  const last = extraEpisodeNumbers[extraEpisodeNumbers.length - 1];
  return `${episodeNumber}-${last}`;
}

// RepickPanel is the shared Movies/Series re-pick search area — one instance
// below the table, opened against whichever proposal's Re-pick was clicked. It
// auto-searches the prefilled query on mount (matching the old openRepick's
// immediate runSearch()), and each result offers a single "Use this" that
// re-points the proposal at that NEW match, then closes and refreshes.
const RepickPanel: Component<{
  mode: "movies" | "series";
  proposal: Proposal;
  onDone: () => void;
  onCancel: () => void;
}> = (props) => {
  const [query, setQuery] = createSignal(
    props.proposal.title || props.proposal.sourceName || "",
  );
  const [submitted, setSubmitted] = createSignal(query());
  const [results] = createResource(submitted, async (q) => {
    if (!q.trim()) return [];
    return tmdbSearch(props.mode, q);
  });
  const [error, setError] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const use = async (id: number, title: string, year?: number) => {
    setError("");
    setBusy(true);
    try {
      await repickProposal(props.proposal.id, { tmdbId: id, title, year });
      props.onDone();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div class="mt-4 rounded-xl border border-border bg-surface p-4">
      <h4 class="text-sm font-semibold text-fg">
        Re-pick match for “{props.proposal.sourceName}”
      </h4>
      <Show when={props.proposal.title}>
        <Muted class="mt-1">
          Currently matched: {props.proposal.title}
          {props.proposal.year ? ` (${props.proposal.year})` : ""}
        </Muted>
      </Show>
      <form
        class="mt-2 flex items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setSubmitted(query());
        }}
      >
        <input
          class="w-80 max-w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent"
          value={query()}
          onInput={(e) => setQuery(e.currentTarget.value)}
          aria-label="Re-pick search query"
        />
        <Button type="submit">Search</Button>
        <Button onClick={props.onCancel}>Cancel</Button>
      </form>
      <Show when={error()}>
        <ErrorText>{error()}</ErrorText>
      </Show>
      <div class="mt-3">
        <Show when={results.error}>
          <ErrorText>{(results.error as Error)?.message}</ErrorText>
        </Show>
        <Show when={!results.loading} fallback={<Muted>Searching…</Muted>}>
          <Show
            when={results() && results()!.length > 0}
            fallback={<Muted>No results.</Muted>}
          >
            <ul class="flex flex-col gap-1">
              <For each={results()}>
                {(item) => {
                  const y = yearOf(item.releaseDate);
                  return (
                    <li class="flex items-center gap-3 rounded-md border border-border bg-surface-2 p-2">
                      <span class="min-w-0 flex-1 truncate text-sm text-fg">
                        {item.title}
                        {y ? ` (${y})` : ""} — TMDB #{item.id}
                      </span>
                      <Button
                        variant="primary"
                        disabled={busy()}
                        onClick={() => use(item.id, item.title, y)}
                      >
                        Use this
                      </Button>
                    </li>
                  );
                }}
              </For>
            </ul>
          </Show>
        </Show>
      </div>
    </div>
  );
};

// RenameQueue is one mode's review table + actions. Keyed on props.mode so the
// resource refetches when the shell switches tabs.
const RenameQueue: Component<{ mode: Mode }> = (props) => {
  const [proposals, { refetch }] = createResource(
    () => props.mode,
    (m) => fetchProposals(m),
  );
  const [repickFor, setRepickFor] = createSignal<Proposal | null>(null);
  // Bulk selection of Pending rows + the last "Apply Selected" outcome. The
  // selection clears on mode-change/scan/act (stale ids must not survive a
  // re-fetched queue); batchResult persists past act (so the summary survives
  // its own apply) but is cleared on the next scan, mode-change, or new batch.
  const selection = useBulkSelection();
  const [batchResult, setBatchResult] = createSignal<ApplyBatchResponse | null>(
    null,
  );

  // Switching modes clears any open re-pick panel, stale action error, the
  // selection, and the previous batch summary — the old frontend rebuilt the
  // whole view on a mode change, which had this effect. scan does NOT close
  // repickFor (scan and act have independent post-success resets); only act
  // closes it. act clears the selection but NOT batchResult, so a batch's own
  // summary survives the act that produced it.
  const { actionError, setActionError, scanning, scan, act } = useWorkflowActions(
    () => props.mode,
    {
      resetOnModeChange: () => {
        setRepickFor(null);
        selection.clear();
        setBatchResult(null);
      },
      scanFn: scanRename,
      resetAfterScan: () => {
        selection.clear();
        setBatchResult(null);
      },
      resetAfterAct: () => {
        setRepickFor(null);
        selection.clear();
      },
      refetch,
    },
  );

  const isTitleMode = () => props.mode === "movies" || props.mode === "series";

  // pendingIds are the ids selectable/batchable — only Pending rows can Apply.
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
  // applySelected posts ONE apply-batch for the current selection (already-
  // reviewed Pending rows). Rename items carry only an id.
  const applySelected = (): void => {
    const items: ApplyBatchItem[] = [...selection.selected()].map((id) => ({
      id,
    }));
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

      <Show when={!proposals.loading} fallback={<Muted class="mt-4">Loading…</Muted>}>
        <Show
          when={proposals() && proposals()!.length > 0}
          fallback={<Muted class="mt-4">No proposals yet — click Scan.</Muted>}
        >
          <div class="mt-4 overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead>
                <tr class="border-b border-border text-xs uppercase tracking-wide text-muted">
                  <th class="px-2 py-2 font-medium">
                    <input
                      type="checkbox"
                      aria-label="Select all pending"
                      checked={allPendingSelected()}
                      disabled={pendingIds().length === 0}
                      onChange={toggleSelectAll}
                    />
                  </th>
                  <th class="px-2 py-2 font-medium">Source</th>
                  <th class="px-2 py-2 font-medium">Title</th>
                  <Show when={props.mode === "movies" || props.mode === "series"}>
                    <th class="px-2 py-2 font-medium">Year</th>
                  </Show>
                  <Show when={props.mode === "series"}>
                    <th class="px-2 py-2 font-medium">Season</th>
                    <th class="px-2 py-2 font-medium">Episode</th>
                  </Show>
                  <Show when={props.mode === "adult"}>
                    <th class="px-2 py-2 font-medium">Studio</th>
                    <th class="px-2 py-2 font-medium">Date</th>
                    <th class="px-2 py-2 font-medium">PHash</th>
                  </Show>
                  <th class="px-2 py-2 font-medium">Status</th>
                  <th class="px-2 py-2 font-medium">Root Folder</th>
                  <th class="px-2 py-2 font-medium">Reason</th>
                  <th class="px-2 py-2 font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                <For each={proposals()}>
                  {(p) => {
                    const status = p.status as ProposalStatus;
                    const actionable =
                      status === "pending" || status === "unmatched";
                    return (
                      <tr class="border-b border-border/60 align-top">
                        <td class="px-2 py-2">
                          <Show when={status === "pending"}>
                            <input
                              type="checkbox"
                              aria-label={`Select ${p.sourceName}`}
                              checked={selection.has(p.id)}
                              onChange={() => selection.toggle(p.id)}
                            />
                          </Show>
                        </td>
                        <td class="px-2 py-2 text-fg">{p.sourceName}</td>
                        <td class="px-2 py-2 text-fg">
                          {p.title || ""}
                          <Show when={(p.genres ?? []).length > 0}>
                            <div class="mt-0.5 flex flex-wrap gap-1">
                              <For each={p.genres}>
                                {(g) => (
                                  <span class="rounded bg-surface-2 px-1.5 py-0.5 text-xs text-muted">
                                    {g}
                                  </span>
                                )}
                              </For>
                            </div>
                          </Show>
                        </td>
                        <Show when={props.mode === "movies" || props.mode === "series"}>
                          <td class="px-2 py-2 text-muted">{p.year || ""}</td>
                        </Show>
                        <Show when={props.mode === "series"}>
                          <td class="px-2 py-2 text-muted">
                            {p.seasonNumber ?? ""}
                          </td>
                          <td class="px-2 py-2 text-muted">
                            {episodeDisplay(p.episodeNumber, p.extraEpisodeNumbers)}
                          </td>
                        </Show>
                        <Show when={props.mode === "adult"}>
                          <td class="px-2 py-2 text-muted">{p.studio || ""}</td>
                          <td class="px-2 py-2 text-muted">{p.date || ""}</td>
                          <td class="px-2 py-2 text-muted">
                            <Show when={p.phash}>
                              <span title={p.phash}>{shortHash(p.phash!)}</span>
                            </Show>
                          </td>
                        </Show>
                        <td class="px-2 py-2">
                          <StatusPill status={p.status} />
                        </td>
                        <td class="px-2 py-2 text-muted">
                          {p.rootFolderPath || ""}
                        </td>
                        <td class="px-2 py-2 text-muted">{p.reason || ""}</td>
                        <td class="px-2 py-2">
                          <div class="flex flex-wrap gap-1">
                            <Show when={status === "pending"}>
                              <Button
                                variant="primary"
                                onClick={() => act(() => applyProposal(p.id))}
                              >
                                Apply
                              </Button>
                            </Show>
                            <Show when={status === "unmatched"}>
                              <Button
                                disabled={!!p.draftId}
                                onClick={() => act(() => submitDraft(p.id))}
                              >
                                {p.draftId ? "Given back" : "Give back"}
                              </Button>
                            </Show>
                            <Show when={actionable && isTitleMode()}>
                              <Button onClick={() => setRepickFor(p)}>
                                Re-pick
                              </Button>
                            </Show>
                            <Show when={actionable}>
                              <Button
                                class="!bg-danger !text-accent-fg"
                                onClick={() => act(() => dismissProposal(p.id))}
                              >
                                Dismiss
                              </Button>
                            </Show>
                          </div>
                        </td>
                      </tr>
                    );
                  }}
                </For>
              </tbody>
            </table>
          </div>
        </Show>
      </Show>

      <Show when={isTitleMode() && repickFor()}>
        {(p) => (
          <RepickPanel
            mode={props.mode as "movies" | "series"}
            proposal={p()}
            onDone={() => {
              // Re-pick already committed inside RepickPanel; just close the
              // panel, clear any stale error, and refresh the queue.
              setActionError("");
              setRepickFor(null);
              void refetch();
            }}
            onCancel={() => setRepickFor(null)}
          />
        )}
      </Show>
    </div>
  );
};

// Rename is the mode-switching shell: tab bar (Movies/Series/Adult) over the
// matching review queue.
export const Rename: Component = () => {
  const [mode, setMode] = createSignal<Mode>("movies");
  return (
    <div>
      <ModeTabs current={mode} onSelect={setMode} />
      <RenameQueue mode={mode()} />
    </div>
  );
};
