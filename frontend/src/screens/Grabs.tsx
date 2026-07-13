// Grabs — a minimal per-mode list of everything SAK has sent to a download
// client (auto-grab and manual grab both land here). Its one Stage-2 job beyond
// listing is to surface the ADVISORY post-grab review flag
// (grab.flaggedForReview / flagReason): a flagged grab imported FINE — the flag
// only means the imported file's runtime looked inconsistent with its metadata
// and a human might want to eyeball it. The copy must never read as a failure.
//
// This is intentionally NOT a management screen (no bulk actions, no
// mutate-many affordances) — Guardrail #3 / the project's no-bulk invariant.

import {
  type Component,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import { type Grab, fetchGrabs } from "../api/grab";
import type { Mode } from "../api/discover";
import { ErrorText, Muted } from "../components/ui";

const MODES: { id: Mode; label: string }[] = [
  { id: "movies", label: "Movies" },
  { id: "series", label: "Series" },
  { id: "adult", label: "Adult" },
];

// ReviewBadge is the advisory flag — amber, explicitly worded so it doesn't
// read as an import failure. Rendered only when the grab is flagged.
const ReviewBadge: Component<{ grab: Grab }> = (props) => (
  <Show when={props.grab.flaggedForReview}>
    <span
      class="inline-block rounded-full bg-warn/20 px-2 py-0.5 text-[11px] font-medium text-warn"
      title={props.grab.flagReason || "flagged for a manual look"}
    >
      review — imported OK, runtime looks off
    </span>
  </Show>
);

const GrabRow: Component<{ grab: Grab }> = (props) => (
  <li class="flex items-center gap-3 rounded-md border border-border bg-surface p-3">
    <div class="min-w-0 flex-1">
      <div class="truncate text-sm text-fg" title={props.grab.title}>
        {props.grab.title}
      </div>
      <div class="truncate text-xs text-muted">
        {[props.grab.indexer, props.grab.protocol, props.grab.downloadClient]
          .filter(Boolean)
          .join(" · ")}
      </div>
      <Show when={props.grab.flaggedForReview && props.grab.flagReason}>
        <div class="mt-1 text-xs text-muted">{props.grab.flagReason}</div>
      </Show>
    </div>
    <span class="rounded-full bg-surface-2 px-2 py-0.5 text-[11px] text-muted">
      {props.grab.status}
    </span>
    <ReviewBadge grab={props.grab} />
  </li>
);

// Grabs is the mode-switching list shell.
export const Grabs: Component = () => {
  const [mode, setMode] = createSignal<Mode>("movies");
  const [grabs] = createResource(mode, (m) => fetchGrabs(m));

  return (
    <div>
      <div class="mb-4 flex gap-1">
        <For each={MODES}>
          {(m) => (
            <button
              type="button"
              class="rounded-md px-3 py-1.5 text-sm font-medium transition"
              classList={{
                "bg-accent text-accent-fg": mode() === m.id,
                "bg-surface-2 text-muted hover:text-fg": mode() !== m.id,
              }}
              onClick={() => setMode(m.id)}
            >
              {m.label}
            </button>
          )}
        </For>
      </div>
      <Show when={grabs.error}>
        <ErrorText>{(grabs.error as Error)?.message}</ErrorText>
      </Show>
      <Show when={!grabs.loading} fallback={<Muted>Loading…</Muted>}>
        <Show
          when={grabs() && grabs()!.length > 0}
          fallback={<Muted>No grabs yet for this mode.</Muted>}
        >
          <ul class="flex flex-col gap-2">
            <For each={grabs()}>{(g) => <GrabRow grab={g} />}</For>
          </ul>
        </Show>
      </Show>
    </div>
  );
};
