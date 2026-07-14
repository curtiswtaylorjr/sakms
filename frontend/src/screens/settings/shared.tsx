// Shared Settings primitives used by every section panel: the MODE_LABELS map,
// the Card fieldset frame, the SaveStatus inline line, and the per-panel
// useSaveStatus signal helper. Extracted verbatim from the original single-file
// Settings.tsx — pieces already shared within that file, relocated.

import { type Component, type JSX, createSignal, Show } from "solid-js";
import type { Mode } from "../../api/discover";

export const MODE_LABELS: Record<Mode, string> = {
  movies: "Movies",
  series: "Series",
  adult: "Adult",
};

// Card is the fieldset frame every settings panel shares.
export const Card: Component<{ title: string; children: JSX.Element }> = (
  props,
) => (
  <fieldset class="mb-4 rounded-xl border border-border bg-surface p-4">
    <legend class="px-2 text-sm font-semibold text-fg">{props.title}</legend>
    {props.children}
  </fieldset>
);

// SaveStatus renders the inline "saved" / error line every panel's Save button
// pairs with. text is empty until an action runs.
export const SaveStatus: Component<{ text: string; error: boolean }> = (
  props,
) => (
  <Show when={props.text}>
    <span class={`text-sm ${props.error ? "text-danger" : "text-muted"}`}>
      {props.text}
    </span>
  </Show>
);

// useSaveStatus is the tiny per-panel status signal helper.
export function useSaveStatus() {
  const [status, setStatus] = createSignal<{ text: string; error: boolean }>({
    text: "",
    error: false,
  });
  return {
    status,
    saved: () => setStatus({ text: "saved", error: false }),
    failed: (e: unknown) =>
      setStatus({ text: (e as Error).message, error: true }),
    set: (text: string) => setStatus({ text, error: false }),
  };
}
