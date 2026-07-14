// Shared Discover machinery used by BOTH the Mainstream and Adult sub-screens:
// the grab pipeline (GrabTarget → GrabDialog → auto-grab / manual FallbackPickList),
// the not-configured setup modal (ConfigureConnectionModal + its detection helper),
// the TextPoster art fallback, and the generic PaginatedStrip pagination engine.
// These were extracted verbatim from the original single-file Discover.tsx — they
// are pieces already shared within that file, relocated, not newly abstracted.

import {
  type Component,
  type JSX,
  createEffect,
  createResource,
  createSignal,
  on,
  For,
  Show,
  Switch,
  Match,
} from "solid-js";
import { type Mode } from "../../api/discover";
import {
  type AutoGrabCandidate,
  type AutoGrabRequest,
  type AutoGrabResponse,
  autoGrab,
  libraryRootFolder,
  manualGrab,
} from "../../api/grab";
import { Button, ErrorText, Muted } from "../../components/ui";
import { buildConnectionUpsertBody, upsertConnection } from "../../api/settings";

// GrabTarget is one pending auto-grab: which mode, a human label for the
// dialog title, and the exact request body the backend needs. For Series the
// season/episode picker has already resolved before a target exists.
export type GrabTarget = { mode: Mode; label: string; request: AutoGrabRequest };

// STATUS_COPY turns an autograb.Grade Status into a short human reason for a
// fallback pick-list row — so the operator sees WHY each release wasn't
// auto-picked, not a bare rejected flag.
const STATUS_COPY: Record<string, string> = {
  qualified: "meets the bar",
  "below-floor": "below the quality floor",
  mislabeled: "looks mislabeled",
  "low-seeders": "too few seeders",
  "unknown-bitrate": "runtime unknown — bitrate not scored",
  "unknown-resolution": "resolution not recognized",
};

// Modal is a lightweight centered overlay for the grab dialog. Clicking the
// backdrop or Close dismisses it; clicks inside don't bubble out.
const Modal: Component<{
  title: string;
  onClose: () => void;
  children: JSX.Element;
}> = (props) => (
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
    onClick={props.onClose}
  >
    <div
      class="max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-xl border border-border bg-surface p-5 shadow-lg"
      onClick={(e) => e.stopPropagation()}
    >
      <div class="mb-3 flex items-center justify-between gap-3">
        <h3 class="truncate text-base font-semibold text-fg">{props.title}</h3>
        <Button onClick={props.onClose}>Close</Button>
      </div>
      {props.children}
    </div>
  </div>
);

// NOT_CONFIGURED_SERVICES maps the two external services Discover itself
// depends on (backend errors are the fixed strings "tmdb isn't configured
// yet — add it in Settings first" / "tpdb isn't configured yet — add it in
// Settings first", see internal/api/discover.go and adultdiscover.go) to
// their fixed base URL (both are external APIs with one canonical endpoint,
// not self-hosted — the operator only ever needs to supply a key, unlike
// Prowlarr/qBittorrent/etc.) and the external page to obtain a key. TMDB's
// is well-known and stable; TPDB's was confirmed directly by Wade
// (2026-07-13) rather than guessed, since it isn't discoverable from a
// plain page fetch (the site is JS-rendered).
const NOT_CONFIGURED_SERVICES: Record<
  "tmdb" | "tpdb",
  { label: string; url: string; keyPageUrl: string; keyPageLabel: string }
> = {
  tmdb: {
    label: "TMDB",
    url: "https://api.themoviedb.org/3",
    keyPageUrl: "https://www.themoviedb.org/settings/api",
    keyPageLabel: "themoviedb.org/settings/api",
  },
  tpdb: {
    label: "TPDB",
    url: "https://api.theporndb.net",
    keyPageUrl: "https://theporndb.net/user/api-tokens",
    keyPageLabel: "theporndb.net/user/api-tokens",
  },
};

