// MainstreamDiscover — the combined Movies+Series page and its cards: a search
// bar over four stacked, independently-paginated TMDB category rows plus a
// paginated "In your library" row of what's already tracked. Movies grab
// directly on click; Series first open a season/episode picker (the gating step,
// since no release exists to score until a specific episode/pack is chosen).
// Extracted from the original single-file Discover.tsx.

import {
  type Component,
  createEffect,
  createResource,
  createSignal,
  on,
  For,
  Show,
} from "solid-js";
import {
  type DiscoverItem,
  type DiscoverCategory,
  fetchDiscover,
  fetchTitlePoster,
  fetchTmdbSearch,
  tmdbPoster,
} from "../../api/discover";
import { type TrackedItem, fetchTrackedItems } from "../../api/tag";
import { Button, ErrorText, Muted, yearOf } from "../../components/ui";
import {
  type GrabTarget,
  ConfigureConnectionModal,
  GrabDialog,
  PaginatedStrip,
  TextPoster,
  notConfiguredService,
} from "./shared";

// ModedTitle is the mode a merged card belongs to — the per-item mode a
// combined (movies+series) row/grid MUST carry so each card grabs via its own
// path: a Series card first opens the season/episode picker, a Movies card
// grabs directly. Passing one fixed mode across a mixed row would silently
// route a series through the movie grab path, breaking auto-grab.
type ModedTitle = { mode: "movies" | "series"; item: DiscoverItem };

// MAINSTREAM_ROWS is the fixed set of TMDB category rows the Mainstream page
// stacks: both modes × both categories. Each row paginates independently.
const MAINSTREAM_ROWS: {
  title: string;
  mode: "movies" | "series";
  category: DiscoverCategory;
}[] = [
  { title: "Trending Movies", mode: "movies", category: "trending" },
  { title: "Trending Shows", mode: "series", category: "trending" },
  { title: "Popular Movies", mode: "movies", category: "popular" },
  { title: "Popular Shows", mode: "series", category: "popular" },
];

// SeasonEpisodePicker gates a Series grab: no release can be scored until a
// specific season (and optionally episode) is chosen. Submitting always marks
// the season as specified — that is what preserves Season-0/Specials (a bare
// season number can't distinguish "Season 0 picked" from "nothing picked").
const SeasonEpisodePicker: Component<{
  onSubmit: (season: number, episode: number) => void;
}> = (props) => {
  const [season, setSeason] = createSignal("");
  const [episode, setEpisode] = createSignal("");
  return (
    <form
      class="mt-1 flex items-center gap-1"
      onSubmit={(e) => {
        e.preventDefault();
        props.onSubmit(
          parseInt(season(), 10) || 0,
          parseInt(episode(), 10) || 0,
        );
      }}
    >
      <input
        class="w-12 rounded border border-border bg-bg px-1 py-0.5 text-xs text-fg outline-none focus:border-accent"
        placeholder="S"
        aria-label="Season"
        value={season()}
        onInput={(e) => setSeason(e.currentTarget.value)}
      />
      <input
        class="w-12 rounded border border-border bg-bg px-1 py-0.5 text-xs text-fg outline-none focus:border-accent"
        placeholder="E"
        aria-label="Episode"
        value={episode()}
        onInput={(e) => setEpisode(e.currentTarget.value)}
      />
      <button
        type="submit"
        class="rounded bg-accent px-2 py-0.5 text-xs font-medium text-accent-fg"
      >
        Go
      </button>
    </form>
  );
};

// GrabButton is the per-title grab affordance. Movies grab on click. Series
// first reveal the season/episode picker (the gating step) and only build a
// GrabTarget once the picker is submitted.
const GrabButton: Component<{
  mode: "movies" | "series";
  item: DiscoverItem;
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const [picking, setPicking] = createSignal(false);

  const grabMovie = () =>
    props.onGrab({
      mode: "movies",
      label: props.item.title,
      request: { title: props.item.title, tmdbId: props.item.id },
    });

  const grabSeries = (season: number, episode: number) => {
    setPicking(false);
    const suffix = `S${season}${episode ? "E" + episode : ""}`;
    props.onGrab({
      mode: "series",
      label: `${props.item.title} ${suffix}`,
      request: {
        title: props.item.title,
        tmdbId: props.item.id,
        seasonNumber: season,
        episodeNumber: episode,
        seasonSpecified: true,
      },
    });
  };

  return (
    <Show
      when={props.mode === "series"}
      fallback={
        <Button class="w-full !py-1 text-xs" onClick={grabMovie}>
          Grab
        </Button>
      }
    >
      <Show
        when={picking()}
        fallback={
          <Button class="w-full !py-1 text-xs" onClick={() => setPicking(true)}>
            Grab
          </Button>
        }
      >
        <SeasonEpisodePicker onSubmit={grabSeries} />
      </Show>
    </Show>
  );
};

