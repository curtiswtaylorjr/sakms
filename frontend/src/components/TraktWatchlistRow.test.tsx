// TraktWatchlistRow tests — the row must stay invisible until Trakt reports
// linked, map watchlist items to the right per-item mode (movie→movies,
// show→series), resolve posters by tmdbId the same way LibraryCard does, and
// hand grabs off through the shared onGrab callback so it drives the same
// GrabDialog/season-episode-picker path every other Discover row uses.
//
// PLACEHOLDER CONTRACT: /api/trakt/* is a proposed shape (src/api/trakt.ts),
// not yet confirmed against task #5's real backend routes/DTOs.

import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { TraktWatchlistRow } from "./TraktWatchlistRow";
import type { GrabTarget } from "../screens/Discover";

const jsonResponse = (obj: unknown): Response =>
  new Response(JSON.stringify(obj), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

type Handler = (url: string) => Response | undefined;
const stubFetch = (handler?: Handler) => {
  const fn = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const r = handler?.(url);
    if (r) return r;
    if (url.includes("/api/trakt/status"))
      return jsonResponse({ configured: true, linked: false });
    if (url.includes("/api/trakt/watchlist")) return jsonResponse([]);
    if (url.includes("/poster")) return jsonResponse({ posterPath: "" });
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal("fetch", fn);
  return fn;
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("TraktWatchlistRow", () => {
  it("renders nothing when Trakt isn't linked", async () => {
    stubFetch((url) => {
      if (url.includes("/api/trakt/status"))
        return jsonResponse({ configured: true, linked: false });
      return undefined;
    });
    render(() => <TraktWatchlistRow onGrab={() => {}} />);
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some((c) => String(c[0]).includes("/api/trakt/status")),
      ).toBe(true),
    );
    expect(screen.queryByText("Trakt Watchlist")).toBeNull();
  });

  it("renders the row and its items once linked", async () => {
    stubFetch((url) => {
      if (url.includes("/api/trakt/status"))
        return jsonResponse({ configured: true, linked: true });
      if (url.includes("/api/trakt/watchlist"))
        return jsonResponse([
          { type: "movie", title: "A Watched Movie", year: 2022, tmdbId: 42 },
          { type: "show", title: "A Watched Show", year: 2021, tmdbId: 99 },
        ]);
      return undefined;
    });
    render(() => <TraktWatchlistRow onGrab={() => {}} />);
    expect(await screen.findByText("Trakt Watchlist")).toBeInTheDocument();
    // Each card's title renders twice (the text-fallback poster tile, which
    // shows the title as its own placeholder text here since no poster art
    // is stubbed, AND the card's title line below it) — getAllByText, not
    // getByText, since more than one match is expected here.
    await waitFor(() =>
      expect(screen.getAllByText("A Watched Movie").length).toBeGreaterThan(0),
    );
    expect(screen.getAllByText("A Watched Show").length).toBeGreaterThan(0);
  });

  it("shows the empty-state text when the watchlist is empty", async () => {
    stubFetch((url) => {
      if (url.includes("/api/trakt/status"))
        return jsonResponse({ configured: true, linked: true });
      if (url.includes("/api/trakt/watchlist")) return jsonResponse([]);
      return undefined;
    });
    render(() => <TraktWatchlistRow onGrab={() => {}} />);
    expect(
      await screen.findByText("Your Trakt watchlist is empty."),
    ).toBeInTheDocument();
  });

  it("a movie item grabs directly via onGrab with mode=movies", async () => {
    stubFetch((url) => {
      if (url.includes("/api/trakt/status"))
        return jsonResponse({ configured: true, linked: true });
      if (url.includes("/api/trakt/watchlist"))
        return jsonResponse([
          { type: "movie", title: "A Watched Movie", year: 2022, tmdbId: 42 },
        ]);
      return undefined;
    });
    const grabbed: GrabTarget[] = [];
    render(() => <TraktWatchlistRow onGrab={(t) => grabbed.push(t)} />);
    await waitFor(() =>
      expect(screen.getAllByText("A Watched Movie").length).toBeGreaterThan(0),
    );
    fireEvent.click(screen.getByRole("button", { name: "Grab" }));
    expect(grabbed).toHaveLength(1);
    expect(grabbed[0]!.mode).toBe("movies");
    expect(grabbed[0]!.request).toEqual({
      title: "A Watched Movie",
      tmdbId: 42,
    });
  });

  it("a show item opens the season/episode picker before grabbing (mode=series)", async () => {
    stubFetch((url) => {
      if (url.includes("/api/trakt/status"))
        return jsonResponse({ configured: true, linked: true });
      if (url.includes("/api/trakt/watchlist"))
        return jsonResponse([
          { type: "show", title: "A Watched Show", year: 2021, tmdbId: 99 },
        ]);
      return undefined;
    });
    const grabbed: GrabTarget[] = [];
    render(() => <TraktWatchlistRow onGrab={(t) => grabbed.push(t)} />);
    await waitFor(() =>
      expect(screen.getAllByText("A Watched Show").length).toBeGreaterThan(0),
    );
    // First click reveals the picker rather than grabbing immediately.
    fireEvent.click(screen.getByRole("button", { name: "Grab" }));
    expect(grabbed).toHaveLength(0);
    fireEvent.input(screen.getByLabelText("Season"), { target: { value: "2" } });
    fireEvent.click(screen.getByText("Go"));
    expect(grabbed).toHaveLength(1);
    expect(grabbed[0]!.mode).toBe("series");
    expect(grabbed[0]!.request).toEqual({
      title: "A Watched Show",
      tmdbId: 99,
      seasonNumber: 2,
      episodeNumber: 0,
      seasonSpecified: true,
    });
  });
});
