// DetailPopup — Discover's primary grab path (Decision, plan
// "pure-dancing-diffie.md"): clicking a card body (not its existing
// quick-grab button, which stays as a secondary one-click shortcut) opens
// this popup, which runs ONE upfront GET /api/modes/{mode}/discover/
// availability call (fetchAvailabilityPreview, src/api/discover.ts) and
// renders three independent selectors — resolution (480/720/1080/2160),
// quality tier (Low/Medium/High/Lossless), protocol (Usenet/Torrent) — whose
// disabled states all derive from that single already-fetched grid. Changing
// any one selector never refetches; it only re-derives the other two
// selectors' disabled states against the current combination (see
// resolutionEnabled/tierEnabled/protocolEnabled below).
//
// This endpoint DOES call Prowlarr, but only once, only on this explicit
// click — the same trigger shape as the pre-existing manual Search screen,
// not a reintroduction of the removed automatic per-card availability probe
// (see CLAUDE.md's "Discover never queries Prowlarr" note and its
// 2026-07-14 clarification).
//
// Series needs season/episode BEFORE the availability fetch can run (the
// backend endpoint requires them) — reused verbatim from Mainstream.tsx's
// existing SeasonEpisodePicker as a gating first step shown INSIDE this
// popup, rather than a second hand-rolled season/episode input or resolving
// it in the caller before opening the popup.