// PosterCard is one Movies/Series title. Fixed width so a row scrolls
// horizontally. The title attribute carries the overview as a native tooltip —
// "show more detail" without any click handler that could mutate.
const PosterCard: Component<{
  mode: "movies" | "series";
  item: DiscoverItem;
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const src = () => tmdbPoster(props.item.posterPath);
  return (
    <div class="w-36 shrink-0" title={props.item.overview}>
      <div class="aspect-[2/3] overflow-hidden rounded-lg border border-border bg-surface">
        <Show when={src()} fallback={<TextPoster label={props.item.title} />}>
          <img
            src={src()}
            alt={props.item.title}
            loading="lazy"
            class="h-full w-full object-cover"
          />
        </Show>
      </div>
      <div class="mt-1.5 truncate text-sm text-fg" title={props.item.title}>
        {props.item.title}
      </div>
      <div class="flex items-center gap-2 text-xs text-muted">
        <span>{yearOf(props.item.releaseDate) ?? "—"}</span>
        <Show when={props.item.voteAverage > 0}>
          <span>★ {props.item.voteAverage.toFixed(1)}</span>
        </Show>
      </div>
      <div class="mt-1.5">
        <GrabButton mode={props.mode} item={props.item} onGrab={props.onGrab} />
      </div>
    </div>
  );
};

// PaginatedRow is the Mainstream TMDB category strip (fixed mode + category) — a
// thin wrapper over PaginatedStrip that loads one TMDB category page and renders
// each result as a PosterCard.
const PaginatedRow: Component<{
  title: string;
  mode: "movies" | "series";
  category: DiscoverCategory;
  reloadToken: () => number;
  onGrab: (t: GrabTarget) => void;
  onError: (err: unknown) => void;
}> = (props) => (
  <PaginatedStrip
    title={props.title}
    reloadToken={props.reloadToken}
    load={(page) => fetchDiscover(props.mode, props.category, page)}
    onError={props.onError}
  >
    {(item) => (
      <PosterCard mode={props.mode} item={item} onGrab={props.onGrab} />
    )}
  </PaginatedStrip>
);

// LibraryCard is one owned-library title on the existing-library row. Its mode
// is per-item (the row mixes movies+series), which drives both the lazy poster
// fetch and the auto-grab path. The library caches no poster art, so the
// poster is resolved on demand by tmdbId (fetchTitlePoster) — one bounded call
// per rendered card, then routed through the image proxy exactly like every
// other card. A synthetic DiscoverItem (id = tmdbId) feeds GrabButton so a
// library card grabs through the identical GrabDialog/autoGrab path a Discover
// card does — Series still gets its season/episode picker.
const LibraryCard: Component<{
  mode: "movies" | "series";
  item: TrackedItem;
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const tmdbId = () => props.item.tmdbId ?? 0;
  const [poster] = createResource(tmdbId, (id) =>
    id ? fetchTitlePoster(props.mode, id).catch(() => "") : Promise.resolve(""),
  );
  const src = () => tmdbPoster(poster() ?? "");
  const grabItem = (): DiscoverItem => ({
    id: tmdbId(),
    title: props.item.title,
    posterPath: poster() ?? "",
    overview: "",
    releaseDate: props.item.year ? String(props.item.year) : "",
    voteAverage: 0,
    mediaType: props.mode === "series" ? "tv" : "movie",
  });
  return (
    <div class="w-36 shrink-0" title={props.item.title}>
      <div class="aspect-[2/3] overflow-hidden rounded-lg border border-border bg-surface">
        <Show when={src()} fallback={<TextPoster label={props.item.title} />}>
          <img
            src={src()}
            alt={props.item.title}
            loading="lazy"
            class="h-full w-full object-cover"
          />
        </Show>
      </div>
      <div class="mt-1.5 truncate text-sm text-fg" title={props.item.title}>
        {props.item.title}
      </div>
      <div class="flex items-center gap-2 text-xs text-muted">
        <span>{props.item.year || "—"}</span>
      </div>
      <div class="mt-1.5">
        <GrabButton mode={props.mode} item={grabItem()} onGrab={props.onGrab} />
      </div>
    </div>
  );
};

// LIBRARY_PAGE_SIZE bounds how many library cards render (and therefore how many
// per-card poster fetches fire) at once, mirroring the category rows' "Show
// more" paging. Without this the whole tracked set mounts in one shot, firing a
// poster fetch per card — a real fan-out on a large library.
const LIBRARY_PAGE_SIZE = 20;

