// SliderAdmin tests — create (fixed feed + reference-list-backed filter
// types), edit, delete, reorder (button-based), enabled toggle, and the
// target auto-correction for studio (movie-only) / network (tv-only) filter
// types. Conventions mirror Settings.test.tsx (stubFetch/defaultGet/Call).

import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@solidjs/testing-library";
import { SliderAdminSection } from "./SliderAdmin";

const jsonResponse = (obj: unknown): Response =>
  new Response(JSON.stringify(obj), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
const noContent = (): Response => new Response(null, { status: 204 });

type Call = { url: string; method: string; body: unknown };
type Override = (
  url: string,
  init?: RequestInit,
) => Response | undefined | Promise<Response | undefined>;

const slider = (over: Partial<Record<string, unknown>> = {}) => ({
  id: 1,
  title: "Trending Movies",
  filterType: "trending",
  filterValue: "",
  target: "mixed",
  sortOrder: 0,
  enabled: true,
  createdAt: "2026-07-14T00:00:00Z",
  updatedAt: "2026-07-14T00:00:00Z",
  ...over,
});

function defaultGet(url: string): Response | undefined {
  if (url.includes("/api/discover/sliders")) return jsonResponse([]);
  if (url.includes("/discover/genres")) return jsonResponse([]);
  if (url.includes("/api/discover/studios")) return jsonResponse([]);
  if (url.includes("/api/discover/networks")) return jsonResponse([]);
  if (url.includes("/api/discover/keywords")) return jsonResponse([]);
  return undefined;
}

const stubFetch = (override?: Override) => {
  const calls: Call[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({
      url,
      method,
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    });
    if (override) {
      const r = await override(url, init);
      if (r) return r;
    }
    if (method === "GET") {
      const d = defaultGet(url);
      if (d) return d;
    }
    return noContent();
  });
  vi.stubGlobal("fetch", fn);
  vi.stubGlobal("confirm", vi.fn(() => true));
  return calls;
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("SliderAdminSection — list", () => {
  it("shows the empty state with no sliders", async () => {
    stubFetch();
    render(() => <SliderAdminSection />);
    expect(await screen.findByText("No custom sliders yet.")).toBeInTheDocument();
  });

  it("lists an existing slider with its summary", async () => {
    stubFetch((url) => {
      if (url.includes("/api/discover/sliders") && !url.includes("/reorder"))
        return jsonResponse([slider({ id: 1, title: "Trending Movies" })]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    expect(await screen.findByText("Trending Movies")).toBeInTheDocument();
    expect(screen.getByText(/Trending · mixed/)).toBeInTheDocument();
  });
});

describe("SliderAdminSection — create", () => {
  it("creates a fixed-feed slider with no filter value required", async () => {
    const calls = stubFetch();
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    fireEvent.input(screen.getByLabelText("Slider title"), {
      target: { value: "My Upcoming Row" },
    });
    // Default filterType is "upcoming" (a fixed feed) — no value field shown.
    expect(screen.queryByLabelText("Genre")).toBeNull();
    fireEvent.click(screen.getByText("Create slider"));
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.method === "POST" && c.url.endsWith("/api/discover/sliders"),
        ),
      ).toBe(true),
    );
    const post = calls.find(
      (c) => c.method === "POST" && c.url.endsWith("/api/discover/sliders"),
    )!;
    expect(post.body).toEqual({
      title: "My Upcoming Row",
      filterType: "upcoming",
      filterValue: "",
      target: "mixed",
      enabled: true,
    });
  });

  it("rejects a genre slider with no genre selected (no POST fired)", async () => {
    const calls = stubFetch();
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    fireEvent.input(screen.getByLabelText("Slider title"), {
      target: { value: "By Genre" },
    });
    fireEvent.change(screen.getByLabelText("Filter type"), {
      target: { value: "genre" },
    });
    fireEvent.click(screen.getByText("Create slider"));
    await screen.findByText(/select a genre value first/i);
    expect(
      calls.some(
        (c) => c.method === "POST" && c.url.endsWith("/api/discover/sliders"),
      ),
    ).toBe(false);
  });

  it("creates a genre slider once a genre is picked from the fetched list", async () => {
    const calls = stubFetch((url) => {
      if (url.includes("/api/modes/movies/discover/genres"))
        return jsonResponse([{ id: 28, name: "Action" }]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    fireEvent.input(screen.getByLabelText("Slider title"), {
      target: { value: "Action Movies" },
    });
    fireEvent.change(screen.getByLabelText("Filter type"), {
      target: { value: "genre" },
    });
    const genreSelect = (await screen.findByLabelText(
      "Genre",
    )) as HTMLSelectElement;
    await waitFor(() =>
      expect(within(genreSelect).getByText("Action")).toBeInTheDocument(),
    );
    fireEvent.change(genreSelect, { target: { value: "28" } });
    fireEvent.click(screen.getByText("Create slider"));
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.method === "POST" && c.url.endsWith("/api/discover/sliders"),
        ),
      ).toBe(true),
    );
    const post = calls.find(
      (c) => c.method === "POST" && c.url.endsWith("/api/discover/sliders"),
    )!;
    expect(post.body).toEqual({
      title: "Action Movies",
      filterType: "genre",
      filterValue: "28",
      target: "mixed",
      enabled: true,
    });
  });

  it("keyword search only fetches on Search click, not per keystroke", async () => {
    const calls = stubFetch((url) => {
      if (url.includes("/api/discover/keywords"))
        return jsonResponse([{ id: 99, name: "heist" }]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    fireEvent.change(screen.getByLabelText("Filter type"), {
      target: { value: "keyword" },
    });
    const input = await screen.findByLabelText("Keyword search");
    fireEvent.input(input, { target: { value: "h" } });
    fireEvent.input(input, { target: { value: "he" } });
    fireEvent.input(input, { target: { value: "heist" } });
    // Typing alone must not fire any /api/discover/keywords request.
    expect(calls.some((c) => c.url.includes("/api/discover/keywords"))).toBe(
      false,
    );
    fireEvent.click(screen.getByText("Search"));
    await waitFor(() =>
      expect(
        calls.some((c) => c.url.includes("/api/discover/keywords?q=heist")),
      ).toBe(true),
    );
    expect(
      calls.filter((c) => c.url.includes("/api/discover/keywords")),
    ).toHaveLength(1);
  });

  it("clears a stale filter value when switching between reference-list filter types", async () => {
    stubFetch((url) => {
      if (url.includes("/api/discover/studios"))
        return jsonResponse([{ id: 1, name: "A24" }]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    fireEvent.change(screen.getByLabelText("Filter type"), {
      target: { value: "studio" },
    });
    const studioSelect = (await screen.findByLabelText(
      "Studio",
    )) as HTMLSelectElement;
    await waitFor(() =>
      expect(within(studioSelect).getByText("A24")).toBeInTheDocument(),
    );
    fireEvent.change(studioSelect, { target: { value: "1" } });
    expect(studioSelect.value).toBe("1");
    // Switching to network must not carry the studio id (1) over as a
    // network id — the value resets so the operator must pick explicitly.
    fireEvent.change(screen.getByLabelText("Filter type"), {
      target: { value: "network" },
    });
    const networkSelect = (await screen.findByLabelText(
      "Network",
    )) as HTMLSelectElement;
    expect(networkSelect.value).toBe("");
  });

  it("auto-corrects target when switching to studio (movie-only)", async () => {
    stubFetch();
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    const targetSelect = (await screen.findByLabelText(
      "Target",
    )) as HTMLSelectElement;
    fireEvent.change(targetSelect, { target: { value: "tv" } });
    expect(targetSelect.value).toBe("tv");
    fireEvent.change(screen.getByLabelText("Filter type"), {
      target: { value: "studio" },
    });
    // "tv" is not a valid target for studio (movie-only) — corrected to "movie".
    await waitFor(() => expect(targetSelect.value).toBe("movie"));
    // The now-invalid "tv" option must not even be selectable.
    expect(
      within(targetSelect).queryByRole("option", { name: "tv" }),
    ).toBeNull();
  });

  it("cancel closes the form without posting", async () => {
    const calls = stubFetch();
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("+ New slider"));
    fireEvent.click(screen.getByText("Cancel"));
    expect(screen.queryByLabelText("Slider title")).toBeNull();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });
});

describe("SliderAdminSection — edit", () => {
  it("Edit pre-fills the form and Save PUTs the updated slider", async () => {
    const calls = stubFetch((url) => {
      if (url.includes("/api/discover/sliders") && !url.includes("/reorder"))
        return jsonResponse([
          slider({ id: 5, title: "Old Title", filterType: "popular" }),
        ]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("Edit"));
    const titleInput = (await screen.findByLabelText(
      "Slider title",
    )) as HTMLInputElement;
    expect(titleInput.value).toBe("Old Title");
    fireEvent.input(titleInput, { target: { value: "New Title" } });
    fireEvent.click(screen.getByText("Save changes"));
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.method === "PUT" && c.url.includes("/api/discover/sliders/5"),
        ),
      ).toBe(true),
    );
    const put = calls.find(
      (c) => c.method === "PUT" && c.url.includes("/api/discover/sliders/5"),
    )!;
    expect(put.body).toEqual({
      title: "New Title",
      filterType: "popular",
      filterValue: "",
      target: "mixed",
      enabled: true,
    });
  });
});

describe("SliderAdminSection — delete", () => {
  it("Delete confirms then DELETEs that slider", async () => {
    const calls = stubFetch((url) => {
      if (url.includes("/api/discover/sliders") && !url.includes("/reorder"))
        return jsonResponse([slider({ id: 7, title: "Doomed Row" })]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    fireEvent.click(await screen.findByText("Delete"));
    await waitFor(() =>
      expect(
        calls.some(
          (c) =>
            c.method === "DELETE" && c.url.includes("/api/discover/sliders/7"),
        ),
      ).toBe(true),
    );
  });
});

describe("SliderAdminSection — reorder", () => {
  it("moving the second slider up sends the full new id order", async () => {
    const calls = stubFetch((url) => {
      if (url.includes("/api/discover/sliders") && !url.includes("/reorder"))
        return jsonResponse([
          slider({ id: 1, title: "First" }),
          slider({ id: 2, title: "Second" }),
        ]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    await screen.findByText("First");
    const secondRow = screen.getByText("Second").closest("li")!;
    fireEvent.click(within(secondRow).getByLabelText("Move Second up"));
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.method === "POST" && c.url.includes("/reorder"),
        ),
      ).toBe(true),
    );
    const reorder = calls.find(
      (c) => c.method === "POST" && c.url.includes("/reorder"),
    )!;
    expect(reorder.body).toEqual({ ids: [2, 1] });
  });

  it("the first slider's Up button is disabled", async () => {
    stubFetch((url) => {
      if (url.includes("/api/discover/sliders") && !url.includes("/reorder"))
        return jsonResponse([slider({ id: 1, title: "Only One" })]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    await screen.findByText("Only One");
    expect(screen.getByLabelText("Move Only One up")).toBeDisabled();
    expect(screen.getByLabelText("Move Only One down")).toBeDisabled();
  });
});

describe("SliderAdminSection — enabled toggle", () => {
  it("toggling the checkbox PUTs the slider with enabled flipped", async () => {
    const calls = stubFetch((url) => {
      if (url.includes("/api/discover/sliders") && !url.includes("/reorder"))
        return jsonResponse([
          slider({ id: 3, title: "Togglable", enabled: true }),
        ]);
      return undefined;
    });
    render(() => <SliderAdminSection />);
    const toggle = (await screen.findByLabelText(
      "Togglable enabled",
    )) as HTMLInputElement;
    expect(toggle.checked).toBe(true);
    fireEvent.click(toggle);
    await waitFor(() =>
      expect(
        calls.some(
          (c) => c.method === "PUT" && c.url.includes("/api/discover/sliders/3"),
        ),
      ).toBe(true),
    );
    const put = calls.find(
      (c) => c.method === "PUT" && c.url.includes("/api/discover/sliders/3"),
    )!;
    expect((put.body as { enabled: boolean }).enabled).toBe(false);
  });
});

describe("SliderAdminSection — no bulk actions", () => {
  it("has no save-all / apply-all / delete-all affordance", async () => {
    stubFetch();
    render(() => <SliderAdminSection />);
    await screen.findByText("+ New slider");
    expect(screen.queryByText(/save all/i)).toBeNull();
    expect(screen.queryByText(/apply all/i)).toBeNull();
    expect(screen.queryByText(/delete all/i)).toBeNull();
  });
});
