// DetailPopup tests: the pure availability-grid derivation logic
// (candidateAt/computeDefaults) plus the rendered popup's selector
// disabled-state behavior, Series' season/episode gating, Adult's
// no-quality-prefs default path, and the Grab wiring — mirroring this
// repo's existing Discover test conventions (stubFetch/jsonResponse from
// Discover.test.tsx / Discover.grab.test.tsx).

import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@solidjs/testing-library";
import type { AdultDiscoverItem, AvailabilityCandidate, AvailabilityPreview, DiscoverItem } from "@dto";
import { DetailPopup, type DetailTarget, candidateAt, computeDefaults } from "./DetailPopup";

const jsonResponse = (obj: unknown): Response =>
  new Response(JSON.stringify(obj), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const candidate = (over: Partial<AvailabilityCandidate> = {}): AvailabilityCandidate => ({
  guid: "g1",
  title: "Release.Title",
  indexer: "IndexerA",
  protocol: "torrent",
  size: 1000,
  seeders: 10,
  downloadUrl: "magnet:?xt=urn:btih:abc",
  publishDate: "2024-01-01",
  score: 5,
  ...over,
});

// emptyPreview builds the full 4×4×2 grid with every cell nil — the same
// all-nil shape the real handler emits when nothing qualified anywhere.
// Tests mutate individual cells to place candidates precisely.
const emptyTier = () => ({ usenet: undefined, torrent: undefined });
const emptyRes = () => ({
  low: emptyTier(),
  medium: emptyTier(),
  high: emptyTier(),
  lossless: emptyTier(),
});
const emptyPreview = (): AvailabilityPreview => ({
  res2160: emptyRes(),
  res1080: emptyRes(),
  res720: emptyRes(),
  res480: emptyRes(),
});

const movie = (over: Partial<DiscoverItem> = {}): DiscoverItem => ({
  id: 1,
  title: "Hero Movie",
  posterPath: "/p.jpg",
  overview: "An overview.",
  releaseDate: "2024-05-01",
  voteAverage: 7.8,
  mediaType: "movie",
  ...over,
});

const adultScene = (over: Partial<AdultDiscoverItem> = {}): AdultDiscoverItem => ({
  id: "s1",
  title: "A Scene",
  studio: "Vixen",
  date: "2023-01-01",
  image: "",
  durationSeconds: 1800,
  rating: 4,
  source: "tpdb",
  ...over,
});

type Call = { url: string; method: string; body: unknown };
type Handler = (url: string, init?: RequestInit) => Response | Promise<Response>;
const stubFetch = (handler: Handler) => {
  const calls: Call[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      method: (init?.method ?? "GET").toUpperCase(),
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    });
    return handler(url, init);
  });
  vi.stubGlobal("fetch", fn);
  return calls;
};

afterEach(() => vi.unstubAllGlobals());

describe("DetailPopup — availability grid derivation (pure logic)", () => {
  it("candidateAt reads the exact (resolution, tier, protocol) cell and undefined for every other cell", () => {
    const preview = emptyPreview();
    preview.res1080.high.torrent = candidate({ title: "T1" });

    expect(candidateAt(preview, 1080, "high", "torrent")?.title).toBe("T1");
    expect(candidateAt(preview, 1080, "high", "usenet")).toBeUndefined();
    expect(candidateAt(preview, 1080, "low", "torrent")).toBeUndefined();
    expect(candidateAt(preview, 720, "high", "torrent")).toBeUndefined();
  });

  it("computeDefaults picks the exact quality-prefs (maxResolution, tier) combination, preferring torrent when both protocols qualify", () => {
    const preview = emptyPreview();
    preview.res1080.high.torrent = candidate({ title: "PreferredTorrent" });
    preview.res1080.high.usenet = candidate({ title: "AlsoUsenet" });

    expect(computeDefaults(preview, { tier: "high", maxResolution: 1080 })).toEqual({
      resolution: 1080,
      tier: "high",
      protocol: "torrent",
    });
  });

  it("computeDefaults falls back to usenet at the prefs combination when only usenet qualifies there", () => {
    const preview = emptyPreview();
    preview.res1080.high.usenet = candidate({ title: "OnlyUsenet" });

    expect(computeDefaults(preview, { tier: "high", maxResolution: 1080 })).toEqual({
      resolution: 1080,
      tier: "high",
      protocol: "usenet",
    });
  });

  it("computeDefaults falls back to the first available grid combination when the prefs combination has no candidate", () => {
    const preview = emptyPreview();
    preview.res720.medium.torrent = candidate({ title: "Fallback" });

    // Prefs point at (2160, lossless), which has nothing — must fall back,
    // not stay stuck on an all-nil default.
    expect(computeDefaults(preview, { tier: "lossless", maxResolution: 2160 })).toEqual({
      resolution: 720,
      tier: "medium",
      protocol: "torrent",
    });
  });

  it("computeDefaults treats maxResolution 0 (no cap) as having no exact combination to try — straight to the grid scan", () => {
    const preview = emptyPreview();
    preview.res480.low.usenet = candidate({ title: "OnlyOption" });

    expect(computeDefaults(preview, { tier: "high", maxResolution: 0 })).toEqual({
      resolution: 480,
      tier: "low",
      protocol: "usenet",
    });
  });

  it("computeDefaults returns undefined when nothing in the grid has a candidate anywhere", () => {
    expect(computeDefaults(emptyPreview(), { tier: "high", maxResolution: 1080 })).toBeUndefined();
  });

  it("computeDefaults works with no prefs at all (Adult's path) — goes straight to the grid scan", () => {
    const preview = emptyPreview();
    preview.res2160.lossless.torrent = candidate({ title: "BestAvailable" });

    expect(computeDefaults(preview)).toEqual({
      resolution: 2160,
      tier: "lossless",
      protocol: "torrent",
    });
  });
});

