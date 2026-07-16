// RowEditor — the Discover Edit-mode surface: given the FULL merged, ordered
// row list for one screen (built-in rows + every dynamic row type — sliders,
// adult newest rows, rss feeds — already resolved to one flat descriptor
// list by the caller), renders it with up/down reorder buttons — matching
// SliderAdmin.tsx/AdultRowAdmin.tsx's existing convention: button-based, not
// drag-and-drop, no new frontend dependency. Built-in rows can be reordered
// but not hidden or deleted (that's not what was asked for); dynamic rows
// additionally get an enable-toggle and Delete. Purely presentational —
// callers own the actual row-order state and persistence (saveRowOrder),
// matching SliderAdmin's per-click-persists-immediately convention but with
// the persistence call living in the parent screen, since the parent is
// what owns the merged order across ALL row types.

import { type Component, For, Show } from "solid-js";
import { Button, Card, Muted } from "../../components/ui";

export type RowDescriptor = {
  key: string;
  label: string;
  // removable rows (sliders, adult newest rows, rss feeds) get an
  // enable-toggle and Delete; built-in rows only get reordered.
  removable: boolean;
  enabled?: boolean;
};

export const RowEditor: Component<{
  rows: RowDescriptor[];
  onMove: (key: string, direction: -1 | 1) => void;
  onToggleEnabled: (row: RowDescriptor) => void;
  onDelete: (row: RowDescriptor) => void;
}> = (props) => (
  <Card title="Reorder rows">
    <Muted class="mb-3">
      Use the up/down buttons to reorder — built-in rows can be moved but not
      hidden or removed. Dynamic rows (custom sliders, RSS feeds, and newest
      rows) can also be disabled or deleted here.
    </Muted>
    <Show when={props.rows.length > 0} fallback={<Muted>No rows yet.</Muted>}>
      <ul>
        <For each={props.rows}>
          {(row, i) => (
            <li class="flex items-center gap-3 border-b border-border/60 py-2">
              <div class="flex flex-col gap-0.5">
                <button
                  type="button"
                  aria-label={`Move ${row.label} up`}
                  class="rounded border border-border px-1 text-xs text-fg disabled:opacity-30"
                  disabled={i() === 0}
                  onClick={() => props.onMove(row.key, -1)}
                >
                  ▲
                </button>
                <button
                  type="button"
                  aria-label={`Move ${row.label} down`}
                  class="rounded border border-border px-1 text-xs text-fg disabled:opacity-30"
                  disabled={i() === props.rows.length - 1}
                  onClick={() => props.onMove(row.key, 1)}
                >
                  ▼
                </button>
              </div>
              <div class="min-w-0 flex-1 truncate text-sm text-fg">
                {row.label}
              </div>
              <Show when={row.removable}>
                <label class="flex items-center gap-1 text-xs text-muted">
                  <input
                    type="checkbox"
                    aria-label={`${row.label} enabled`}
                    checked={row.enabled ?? true}
                    onChange={() => props.onToggleEnabled(row)}
                  />
                  enabled
                </label>
                <Button
                  class="!px-2 !py-1 !text-xs"
                  onClick={() => props.onDelete(row)}
                >
                  Delete
                </Button>
              </Show>
            </li>
          )}
        </For>
      </ul>
    </Show>
  </Card>
);
