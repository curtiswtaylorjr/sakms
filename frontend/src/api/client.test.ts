import { afterEach, describe, expect, it, vi } from "vitest";
import { api, apiWithKey, setOnSessionExpired } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
  setOnSessionExpired(null);
});

describe("api() 401 handling", () => {
  it("401 on a non-/api/auth path triggers session-expiry and throws", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("nope", { status: 401 })),
    );
    const onExpired = vi.fn();
    setOnSessionExpired(onExpired);

    await expect(api("/api/modes/movies/discover")).rejects.toThrow(
      "session expired",
    );
    expect(onExpired).toHaveBeenCalledOnce();
  });

  it("401 on an /api/auth/ path does NOT trigger session-expiry (surfaces inline)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response("bad key", {
            status: 401,
            headers: { "Content-Type": "text/plain" },
          }),
      ),
    );
    const onExpired = vi.fn();
    setOnSessionExpired(onExpired);

    // The break-glass recovery path relies on this: a wrong key -> 401 on
    // /api/auth/oidc must surface as an error, not reset the whole app.
    await expect(apiWithKey("/api/auth/oidc", "wrong")).rejects.toThrow(
      "bad key",
    );
    expect(onExpired).not.toHaveBeenCalled();
  });
});

describe("api() body handling", () => {
  it("returns null on 204", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 204 })),
    );
    expect(await api("/api/setup/status")).toBeNull();
  });

  it("throws the server's JSON error message on a non-ok response", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: "kaboom" }), {
            status: 400,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    );
    await expect(api("/api/setup/status")).rejects.toThrow("kaboom");
  });
});

describe("apiWithKey()", () => {
  it("attaches X-Api-Key and preserves the JSON Content-Type", async () => {
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await apiWithKey("/api/auth/oidc", "sk-123", { method: "PUT" });

    const [, init] = fetchMock.mock.calls[0]!;
    const headers = init?.headers as Record<string, string>;
    expect(headers["X-Api-Key"]).toBe("sk-123");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(init?.method).toBe("PUT");
  });
});
