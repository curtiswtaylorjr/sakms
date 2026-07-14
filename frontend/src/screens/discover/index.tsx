// Discover — the Seerr-inspired browse landing, MUTATING (Stage 2). The
// Mainstream tab is a search bar over four stacked, independently-paginated TMDB
// category rows (Trending/Popular × Movies/Series) plus a paginated "In your
// library" row of what's already tracked; the Adult tab is a TPDB scene browse.
// Discovery is sourced purely from TMDB/TPDB (and the local library) — Prowlarr
// is never consulted here; it's only involved later, when a grab actually
// retrieves a title. Poster/scene art renders ONLY through the image proxy
// (src/api/discover.ts's proxyImage/tmdbPoster), never hot-linked from
// TMDB/TPDB (plan Decision #7).
//
// One-click auto-grab (plan Decision #5): a card's "Grab" triggers the backend
// auto-grab — search + bitrate-quality-floor scoring — which either grabs the
// top qualifier outright or returns a ranked manual pick list when nothing
// clears the floor (never a silent failure, never "grab the least-bad option").
// Per-mode nuance is respected exactly:
//   - Movies: one click grabs directly (the clean 1-poster=1-title case).
//   - Series: one click opens a season/episode picker FIRST — "one click per
//     season/episode selection", since no release exists to score until a
//     specific episode/pack is chosen. Season-0/Specials is preserved:
//     submitting the picker always sets seasonSpecified=true (a bare season
//     number can't tell "Season 0 picked" from "no season picked").
//   - Adult: one click grabs a scene, sourcing the bitrate scorer's runtime
//     from the scene's TPDB durationSeconds.
// No bulk actions anywhere (Guardrail #3): every affordance grabs exactly one
// title/episode/scene per click.
//
// This screen is split across discover/: the grab pipeline, setup-modal, and
// PaginatedStrip pagination engine shared by both tabs live in shared.tsx;
// MainstreamDiscover (rows/cards/library/search) in Mainstream.tsx; AdultDiscover
// (scene rows/cards/drill-down) in Adult.tsx; this file is the thin tab shell.

import {
  type Component,
  createSignal,
  Switch,
  Match,
} from "solid-js";
import { type TabDef, ScreenTabs } from "../../components/ui";
import { MainstreamDiscover } from "./Mainstream";
import { AdultDiscover } from "./Adult";

// MAINSTREAM_TABS replaces the old Movies/Series/Adult set: Mainstream (all
// TMDB titles, both modes combined on one page) and Adult (TPDB scene view).
const MAINSTREAM_TABS: TabDef[] = [
  { id: "mainstream", label: "Mainstream" },
  { id: "adult", label: "Adult" },
];

// Discover is the tab shell: Mainstream (combined Movies+Series) / Adult. Tabs
// register with the app shell (which draws the bar in its consistent location);
// rendered standalone (a unit test with no shell context) it falls back to
// drawing the bar inline, the same pattern ModeTabs uses — so tests can still
// click "Adult" without mounting the whole shell.
export const Discover: Component = () => {
  const [tab, setTab] = createSignal("mainstream");
  return (
    <div>
      <ScreenTabs
        tabs={MAINSTREAM_TABS}
        current={tab}
        onSelect={setTab}
        class="flex gap-1"
      />
      <div class="mt-4">
        <Switch>
          <Match when={tab() === "adult"}>
            <AdultDiscover />
          </Match>
          <Match when={tab() === "mainstream"}>
            <MainstreamDiscover />
          </Match>
        </Switch>
      </div>
    </div>
  );
};
