// AdultDiscover — the scene-shaped browse and its cards: a search bar over two
// ordered TPDB scene rows (Recently Released, Highest Rated), a Studios row, and
// a Performers row. Searching swaps the rows for a plain result grid; clicking a
// Studio/Performer card drills down into a paginated grid of just that entity's
// scenes. Extracted from the original single-file Discover.tsx.

import {
  type Component,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import {
  type AdultCategory,
  type AdultDiscoverItem,
  type PerformerSummary,
  type StudioSummary,
  fetchAdultDiscover,
  fetchAdultDiscoverCategory,
  fetchAdultPerformerScenes,
  fetchAdultPerformers,
  fetchAdultStudioScenes,
  fetchAdultStudios,
  proxyImage,
} from "../../api/discover";
import { Button, ErrorText, Muted, yearOf } from "../../components/ui";
import {
  type GrabTarget,
  ConfigureConnectionModal,
  GrabDialog,
  PaginatedStrip,
  TextPoster,
  notConfiguredService,
} from "./shared";

// AdultCard is one TPDB scene. TPDB frequently returns no art, so the image is
// Show-guarded with a text fallback (the old frontend rendered Adult text-only;
// this adds art where TPDB provides it, via the proxy).
const AdultCard: Component<{
  item: AdultDiscoverItem;
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const src = () => proxyImage(props.item.image);
  const subtitle = () =>
    [props.item.studio, yearOf(props.item.date)].filter(Boolean).join(" · ");
  const grab = () =>
    props.onGrab({
      mode: "adult",
      label: props.item.title,
      request: {
        title: props.item.title,
        studio: props.item.studio,
        durationSeconds: props.item.durationSeconds,
      },
    });
  return (
    <div class="w-40 shrink-0" title={props.item.title}>
      <div class="aspect-video overflow-hidden rounded-lg border border-border bg-surface">
        <Show when={src()} fallback={<TextPoster label={props.item.title} />}>
          <img
            src={src()}
            alt={props.item.title}
            loading="lazy"
            class="h-full w-full object-cover"
          />
        </Show>
      </div>
      <div class="mt-1.5 truncate text-sm text-fg">{props.item.title}</div>
      <div class="truncate text-xs text-muted">{subtitle() || "—"}</div>
      <div class="mt-1.5">
        <Button class="w-full !py-1 text-xs" onClick={grab}>
          Grab
        </Button>
      </div>
    </div>
  );
};

// EntityCard is one Studio or Performer on the Adult browse rows — image-or-text
// tile + name, no grab (these are pure browse/navigation, not gradable items).
// TPDB frequently returns no art, so the image is Show-guarded with a text
// fallback and any non-empty URL is routed through the proxy (never hot-linked).
// The whole card is a button: clicking it drills down into that entity's scenes.
const EntityCard: Component<{
  name: string;
  image: string;
  onSelect: () => void;
}> = (props) => {
  const src = () => proxyImage(props.image);
  return (
    <button
      type="button"
      class="w-40 shrink-0 text-left"
      title={props.name}
      onClick={props.onSelect}
    >
      <div class="aspect-video overflow-hidden rounded-lg border border-border bg-surface">
        <Show when={src()} fallback={<TextPoster label={props.name} />}>
          <img
            src={src()}
            alt={props.name}
            loading="lazy"
            class="h-full w-full object-cover"
          />
        </Show>
      </div>
      <div class="mt-1.5 truncate text-sm text-fg">{props.name}</div>
    </button>
  );
};

// ADULT_SCENE_ROWS is the fixed pair of ordered TPDB scene feeds the Adult
// browse stacks: Recently Released (TPDB's real recency sort, pages normally)
// and Highest Rated (a page-local rating re-sort, honestly NOT a global
// popularity ranking — see internal/api/adultdiscover.go). Highest Rated is
// singlePage: "Show more" would append an independently-resorted page 2 after
// page 1, producing a visibly non-monotonic rating order under that label.
const ADULT_SCENE_ROWS: { title: string; category: AdultCategory; singlePage?: boolean }[] = [
  { title: "Recently Released", category: "recent" },
  { title: "Highest Rated", category: "top-rated", singlePage: true },
];

// AdultDrill is the active drill-down target: which entity kind, its opaque TPDB
// id (passed verbatim to the drill-down endpoint), and its name for the header.
type AdultDrill = { kind: "studio" | "performer"; id: string; name: string };

// AdultDiscover is the scene-shaped browse, row-based like Mainstream: a search
// bar over two ordered scene rows (Recently Released, Highest Rated), a Studios
// row, and a Performers row. Searching swaps the rows for a plain result grid;
// clicking a Studio/Performer card drills down into a paginated grid of just
// that entity's scenes (with a "Back to browse" control). Owns the single grab
// dialog for every scene card (rows, search, drill-down) and the not-configured
// setup modal, raised once when any strip's fetch reports TPDB missing.
export const AdultDiscover: Component = () => {
  const [grabTarget, setGrabTarget] = createSignal<GrabTarget | null>(null);
  const [setupError, setSetupError] = createSignal<unknown>(null);
  const [dismissedSetup, setDismissedSetup] = createSignal(false);
  const [reloadToken, setReloadToken] = createSignal(0);

  const [draft, setDraft] = createSignal("");
  const [submitted, setSubmitted] = createSignal("");
  const searching = () => submitted().trim().length > 0;

  // drill is the active Studio/Performer drill-down (null = the browse rows).
  const [drill, setDrill] = createSignal<AdultDrill | null>(null);

  const [results] = createResource(
    () => (searching() ? submitted().trim() : null),
    async (q): Promise<AdultDiscoverItem[]> => {
      // A search error is surfaced the same way a row's is: handed to
      // setSetupError so a "tpdb isn't configured yet" failure raises the same
      // setup modal (the render's notConfiguredService gate decides modal vs.
      // plain error), instead of being swallowed into an empty "No scenes
      // found". One detection path for every Adult fetch, not two.
      try {
        return await fetchAdultDiscover(q);
      } catch (e) {
        setSetupError(e);
        return [];
      }
    },
  );

  const clearSearch = () => {
    setDraft("");
    setSubmitted("");
  };

  const configureFor = () => notConfiguredService(setupError());

  return (
    <div>
      <form
        class="mb-4 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          // A new search takes precedence over any drill-down (Clear returns to
          // the rows, not back into a stale drill).
          setDrill(null);
          setSubmitted(draft());
        }}
      >
        <input
          class="w-full max-w-sm rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent"
          placeholder="Search scenes by title…"
          value={draft()}
          onInput={(e) => setDraft(e.currentTarget.value)}
        />
        <Show when={searching()}>
          <Button onClick={clearSearch}>Clear</Button>
        </Show>
      </form>

      <Show when={setupError()}>
        <Show
          when={!dismissedSetup() && configureFor()}
          fallback={<ErrorText>{(setupError() as Error)?.message}</ErrorText>}
        >
          {(service) => (
            <ConfigureConnectionModal
              service={service()}
              onClose={() => setDismissedSetup(true)}
              onSaved={() => {
                setDismissedSetup(true);
                setSetupError(null);
                setReloadToken((n) => n + 1);
              }}
            />
          )}
        </Show>
      </Show>

      <Show
        when={searching()}
        fallback={
          <Show
            when={drill()}
            fallback={
              <>
                <For each={ADULT_SCENE_ROWS}>
                  {(row) => (
                    <PaginatedStrip
                      title={row.title}
                      reloadToken={reloadToken}
                      load={(page) =>
                        fetchAdultDiscoverCategory(row.category, page)
                      }
                      onError={setSetupError}
                      singlePage={row.singlePage}
                    >
                      {(item) => (
                        <AdultCard item={item} onGrab={setGrabTarget} />
                      )}
                    </PaginatedStrip>
                  )}
                </For>
                <PaginatedStrip<StudioSummary>
                  title="Studios"
                  reloadToken={reloadToken}
                  load={(page) => fetchAdultStudios(page)}
                  onError={setSetupError}
                >
                  {(s) => (
                    <EntityCard
                      name={s.name}
                      image={s.image}
                      onSelect={() =>
                        setDrill({ kind: "studio", id: s.id, name: s.name })
                      }
                    />
                  )}
                </PaginatedStrip>
                <PaginatedStrip<PerformerSummary>
                  title="Performers"
                  reloadToken={reloadToken}
                  load={(page) => fetchAdultPerformers(page)}
                  onError={setSetupError}
                >
                  {(p) => (
                    <EntityCard
                      name={p.name}
                      image={p.image}
                      onSelect={() =>
                        setDrill({ kind: "performer", id: p.id, name: p.name })
                      }
                    />
                  )}
                </PaginatedStrip>
              </>
            }
          >
            {(d) => (
              <div>
                <div class="mb-2 flex items-center gap-3">
                  <Button class="!py-1 text-xs" onClick={() => setDrill(null)}>
                    Back to browse
                  </Button>
                </div>
                <PaginatedStrip
                  title={d().name}
                  reloadToken={reloadToken}
                  load={(page) =>
                    d().kind === "studio"
                      ? fetchAdultStudioScenes(d().id, page)
                      : fetchAdultPerformerScenes(d().id, page)
                  }
                  onError={setSetupError}
                  containerClass="flex flex-wrap gap-3"
                >
                  {(item) => <AdultCard item={item} onGrab={setGrabTarget} />}
                </PaginatedStrip>
              </div>
            )}
          </Show>
        }
      >
        <section class="mt-2">
          <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-muted">
            Search results
          </h2>
          <Show when={!results.loading} fallback={<Muted>Searching…</Muted>}>
            <Show
              when={(results()?.length ?? 0) > 0}
              fallback={<Muted>No scenes found.</Muted>}
            >
              <div class="flex flex-wrap gap-3">
                <For each={results()}>
                  {(item) => <AdultCard item={item} onGrab={setGrabTarget} />}
                </For>
              </div>
            </Show>
          </Show>
        </section>
      </Show>

      <Show when={grabTarget()}>
        {(t) => <GrabDialog target={t()} onClose={() => setGrabTarget(null)} />}
      </Show>
    </div>
  );
};