import {
  type Component,
  createEffect,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import type { AvailabilityCandidate, AvailabilityPreview } from "@dto";
import {
  type AdultDiscoverItem,
  type DiscoverItem,
  type Mode,
  fetchAvailabilityPreview,
  proxyImage,
  tmdbPoster,
} from "../../api/discover";
import { libraryRootFolder, manualGrab } from "../../api/grab";
import { fetchQualityPrefs } from "../../api/settings";
import { Button, ErrorText, Muted, labelClass, yearOf } from "../../components/ui";
import { Modal, TextPoster } from "./shared";
import { SeasonEpisodePicker } from "./Mainstream";

// DetailTarget is the card DetailPopup was opened for — a discriminated union
// so Adult's scene-shaped item (no overview/voteAverage/tmdbId) and
// Movies/Series' title-shaped DiscoverItem never get cross-accessed by
// mistake.
export type DetailTarget =
  | { mode: "movies" | "series"; item: DiscoverItem }
  | { mode: "adult"; item: AdultDiscoverItem };

type TierKey = "low" | "medium" | "high" | "lossless";
type ProtocolKey = "usenet" | "torrent";

// RESOLUTIONS_DESC is the defaults scan order (highest resolution first) —
// see computeDefaults. RESOLUTION_DISPLAY is the UI's left-to-right order,
// matching the plan's own "480p/720p/1080p/2160p" phrasing.
const RESOLUTIONS_DESC = [2160, 1080, 720, 480] as const;
const RESOLUTION_DISPLAY = [480, 720, 1080, 2160] as const;
const RESOLUTION_LABELS: Record<number, string> = {
  2160: "2160p",
  1080: "1080p",
  720: "720p",
  480: "480p",
};

const TIERS: TierKey[] = ["low", "medium", "high", "lossless"];
const TIER_LABELS: Record<TierKey, string> = {
  low: "Low",
  medium: "Medium",
  high: "High",
  lossless: "Lossless",
};

const PROTOCOLS: ProtocolKey[] = ["usenet", "torrent"];
const PROTOCOL_LABELS: Record<ProtocolKey, string> = {
  usenet: "Usenet",
  torrent: "Torrent",
};

// RES_KEYS maps a numeric resolution to its AvailabilityPreview field —
// the DTO models the 4-resolution axis as four named fields (res2160/
// res1080/res720/res480), not a map (see internal/apidto/dto.go's doc
// comment on why: flat structs, codegen risk with map types), so every
// numeric-resolution lookup goes through this table.
const RES_KEYS: Record<number, keyof AvailabilityPreview> = {
  2160: "res2160",
  1080: "res1080",
  720: "res720",
  480: "res480",
};

// resKey resolves a numeric resolution to its AvailabilityPreview field,
// falling back to res480 for anything outside the fixed 4-resolution axis —
// unreachable in practice (every call site only ever passes a value drawn
// from RESOLUTION_DISPLAY/RESOLUTIONS_DESC), but RES_KEYS' lookup type is
// `keyof AvailabilityPreview | undefined` under this project's
// noUncheckedIndexedAccess, so a safe default keeps candidateAt total.
function resKey(r: number): keyof AvailabilityPreview {
  return RES_KEYS[r] ?? "res480";
}

// candidateAt reads one (resolution, tier, protocol) cell of the preview
// grid — undefined when autograb.Select found no qualifying release for that
// exact combination (the backend's TierAvailability.usenet/torrent are
// already `?` — nil on the wire).
export function candidateAt(
  preview: AvailabilityPreview,
  resolution: number,
  tier: TierKey,
  protocol: ProtocolKey,
): AvailabilityCandidate | undefined {
  return preview[resKey(resolution)][tier][protocol];
}

// pickProtocol picks whichever protocol has a candidate at (resolution,
// tier), preferring torrent when both do — the task's own stated default
// ("prefer torrent if both available") since the plan doesn't specify one.
function pickProtocol(
  preview: AvailabilityPreview,
  resolution: number,
  tier: TierKey,
): { protocol: ProtocolKey; candidate: AvailabilityCandidate } | undefined {
  const torrent = candidateAt(preview, resolution, tier, "torrent");
  if (torrent) return { protocol: "torrent", candidate: torrent };
  const usenet = candidateAt(preview, resolution, tier, "usenet");
  if (usenet) return { protocol: "usenet", candidate: usenet };
  return undefined;
}

const isTierKey = (t: string): t is TierKey =>
  (TIERS as string[]).includes(t);

// computeDefaults picks the popup's initial (resolution, tier, protocol)
// selection: first try the mode's configured quality-prefs combination
// (Movies/Series only — Adult has no quality-prefs endpoint, see
// QualityPrefsResponse's doc comment: "Movies/Series only — Adult has no
// Search workflow"); prefs.maxResolution of 0 means "no cap," which has no
// single exact resolution to try, so that also falls through. If that exact
// combination has no candidate (or prefs are absent/inapplicable), fall back
// to the first available combination in the grid — never an all-nil
// default when a better one exists. Scan order (own judgment call, the plan
// doesn't specify one): resolution descending (highest quality first), then
// tier in the fixed low→lossless order, protocol torrent-preferred.
export function computeDefaults(
  preview: AvailabilityPreview,
  prefs?: { tier: string; maxResolution: number },
): { resolution: number; tier: TierKey; protocol: ProtocolKey } | undefined {
  if (
    prefs &&
    prefs.maxResolution !== 0 &&
    prefs.maxResolution in RES_KEYS &&
    isTierKey(prefs.tier)
  ) {
    const found = pickProtocol(preview, prefs.maxResolution, prefs.tier);
    if (found) {
      return { resolution: prefs.maxResolution, tier: prefs.tier, protocol: found.protocol };
    }
  }
  for (const r of RESOLUTIONS_DESC) {
    for (const t of TIERS) {
      const found = pickProtocol(preview, r, t);
      if (found) return { resolution: r, tier: t, protocol: found.protocol };
    }
  }
  return undefined;
}

export const DetailPopup: Component<{
  target: DetailTarget;
  onClose: () => void;
}> = (props) => {
  const mode = () => props.target.mode;
  const item = () => props.target.item;

  // Series needs season/episode BEFORE the availability fetch can run.
  const [seasonEpisode, setSeasonEpisode] = createSignal<
    { season: number; episode: number } | null
  >(null);
  const ready = () => mode() !== "series" || seasonEpisode() !== null;

  // Movies/Series' configured quality-tier/max-resolution prefs seed the
  // default selection. Adult has no quality-prefs endpoint at all, so this
  // resource never fetches for Adult (source returns null) — computeDefaults
  // then falls straight to its "first available combination" scan.
  const [prefs] = createResource(
    () => (mode() !== "adult" ? mode() : null),
    async (m) => {
      try {
        return await fetchQualityPrefs(m as Exclude<Mode, "adult">);
      } catch {
        return undefined;
      }
    },
  );

  const [preview] = createResource(
    () => (ready() ? { m: mode(), i: item(), se: seasonEpisode() } : null),
    ({ m, i, se }) => {
      if (m === "adult") {
        const scene = i as AdultDiscoverItem;
        return fetchAvailabilityPreview("adult", {
          title: scene.title,
          studio: scene.studio,
          durationSeconds: scene.durationSeconds,
        });
      }
      const title = i as DiscoverItem;
      return fetchAvailabilityPreview(m, {
        title: title.title,
        tmdbId: title.id,
        season: se?.season,
        episode: se?.episode,
      });
    },
  );

  const [resolution, setResolution] = createSignal<number | null>(null);
  const [tier, setTier] = createSignal<TierKey | null>(null);
  const [protocol, setProtocol] = createSignal<ProtocolKey | null>(null);
  const [defaulted, setDefaulted] = createSignal(false);

  // Seed the three selectors once, the first time the preview grid AND (for
  // Movies/Series) the quality-prefs fetch have both settled — never again
  // afterward, so an operator's own later clicks aren't overwritten.
  createEffect(() => {
    if (defaulted()) return;
    if (mode() !== "adult" && prefs.loading) return;
    const p = preview();
    if (!p) return;
    const d = computeDefaults(p, prefs());
    setResolution(d?.resolution ?? null);
    setTier(d?.tier ?? null);
    setProtocol(d?.protocol ?? null);
    setDefaulted(true);
  });

  const resolutionEnabled = (r: number) => {
    const p = preview();
    const t = tier();
    const pr = protocol();
    if (!p || !t || !pr) return false;
    return !!candidateAt(p, r, t, pr);
  };
  const tierEnabled = (t: TierKey) => {
    const p = preview();
    const r = resolution();
    const pr = protocol();
    if (!p || !r || !pr) return false;
    return !!candidateAt(p, r, t, pr);
  };
  const protocolEnabled = (pr: ProtocolKey) => {
    const p = preview();
    const r = resolution();
    const t = tier();
    if (!p || !r || !t) return false;
    return !!candidateAt(p, r, t, pr);
  };
  const selectedCandidate = (): AvailabilityCandidate | undefined => {
    const p = preview();
    const r = resolution();
    const t = tier();
    const pr = protocol();
    if (!p || !r || !t || !pr) return undefined;
    return candidateAt(p, r, t, pr);
  };

  // posterSrc/overviewText/ratingValue normalize the two item shapes into one
  // rendering surface. Adult scenes carry no `overview` field at all
  // (AdultDiscoverItem is id/title/studio/date/image/durationSeconds/rating/
  // source — see dto.gen.ts) — rather than fabricate a description, the
  // Adult body shows the same studio/date summary AdultCard's subtitle
  // already displays.
  const posterSrc = () =>
    mode() === "adult"
      ? proxyImage((item() as AdultDiscoverItem).image)
      : tmdbPoster((item() as DiscoverItem).posterPath);
  const overviewText = () =>
    mode() === "adult"
      ? [
          (item() as AdultDiscoverItem).studio,
          yearOf((item() as AdultDiscoverItem).date),
        ]
          .filter(Boolean)
          .join(" · ") || "No description available."
      : (item() as DiscoverItem).overview || "No description available.";
  const ratingValue = () =>
    mode() === "adult"
      ? (item() as AdultDiscoverItem).rating
      : (item() as DiscoverItem).voteAverage;

  const [grabbing, setGrabbing] = createSignal(false);
  const [grabError, setGrabError] = createSignal("");
  const [grabbed, setGrabbed] = createSignal(false);

  // grab mirrors GrabDialog.pickManual (shared.tsx) exactly: resolve the
  // mode's root folder first, then manualGrab with the selected candidate's
  // indexer/protocol/downloadUrl plus the item's own identity fields.
  const grab = async () => {
    const c = selectedCandidate();
    if (!c) return;
    setGrabError("");
    setGrabbing(true);
    try {
      const root = await libraryRootFolder(mode());
      if (!root) {
        throw new Error(
          "no root folder configured for this mode — set one in Settings first",
        );
      }
      const se = seasonEpisode();
      await manualGrab(mode(), {
        title: item().title,
        tmdbId: mode() !== "adult" ? (item() as DiscoverItem).id : undefined,
        seasonNumber: mode() === "series" ? se?.season : undefined,
        episodeNumber: mode() === "series" ? se?.episode : undefined,
        seasonSpecified: mode() === "series" ? true : undefined,
        indexer: c.indexer,
        protocol: c.protocol,
        downloadUrl: c.downloadUrl,
        rootFolderPath: root,
      });
      setGrabbed(true);
    } catch (e) {
      setGrabError((e as Error).message);
    } finally {
      setGrabbing(false);
    }
  };

  return (
    <Modal title={item().title} onClose={props.onClose}>
      <Show
        when={ready()}
        fallback={
          <div>
            <Muted class="mb-2">
              Pick a season (and optionally an episode) to check availability.
            </Muted>
            <SeasonEpisodePicker
              onSubmit={(season, episode) => setSeasonEpisode({ season, episode })}
            />
          </div>
        }
      >
        <div class="flex gap-3">
          <div class="h-28 w-20 shrink-0 overflow-hidden rounded-lg border border-border bg-surface-2">
            <Show when={posterSrc()} fallback={<TextPoster label={item().title} />}>
              <img
                src={posterSrc()}
                alt={item().title}
                class="h-full w-full object-cover"
              />
            </Show>
          </div>
          <div class="min-w-0 flex-1">
            <Show when={ratingValue() > 0}>
              <div class="text-xs text-muted">★ {ratingValue().toFixed(1)}</div>
            </Show>
            <p class="mt-1 line-clamp-4 text-sm text-muted">{overviewText()}</p>
          </div>
        </div>

        <Show
          when={!preview.loading}
          fallback={<Muted class="mt-3">Checking availability…</Muted>}
        >
          <Show
            when={!preview.error}
            fallback={
              <ErrorText>{(preview.error as Error)?.message}</ErrorText>
            }
          >
            {/* No further Show(when={preview()}) wrapper here — the two
                Shows above already guard !loading && !error, so preview() is
                settled by this point; resolutionEnabled/tierEnabled/
                protocolEnabled/selectedCandidate all independently
                null-guard against a transient unsettled read anyway. */}
            <div class="mt-3">
              <div class="mb-2">
                <div class={labelClass}>Resolution</div>
                <div class="mt-1 flex flex-wrap gap-1.5">
                  <For each={RESOLUTION_DISPLAY}>
                    {(r) => (
                      <button
                        type="button"
                        class="rounded-md border px-2 py-1 text-xs font-medium disabled:opacity-40"
                        classList={{
                          "border-accent bg-accent text-accent-fg": resolution() === r,
                          "border-border bg-surface-2 text-fg": resolution() !== r,
                        }}
                        disabled={!resolutionEnabled(r)}
                        onClick={() => setResolution(r)}
                      >
                        {RESOLUTION_LABELS[r]}
                      </button>
                    )}
                  </For>
                </div>
              </div>

              <div class="mb-2">
                <div class={labelClass}>Quality tier</div>
                <div class="mt-1 flex flex-wrap gap-1.5">
                  <For each={TIERS}>
                    {(t) => (
                      <button
                        type="button"
                        class="rounded-md border px-2 py-1 text-xs font-medium disabled:opacity-40"
                        classList={{
                          "border-accent bg-accent text-accent-fg": tier() === t,
                          "border-border bg-surface-2 text-fg": tier() !== t,
                        }}
                        disabled={!tierEnabled(t)}
                        onClick={() => setTier(t)}
                      >
                        {TIER_LABELS[t]}
                      </button>
                    )}
                  </For>
                </div>
              </div>

              <div class="mb-2">
                <div class={labelClass}>Protocol</div>
                <div class="mt-1 flex flex-wrap gap-1.5">
                  <For each={PROTOCOLS}>
                    {(pr) => (
                      <button
                        type="button"
                        class="rounded-md border px-2 py-1 text-xs font-medium disabled:opacity-40"
                        classList={{
                          "border-accent bg-accent text-accent-fg": protocol() === pr,
                          "border-border bg-surface-2 text-fg": protocol() !== pr,
                        }}
                        disabled={!protocolEnabled(pr)}
                        onClick={() => setProtocol(pr)}
                      >
                        {PROTOCOL_LABELS[pr]}
                      </button>
                    )}
                  </For>
                </div>
              </div>

              <div class="mt-4 flex items-center justify-end gap-3">
                <Show when={grabError()}>
                  <ErrorText>{grabError()}</ErrorText>
                </Show>
                <Show
                  when={!grabbed()}
                  fallback={
                    <div class="text-sm text-ok">
                      Grabbed “{selectedCandidate()?.title}”.
                    </div>
                  }
                >
                  <Button
                    variant="primary"
                    onClick={() => void grab()}
                    disabled={!selectedCandidate() || grabbing()}
                  >
                    {grabbing() ? "Grabbing…" : "Grab"}
                  </Button>
                </Show>
              </div>
            </div>
          </Show>
        </Show>
      </Show>
    </Modal>
  );
};
