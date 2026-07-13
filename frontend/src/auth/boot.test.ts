import { afterEach, describe, expect, it, vi } from "vitest";
import type { AuthStatusResponse } from "@dto";
import { loadBootState, resolveAuthBranch } from "./boot";

const status = (
  over: Partial<AuthStatusResponse>,
): AuthStatusResponse => ({
  configured: true,
  authenticated: false,
  mode: "password",
  ...over,
});

const jsonResponse = (obj: unknown, init: ResponseInit = {}): Response =>
  new Response(JSON.stringify(obj), {
    status: 200,
    headers: { "Content-Type": "application/json" },
    ...init,
  });

describe("resolveAuthBranch — the 3-way boot branch (pure)", () => {
  it("null status (fetch failed) -> error, never silently authed", () => {
    expect(resolveAuthBranch(null, null)).toEqual({ kind: "error" });
  });

  it("not configured -> setup wizard (regardless of mode)", () => {
    expect(
      resolveAuthBranch(status({ configured: false, mode: "oidc" }), null),
    ).toEqual({ kind: "setup" });
  });

  it("password + no session -> password login", () => {
    expect(
      resolveAuthBranch(status({ mode: "password", authenticated: false }), null),
    ).toEqual({ kind: "login-password" });
  });

  it("oidc + no session -> SSO login, passing the auth_error through", () => {
    expect(
      resolveAuthBranch(
        status({ mode: "oidc", authenticated: false }),
        "state_mismatch",
      ),
    ).toEqual({ kind: "login-oidc", authError: "state_mismatch" });
  });

  it("mode none -> proceed with noneMode true", () => {
    expect(
      resolveAuthBranch(status({ mode: "none", authenticated: true }), null),
    ).toEqual({ kind: "proceed", noneMode: true });
  });

  it("authenticated password session -> proceed, noneMode false", () => {
    expect(
      resolveAuthBranch(status({ mode: "password", authenticated: true }), null),
    ).toEqual({ kind: "proceed", noneMode: false });
  });

  it("authenticated oidc session -> proceed, noneMode false", () => {
    expect(
      resolveAuthBranch(status({ mode: "oidc", authenticated: true }), null),
    ).toEqual({ kind: "proceed", noneMode: false });
  });
});

describe("loadBootState — orchestration + ordering", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("authed -> fetches /api/setup/status and reports connectionsSetupPending", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/auth/status") {
        return jsonResponse(status({ mode: "password", authenticated: true }));
      }
      if (url === "/api/setup/status") {
        return jsonResponse({ dismissed: false });
      }
      throw new Error("unexpected fetch: " + url);
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await loadBootState();
    expect(result).toEqual({
      kind: "app",
      noneMode: false,
      connectionsSetupPending: true,
    });
    expect(fetchMock).toHaveBeenCalledWith("/api/setup/status", expect.anything());
  });

  it("NOT authed -> never calls the auth-gated /api/setup/status", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/auth/status") {
        return jsonResponse(status({ mode: "password", authenticated: false }));
      }
      throw new Error("unexpected fetch: " + url);
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await loadBootState();
    expect(result).toEqual({ kind: "login-password" });
    const calledUrls = fetchMock.mock.calls.map((c) => String(c[0]));
    expect(calledUrls).toEqual(["/api/auth/status"]);
  });

  it("setup/status failure is non-fatal -> land in app, hint off", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/auth/status") {
        return jsonResponse(status({ mode: "none", authenticated: true }));
      }
      if (url === "/api/setup/status") {
        return new Response("boom", { status: 500 });
      }
      throw new Error("unexpected fetch: " + url);
    });
    vi.stubGlobal("fetch", fetchMock);

    const result = await loadBootState();
    expect(result).toEqual({
      kind: "app",
      noneMode: true,
      connectionsSetupPending: false,
    });
  });

  it("auth/status failure -> error branch", async () => {
    const fetchMock = vi.fn(async () => {
      throw new Error("network down");
    });
    vi.stubGlobal("fetch", fetchMock);

    expect(await loadBootState()).toEqual({ kind: "error" });
  });
});
