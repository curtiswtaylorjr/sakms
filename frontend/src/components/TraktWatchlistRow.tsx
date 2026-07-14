// TraktWatchlistRow — Discover's "Trakt Watchlist" row (task #8): titles the
// operator marked "want to watch" on Trakt but doesn't own yet. Self-guards on
// Trakt's connection status so it renders nothing until an account is linked
// (Settings' Trakt section, Settings.tsx's TraktConnectionSection, is where
// that happens) — no other Discover code needs to know whether Trakt is
// configured.
//
// Not wired into Discover.tsx yet (same "component built standalone, wired by
// a later task" pattern worker-3's Carousel.tsx used) — mount
// `<TraktWatchlistRow onGrab={setGrabTarget} />` inside MainstreamDiscover,
// sharing its existing grab dialog. GrabButton/GrabTarget are imported from
// Discover.tsx (exported there for exactly this reuse) rather than
// reimplemented, so a watchlist card grabs through the identical
// auto-grab/season-episode-picker path every other Discover card does.
//
// PLACEHOLDER: fetchTraktStatus/fetchTraktWatchlist (src/api/trakt.ts) call a
// proposed, not-yet-confirmed backend contract — task #5 (worker-1) owns the
// real routes/DTOs. See trakt.ts's file doc comment.

import { type Component, createResource, Show } from "solid-js";
import { Carousel } from "./Carousel";
import { ErrorText } from "./ui";
import { fetchTraktStatus, fetchTraktWatchlist, type TraktWatchlistItem } from "../api/trakt";
import { fetchTitlePoster, tmdbPoster, type DiscoverItem } from "../api/discover";
import { GrabButton, type GrabTarget } from "../screens/Discover";

// TextPoster mirrors Discover.tsx's own fallback tile (not exported from
// there — small enough to duplicate rather than grow that file's export
// surface further for a one-line component).
const TextPoster: Component<{ label: string }> = (props) => (
  <div class="flex h-full w-full items-center justify-center bg-surface-2 p-2 text-center text-xs text-muted">
    {props.label}
  </div>
);

// WatchlistCard maps one Trakt watchlist entry to sakms's card shape. Trakt
// gives only (type, title, year, tmdbId) — no poster — so art is resolved the
// same on-demand way LibraryCard resolves it for the existing-library row:
// one bounded fetchTitlePoster call per rendered card, by tmdbId.
const WatchlistCard: Component<{
  item: TraktWatchlistItem;
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const mode = (): "movies" | "series" =>
    props.item.type === "show" ? "series" : "movies";
  const [poster] = createResource(
    () => props.item.tmdbId,
    (id) => (id ? fetchTitlePoster(mode(), id).catch(() => "") : Promise.resolve("")),
  );
  const src = () => tmdbPoster(poster() ?? "");
  const grabItem = (): DiscoverItem => ({
    id: props.item.tmdbId,
    title: props.item.title,
    posterPath: poster() ?? "",
    overview: "",
    releaseDate: props.item.year ? String(props.item.year) : "",
    voteAverage: 0,
    mediaType: mode() === "series" ? "tv" : "movie",
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
      <div class="text-xs text-muted">{props.item.year || "—"}</div>
      <div class="mt-1.5">
        <GrabButton mode={mode()} item={grabItem()} onGrab={props.onGrab} />
      </div>
    </div>
  );
};

export const TraktWatchlistRow: Component<{
  onGrab: (t: GrabTarget) => void;
}> = (props) => {
  const [status] = createResource(fetchTraktStatus);
  const [items] = createResource(
    () => (status()?.linked ? true : undefined),
    () => fetchTraktWatchlist(),
  );

  return (
    <Show when={status()?.linked}>
      <Show
        when={!items.error}
        fallback={<ErrorText>{(items.error as Error)?.message}</ErrorText>}
      >
        <Carousel
          title="Trakt Watchlist"
          items={items() ?? []}
          loading={items.loading}
          emptyText="Your Trakt watchlist is empty."
          renderItem={(item) => <WatchlistCard item={item} onGrab={props.onGrab} />}
        />
      </Show>
    </Show>
  );
};
