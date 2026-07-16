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
import { Button, type TabDef, ScreenTabs } from "../../components/ui";
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
//
// editMode drives the Optional RSS Discover rows + inline row editor feature:
// a single Edit toggle lives in the tab bar's trailing slot (ScreenTabs'
// `trailing` prop) here, one level above Mainstream/Adult, since it's the
// same toggle regardless of which sub-tab is active — each sub-screen reads
// it via a prop and renders RowEditor in place of its normal row list while
// on. Switching tabs resets it to false so a stale "Edit" state never
// carries from one screen to the other.
export const Discover: Component = () => {
  const [tab, setTab] = createSignal("mainstream");
  const [editMode, setEditMode] = createSignal(false);

  const selectTab = (id: string) => {
    setEditMode(false);
    setTab(id);
  };

  return (
    <div>
      <ScreenTabs
        tabs={MAINSTREAM_TABS}
        current={tab}
        onSelect={selectTab}
        trailing={
          <Button
            class="!px-3 !py-1.5 !text-sm"
            onClick={() => setEditMode((v) => !v)}
          >
            {editMode() ? "Done" : "Edit"}
          </Button>
        }
        class="flex items-center gap-1"
      />
      <div class="mt-4">
        <Switch>
          <Match when={tab() === "adult"}>
            <AdultDiscover editMode={editMode} />
          </Match>
          <Match when={tab() === "mainstream"}>
            <MainstreamDiscover editMode={editMode} />
          </Match>
        </Switch>
      </div>
    </div>
  );
};