describe("DetailPopup — selector disabled-state derivation (rendered)", () => {
  it("disables a resolution/protocol option with no candidate at the CURRENT other-two-axes combination, not just any candidate anywhere", async () => {
    const preview = emptyPreview();
    // Default combo lands on (1080, high) via prefs. Torrent qualifies there;
    // usenet does not — even though usenet exists elsewhere in the grid.
    preview.res1080.high.torrent = candidate({ title: "Preferred1080HighTorrent" });
    preview.res1080.low.usenet = candidate({ title: "UsenetAtADifferentTier" });
    // 720p also qualifies at (high, torrent) — the CURRENT combo — so it must
    // be enabled; 2160p/480p have nothing at (high, torrent) anywhere.
    preview.res720.high.torrent = candidate({ title: "720HighTorrent" });

    stubFetch((url) => {
      if (url.includes("/discover/availability")) return jsonResponse(preview);
      if (url.includes("/quality-prefs"))
        return jsonResponse({ tier: "high", maxResolution: 1080 });
      throw new Error("unexpected fetch: " + url);
    });

    const target: DetailTarget = { mode: "movies", item: movie() };
    render(() => <DetailPopup target={target} onClose={() => {}} />);

    expect(await screen.findByRole("button", { name: "Grab" })).not.toBeDisabled();

    expect(screen.getByRole("button", { name: "720p" })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "480p" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "2160p" })).toBeDisabled();

    // Usenet exists in the grid (at a different tier), but not at the
    // CURRENT (1080, high) combination — must render disabled.
    expect(screen.getByRole("button", { name: "Usenet" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Torrent" })).not.toBeDisabled();
  });

  it("re-derives every selector's disabled state on a resolution switch, without any additional fetch", async () => {
    const preview = emptyPreview();
    preview.res1080.high.torrent = candidate({ title: "A" });
    preview.res720.high.torrent = candidate({ title: "B" });
    // Only reachable via a DIFFERENT tier/protocol than the current selection.
    preview.res720.medium.usenet = candidate({ title: "C" });

    const calls = stubFetch((url) => {
      if (url.includes("/discover/availability")) return jsonResponse(preview);
      if (url.includes("/quality-prefs"))
        return jsonResponse({ tier: "high", maxResolution: 1080 });
      throw new Error("unexpected fetch: " + url);
    });

    const target: DetailTarget = { mode: "movies", item: movie() };
    render(() => <DetailPopup target={target} onClose={() => {}} />);
    await screen.findByRole("button", { name: "Grab" });

    const availabilityCalls = () =>
      calls.filter((c) => c.url.includes("/discover/availability")).length;
    expect(availabilityCalls()).toBe(1);

    fireEvent.click(screen.getByRole("button", { name: "720p" }));

    // Switching a selector never refetches — only re-derives disabled state
    // against the already-fetched grid.
    expect(availabilityCalls()).toBe(1);

    // At the new (720, high, torrent) combination: "medium" and "usenet"
    // only have a candidate at a DIFFERENT combination (720/medium/usenet),
    // not this one — both must now be disabled.
    expect(screen.getByRole("button", { name: "Medium" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Usenet" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "High" })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "Torrent" })).not.toBeDisabled();
  });

  it("Series gates the availability fetch behind season/episode — no call until the picker is submitted", async () => {
    const preview = emptyPreview();
    preview.res1080.high.torrent = candidate();

    const calls = stubFetch((url) => {
      if (url.includes("/discover/availability")) return jsonResponse(preview);
      if (url.includes("/quality-prefs"))
        return jsonResponse({ tier: "high", maxResolution: 1080 });
      throw new Error("unexpected fetch: " + url);
    });

    const target: DetailTarget = {
      mode: "series",
      item: movie({ id: 5, title: "A Series", mediaType: "tv" }),
    };
    render(() => <DetailPopup target={target} onClose={() => {}} />);

    expect(screen.getByLabelText("Season")).toBeInTheDocument();
    expect(calls.filter((c) => c.url.includes("/discover/availability"))).toHaveLength(0);

    fireEvent.input(screen.getByLabelText("Season"), { target: { value: "2" } });
    fireEvent.input(screen.getByLabelText("Episode"), { target: { value: "4" } });
    fireEvent.click(screen.getByText("Go"));

    expect(await screen.findByRole("button", { name: "Grab" })).toBeInTheDocument();
    const availCall = calls.find((c) => c.url.includes("/discover/availability"));
    expect(availCall?.url).toContain("season=2");
    expect(availCall?.url).toContain("episode=4");
  });

  it("Adult never calls quality-prefs (Movies/Series-only endpoint) and defaults from the first available grid combination instead", async () => {
    const preview = emptyPreview();
    preview.res720.medium.usenet = candidate({ title: "AdultOnlyOption" });

    const calls = stubFetch((url) => {
      if (url.includes("/discover/availability")) return jsonResponse(preview);
      if (url.includes("/quality-prefs")) {
        throw new Error("must not call quality-prefs for Adult");
      }
      throw new Error("unexpected fetch: " + url);
    });

    const target: DetailTarget = { mode: "adult", item: adultScene() };
    render(() => <DetailPopup target={target} onClose={() => {}} />);

    expect(await screen.findByRole("button", { name: "Grab" })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "720p" })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "Medium" })).not.toBeDisabled();
    expect(screen.getByRole("button", { name: "Usenet" })).not.toBeDisabled();

    const availCall = calls.find((c) => c.url.includes("/discover/availability"));
    expect(availCall?.url).toContain("studio=Vixen");
    expect(availCall?.url).toContain("durationSeconds=1800");
    expect(availCall?.url).not.toContain("tmdbId");
  });
});