// LibraryRow surfaces what's already tracked, movies + series merged into one
// strip (each card tagged with its own mode). The full tracked set is fetched
// once (it's the operator's own bounded library, not TMDB's infinite catalog),
// but only one page's worth is rendered at a time behind a "Show more" — the
// same paging shape PaginatedRow uses — so DOM size and concurrent per-card
// poster fetches stay bounded. Reloads on reloadToken alongside the category
// rows; the visible count resets to one page on every reload.
const LibraryRow: Component<{
  reloadToken: () => number;
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const [entries] = createResource(props.reloadToken, async () => {
    const [movies, series] = await Promise.all([
      fetchTrackedItems("movies").catch(() => [] as TrackedItem[]),
      fetchTrackedItems("series").catch(() => [] as TrackedItem[]),
    ]);
    return [
      ...movies.map((item) => ({ mode: "movies" as const, item })),
      ...series.map((item) => ({ mode: "series" as const, item })),
    ];
  });

  const [visible, setVisible] = createSignal(LIBRARY_PAGE_SIZE);
  createEffect(on(props.reloadToken, () => setVisible(LIBRARY_PAGE_SIZE)));

  const shown = () => (entries() ?? []).slice(0, visible());
  const hasMore = () => (entries()?.length ?? 0) > visible();

  return (
    <Show when={(entries()?.length ?? 0) > 0}>
      <section class="mt-6">
        <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-muted">
          In your library
        </h2>
        <div class="flex items-stretch gap-3 overflow-x-auto pb-2">
          <For each={shown()}>
            {(e) => (
              <LibraryCard mode={e.mode} item={e.item} onGrab={props.onGrab} />
            )}
          </For>
          <Show when={hasMore()}>
            <div class="flex w-28 shrink-0 items-center justify-center">
              <Button
                class="!py-1 text-xs"
                onClick={() => setVisible((n) => n + LIBRARY_PAGE_SIZE)}
              >
                Show more
              </Button>
            </div>
          </Show>
        </div>
      </section>
    </Show>
  );
};

// MainstreamDiscover is the combined Movies+Series page: a search bar over four
// stacked TMDB category rows plus the existing-library row. Searching replaces
// the rows with one merged (movies+series) result grid; clearing restores the
// rows. It owns the single grab dialog for every card (rows, library, search)
// and the not-configured setup modal, raised once when any row's fetch reports
// TMDB missing.
export const MainstreamDiscover: Component = () => {
  const [grabTarget, setGrabTarget] = createSignal<GrabTarget | null>(null);
  const [setupError, setSetupError] = createSignal<unknown>(null);
  const [dismissedSetup, setDismissedSetup] = createSignal(false);
  const [reloadToken, setReloadToken] = createSignal(0);

  // Search: draft is the input value, submitted is the committed query. A
  // non-empty submitted query swaps the rows for the merged result grid.
  const [draft, setDraft] = createSignal("");
  const [submitted, setSubmitted] = createSignal("");
  const searching = () => submitted().trim().length > 0;

  const [results] = createResource(
    () => (searching() ? submitted().trim() : null),
    async (q): Promise<ModedTitle[]> => {
      // A search error is surfaced the same way a category row's is: hand it to
      // setSetupError so a "tmdb isn't configured yet" failure raises the same
      // setup modal (the render's notConfiguredService gate decides modal vs.
      // plain error), instead of being swallowed into an empty "No results
      // found". Reusing the row plumbing keeps one detection path, not two.
      try {
        const [movies, series] = await Promise.all([
          fetchTmdbSearch("movies", q),
          fetchTmdbSearch("series", q),
        ]);
        return [
          ...movies.map((item) => ({ mode: "movies" as const, item })),
          ...series.map((item) => ({ mode: "series" as const, item })),
        ];
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
          setSubmitted(draft());
        }}
      >
        <input
          class="w-full max-w-sm rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg outline-none focus:border-accent"
          placeholder="Search movies & shows…"
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
          <>
            <For each={MAINSTREAM_ROWS}>
              {(row) => (
                <PaginatedRow
                  title={row.title}
                  mode={row.mode}
                  category={row.category}
                  reloadToken={reloadToken}
                  onGrab={setGrabTarget}
                  onError={setSetupError}
                />
              )}
            </For>
            <LibraryRow reloadToken={reloadToken} onGrab={setGrabTarget} />
          </>
        }
      >
        <section class="mt-2">
          <h2 class="mb-2 text-sm font-semibold uppercase tracking-wide text-muted">
            Search results
          </h2>
          <Show when={!results.loading} fallback={<Muted>Searching…</Muted>}>
            <Show
              when={(results()?.length ?? 0) > 0}
              fallback={<Muted>No results found.</Muted>}
            >
              <div class="flex flex-wrap gap-3">
                <For each={results()}>
                  {(e) => (
                    <PosterCard
                      mode={e.mode}
                      item={e.item}
                      onGrab={setGrabTarget}
                    />
                  )}
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
