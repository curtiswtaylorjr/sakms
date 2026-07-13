// Grabs view test — the list renders, and the ADVISORY post-grab review flag
// surfaces as a badge whose copy makes clear the import still succeeded (it is
// not an error state).

import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@solidjs/testing-library";
import type { Grab } from "@dto";
import { Grabs } from "./Grabs";

const jsonResponse = (obj: unknown): Response =>
  new Response(JSON.stringify(obj), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

const grab = (over: Partial<Grab>): Grab => ({
  id: 1,
  mode: "movies",
  title: "Some Movie",
  indexer: "IndexerA",
  protocol: "torrent",
  downloadClient: "qbittorrent",
  status: "imported",
  rootFolderPath: "/movies",
  createdAt: "2026-07-13T00:00:00Z",
  updatedAt: "2026-07-13T00:00:00Z",
  ...over,
});

const stubFetch = (handler: (url: string) => Response) => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => handler(String(input))));
};

afterEach(() => vi.unstubAllGlobals());

describe("Grabs view", () => {
  it("lists grabs for the mode with their status", async () => {
    stubFetch((url) => {
      if (url.includes("/api/modes/movies/grabs"))
        return jsonResponse([grab({ id: 1, title: "Some Movie", status: "downloading" })]);
      throw new Error("unexpected fetch: " + url);
    });

    render(() => <Grabs />);
    expect(await screen.findByText("Some Movie")).toBeInTheDocument();
    expect(screen.getByText("downloading")).toBeInTheDocument();
  });

  it("surfaces flaggedForReview as an advisory badge that says the import was OK", async () => {
    stubFetch((url) => {
      if (url.includes("/api/modes/movies/grabs"))
        return jsonResponse([
          grab({
            id: 2,
            title: "Mislabeled Movie",
            status: "imported",
            flaggedForReview: true,
            flagReason: "imported file runs 12 min but TMDB lists 120 min",
          }),
        ]);
      throw new Error("unexpected fetch: " + url);
    });

    render(() => <Grabs />);
    // Badge is present and its copy explicitly signals "imported OK" — NOT a
    // failure.
    const badge = await screen.findByText(/imported OK/i);
    expect(badge).toBeInTheDocument();
    // The reason detail is shown too.
    expect(screen.getByText(/12 min but TMDB lists 120 min/)).toBeInTheDocument();
  });

  it("shows no review badge for an unflagged grab", async () => {
    stubFetch((url) => {
      if (url.includes("/api/modes/movies/grabs"))
        return jsonResponse([grab({ id: 3, title: "Clean Movie", status: "imported" })]);
      throw new Error("unexpected fetch: " + url);
    });

    render(() => <Grabs />);
    await screen.findByText("Clean Movie");
    expect(screen.queryByText(/imported OK/i)).not.toBeInTheDocument();
  });
});