describe("DetailPopup — Grab wiring (mirrors GrabDialog.pickManual's call shape)", () => {
  it("resolves the root folder, then calls manualGrab with the selected candidate's fields", async () => {
    const preview = emptyPreview();
    preview.res1080.high.torrent = candidate({
      title: "Hero.Movie.1080p",
      indexer: "IndexerA",
      protocol: "torrent",
      downloadUrl: "magnet:?xt=urn:btih:abc",
    });

    const calls = stubFetch((url) => {
      if (url.includes("/discover/availability")) return jsonResponse(preview);
      if (url.includes("/quality-prefs"))
        return jsonResponse({ tier: "high", maxResolution: 1080 });
      if (url.includes("/library/root-folder")) return jsonResponse({ path: "/movies" });
      if (url.includes("/search/grab"))
        return jsonResponse({ id: 9, mode: "movies", title: "Hero Movie", status: "queued" });
      throw new Error("unexpected fetch: " + url);
    });

    const target: DetailTarget = { mode: "movies", item: movie({ id: 42, title: "Hero Movie" }) };
    render(() => <DetailPopup target={target} onClose={() => {}} />);

    fireEvent.click(await screen.findByRole("button", { name: "Grab" }));

    expect(await screen.findByText(/Grabbed/)).toBeInTheDocument();
    const grabCall = calls.find((c) => c.url.includes("/search/grab"));
    expect(grabCall?.body).toMatchObject({
      title: "Hero Movie",
      tmdbId: 42,
      indexer: "IndexerA",
      protocol: "torrent",
      downloadUrl: "magnet:?xt=urn:btih:abc",
      rootFolderPath: "/movies",
    });
  });
});