// notConfiguredService detects which (if either) of Discover's two external
// dependencies a resource error is reporting missing, by matching the
// backend's fixed error string — returns undefined for any other error (a
// genuine network failure, a 500, etc.), which callers fall back to
// ErrorText for instead of assuming it's a "go configure this" case.
export function notConfiguredService(
  err: unknown,
): "tmdb" | "tpdb" | undefined {
  const msg = (err as Error)?.message ?? "";
  if (!/isn't configured yet/i.test(msg)) return undefined;
  if (/\btmdb\b/i.test(msg)) return "tmdb";
  if (/\btpdb\b/i.test(msg)) return "tpdb";
  return undefined;
}

// ConfigureConnectionModal — shown instead of a bare error message when
// Discover detects TMDB/TPDB isn't configured. Saves directly into the same
// connection store Settings' own form writes to (upsertConnection/
// buildConnectionUpsertBody, reused verbatim, not duplicated) so there's
// exactly one place that actually persists a connection — this is just a
// second, more contextual entry point into it. First-time save, so
// hasExistingKey is always false and keyTouched is always true here (see
// buildConnectionUpsertBody's own doc comment on why that combination is
// safe: a first save always sends the key, even if it were left blank).
export const ConfigureConnectionModal: Component<{
  service: "tmdb" | "tpdb";
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const info = NOT_CONFIGURED_SERVICES[props.service];
  const [key, setKey] = createSignal("");
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal("");

  const save = async () => {
    setError("");
    if (!key().trim()) {
      setError("Enter an API key first.");
      return;
    }
    setSaving(true);
    try {
      await upsertConnection(
        props.service,
        buildConnectionUpsertBody({
          url: info.url,
          needsUsername: false,
          keyTouched: true,
          keyValue: key(),
          hasExistingKey: false,
        }),
      );
      props.onSaved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title={`Set up ${info.label}`} onClose={props.onClose}>
      <p class="mb-3 text-sm text-muted">
        {info.label} isn't configured yet — Discover needs it to browse{" "}
        {props.service === "tpdb" ? "Adult scenes" : "titles"}. Paste an API
        key below to enable it now, or add it later in Settings.
      </p>
      <a
        href={info.keyPageUrl}
        target="_blank"
        rel="noreferrer"
        class="mb-3 block text-sm text-accent underline"
      >
        Get an API key at {info.keyPageLabel}
      </a>
      <input
        type="password"
        class="w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent"
        placeholder="API key"
        value={key()}
        onInput={(e) => setKey(e.currentTarget.value)}
      />
      <Show when={error()}>
        <ErrorText>{error()}</ErrorText>
      </Show>
      <div class="mt-3 flex justify-end gap-2">
        <Button onClick={props.onClose}>Cancel</Button>
        <Button variant="primary" onClick={save} disabled={saving()}>
          {saving() ? "Saving…" : "Save"}
        </Button>
      </div>
    </Modal>
  );
};

// FallbackPickList renders the ranked manual pick list the backend returns when
// nothing auto-qualified. Each row labels why it wasn't auto-picked and offers
// a single "Grab this" — one release per click, never a batch.
const FallbackPickList: Component<{
  response: AutoGrabResponse;
  onPick: (c: AutoGrabCandidate) => void;
  grabbing: string;
  error: string;
}> = (props) => (
  <div>
    <Muted class="mb-2">{props.response.message}</Muted>
    <Show when={props.error}>
      <ErrorText>{props.error}</ErrorText>
    </Show>
    <Show
      when={(props.response.candidates ?? []).length > 0}
      fallback={<Muted>No releases found for this title.</Muted>}
    >
      <ul class="flex flex-col gap-2">
        <For each={props.response.candidates}>
          {(c) => (
            <li class="flex items-center gap-3 rounded-md border border-border bg-surface-2 p-2">
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm text-fg" title={c.title}>
                  {c.title}
                </div>
                <div class="truncate text-xs text-muted">
                  {[c.indexer, c.protocol, STATUS_COPY[c.status] ?? c.status]
                    .filter(Boolean)
                    .join(" · ")}
                </div>
              </div>
              <Button
                onClick={() => props.onPick(c)}
                disabled={!!props.grabbing}
              >
                {props.grabbing === c.downloadUrl ? "Grabbing…" : "Grab this"}
              </Button>
            </li>
          )}
        </For>
      </ul>
    </Show>
  </div>
);

// GrabDialog fires the auto-grab for a target on mount, then shows the outcome:
// a success line when the backend grabbed the top qualifier, or the manual pick
// list when it fell back. The manual pick reuses the existing /search/grab
// endpoint (auto-grab resolves the root folder server-side; the fallback path
// must fetch it explicitly).
export const GrabDialog: Component<{ target: GrabTarget; onClose: () => void }> = (
  props,
) => {
  const [result] = createResource(
    () => props.target,
    (t) => autoGrab(t.mode, t.request),
  );
  const [grabbing, setGrabbing] = createSignal("");
  const [manualError, setManualError] = createSignal("");
  const [manualGrabbed, setManualGrabbed] = createSignal<string | null>(null);

  const pickManual = async (c: AutoGrabCandidate) => {
    setManualError("");
    setGrabbing(c.downloadUrl);
    try {
      const root = await libraryRootFolder(props.target.mode);
      if (!root) {
        throw new Error(
          "no root folder configured for this mode — set one in Settings first",
        );
      }
      await manualGrab(props.target.mode, {
        title: props.target.request.title,
        tmdbId: props.target.request.tmdbId,
        seasonNumber: props.target.request.seasonNumber,
        episodeNumber: props.target.request.episodeNumber,
        seasonSpecified: props.target.request.seasonSpecified,
        indexer: c.indexer,
        protocol: c.protocol,
        downloadUrl: c.downloadUrl,
        rootFolderPath: root,
      });
      setManualGrabbed(c.title);
    } catch (e) {
      setManualError((e as Error).message);
    } finally {
      setGrabbing("");
    }
  };

  return (
    <Modal title={`Grab — ${props.target.label}`} onClose={props.onClose}>
      <Show
        when={!result.loading}
        fallback={<Muted>Searching and scoring releases…</Muted>}
      >
        <Show when={result.error}>
          <ErrorText>{(result.error as Error)?.message}</ErrorText>
        </Show>
        <Show when={result()}>
          {(r) => (
            <Switch>
              <Match when={r().grabbed}>
                <div class="text-sm text-ok">{r().message}</div>
                <Muted class="mt-1">
                  Tracked in the Grabs view — check import there once it finishes
                  downloading.
                </Muted>
              </Match>
              <Match when={r().fallback}>
                <Show
                  when={manualGrabbed()}
                  fallback={
                    <FallbackPickList
                      response={r()}
                      onPick={pickManual}
                      grabbing={grabbing()}
                      error={manualError()}
                    />
                  }
                >
                  <div class="text-sm text-ok">
                    Grabbed “{manualGrabbed()}”. Tracked in the Grabs view.
                  </div>
                </Show>
              </Match>
            </Switch>
          )}
        </Show>
      </Show>
    </Modal>
  );
};

// TextPoster is the fallback tile when no art exists (TMDB/TPDB returned a
// blank poster/image) — a titled placeholder that keeps the card's footprint
// identical to an image card so rows don't reflow.
export const TextPoster: Component<{ label: string }> = (props) => (
  <div class="flex h-full w-full items-center justify-center bg-surface-2 p-2 text-center text-xs text-muted">
    {props.label}
  </div>
);

// PaginatedStrip is the generic "Show more" strip every Discover row is built
// from: a title, a horizontal (or, via containerClass, wrapping) list of cards,
// and a "Show more" that APPENDS the next page rather than replacing the strip —
// the accumulator (items) only ever grows. It reloads from page 1 whenever
// reloadToken changes (the setup-modal "I just configured it, refetch" signal).
// Fetch errors are reported up via onError so the parent can raise the
// not-configured setup modal once for the whole page, not per strip. The item
// type T and both the page loader (load) and the per-item renderer (children)
// are supplied by the caller, so one pagination engine backs the Mainstream
// TMDB rows, the Adult scene rows, the Studios/Performers browse rows, and the
// drill-down scene grid alike (plan: reuse the pattern, don't reimplement it).
export function PaginatedStrip<T>(props: {
  title: string;
  reloadToken: () => number;
  load: (page: number) => Promise<T[]>;
  onError: (err: unknown) => void;
  containerClass?: string;
  children: (item: T) => JSX.Element;
  // singlePage suppresses "Show more" even when more data may exist — for
  // rows whose ordering is only meaningful within one fetched page (e.g.
  // Adult's "Highest Rated," a same-page rating re-sort with no true
  // server-side popularity sort behind it: paginating would append an
  // independently-resorted page 2 after page 1, producing a visibly
  // non-monotonic rating order under a "Highest Rated" label).
  singlePage?: boolean;
}): JSX.Element {
  const [items, setItems] = createSignal<T[]>([]);
  const [page, setPage] = createSignal(0);
  const [loading, setLoading] = createSignal(false);
  const [exhausted, setExhausted] = createSignal(false);

  const load = async (reset: boolean) => {
    const next = reset ? 1 : page() + 1;
    setLoading(true);
    try {
      const batch = await props.load(next);
      setItems((prev) => (reset ? batch : [...prev, ...batch]));
      setPage(next);
      if (batch.length === 0) setExhausted(true);
    } catch (e) {
      props.onError(e);
    } finally {
      setLoading(false);
    }
  };

  // Initial load AND reload-on-token in one effect (on() runs immediately by
  // default, so no separate onMount is needed).
  createEffect(
    on(props.reloadToken, () => {
      setItems([]);
      setPage(0);
      setExhausted(false);
      void load(true);
    }),
  );

  return (
    <section class="mt-6">
      <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-muted">
        {props.title}
      </h2>
      <Show
        when={items().length > 0}
        fallback={
          <Muted>{loading() ? "Loading…" : "Nothing here yet."}</Muted>
        }
      >
        <div class={props.containerClass ?? "flex items-stretch gap-3 overflow-x-auto pb-2"}>
          <For each={items()}>{(item) => props.children(item)}</For>
          <Show when={!exhausted() && !props.singlePage}>
            <div class="flex w-28 shrink-0 items-center justify-center">
              <Button
                class="!py-1 text-xs"
                onClick={() => void load(false)}
                disabled={loading()}
              >
                {loading() ? "Loading…" : "Show more"}
              </Button>
            </div>
          </Show>
        </div>
      </Show>
    </section>
  );
}
