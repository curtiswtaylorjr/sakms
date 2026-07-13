// The single highest-risk logic in the whole frontend: deciding what belongs
// on screen right now. Ported from the current vanilla-JS frontend's boot()
// (internal/web/static/index.html). The 3-way branch itself is extracted into
// resolveAuthBranch — a pure function with no I/O — so every branch is covered
// by a plain unit test (a regression here is a total, break-glass-only
// lockout). loadBootState wraps it with the two real fetches.

import type { AuthStatusResponse, SetupStatusResponse } from "@dto";
import { api } from "../api/client";

// BootState is the discriminated union of everything the app shell can be.
// Exactly one is ever active, mirroring boot()'s ordered gate:
//   setup          -> instance has no login yet (3-way mode-selection wizard)
//   login-password -> configured, password mode, no session
//   login-oidc     -> configured, oidc mode, no session (SSO notice + break-glass)
//   app            -> authenticated (or mode "none"): land in the app shell
//   error          -> /api/auth/status itself failed; show a retry, never
//                     silently assume authed (divergence from the old code,
//                     which fell through — safe there only because it then
//                     hit an auth-gated /api/setup/status that would 401; our
//                     placeholder makes no such call, so we must be explicit).
export type BootState =
  | { kind: "setup" }
  | { kind: "login-password" }
  | { kind: "login-oidc"; authError: string | null }
  | { kind: "app"; noneMode: boolean; connectionsSetupPending: boolean }
  | { kind: "error" };

// AuthBranch is the result of the first, auth-only gate. "proceed" means the
// operator is through auth (authenticated, or mode "none") and boot should go
// on to consult /api/setup/status; every other variant is a terminal screen.
export type AuthBranch =
  | { kind: "setup" }
  | { kind: "login-password" }
  | { kind: "login-oidc"; authError: string | null }
  | { kind: "proceed"; noneMode: boolean }
  | { kind: "error" };

// resolveAuthBranch is the pure core of the 3-way boot branch. It reads only
// GET /api/auth/status's already-fetched result (+ any OIDC callback auth_error)
// and never performs I/O, so each branch is unit-testable in isolation.
//
// Order and semantics match the backend (internal/api/auth.go authStatusHandler
// + internal/web/static/index.html boot()):
//   - authStatus === null  -> the status call failed; surface an error rather
//     than guessing (see BootState "error").
//   - !configured          -> no login exists yet -> setup wizard.
//   - password + !authed   -> password login form.
//   - oidc + !authed       -> SSO login notice (carries auth_error banner).
//   - everything else      -> proceed. mode "none" is always authenticated
//     server-side; an authenticated password/oidc session lands here too.
export function resolveAuthBranch(
  authStatus: AuthStatusResponse | null,
  authError: string | null,
): AuthBranch {
  if (authStatus === null) return { kind: "error" };
  if (!authStatus.configured) return { kind: "setup" };

  const mode = authStatus.mode;
  if (mode === "password" && !authStatus.authenticated) {
    return { kind: "login-password" };
  }
  if (mode === "oidc" && !authStatus.authenticated) {
    return { kind: "login-oidc", authError };
  }
  // Reachable for mode === "none" (authenticated is always true server-side)
  // or an authenticated password/oidc session.
  return { kind: "proceed", noneMode: mode === "none" };
}

// consumeAuthError reads the ?auth_error=<code> a failed OIDC callback redirects
// back with (a top-level browser navigation can't surface an inline error any
// other way) and strips it from the URL so a manual refresh doesn't re-stick a
// stale failure. Called once at the very start of the boot sequence, before any
// router mounts. No-op outside a browser (e.g. under a non-jsdom test env).
export function consumeAuthError(): string | null {
  if (typeof window === "undefined") return null;
  const authError = new URLSearchParams(window.location.search).get(
    "auth_error",
  );
  if (authError) {
    window.history.replaceState(null, "", window.location.pathname);
  }
  return authError;
}

// loadBootState runs the real boot sequence: read+strip auth_error, fetch
// /api/auth/status (public), branch, and ONLY in the proceed branch fetch
// /api/setup/status (auth-gated — fetching it before auth would 401 and trip
// the global session-expiry fallback, so it must never be parallelized with the
// status call). A setup/status failure is non-fatal: land in the app with the
// connections wizard hint off, exactly as the old boot() did (setupStatus null
// -> app shown).
export async function loadBootState(): Promise<BootState> {
  const authError = consumeAuthError();

  let authStatus: AuthStatusResponse | null = null;
  try {
    authStatus = await api<AuthStatusResponse>("/api/auth/status");
  } catch {
    // fall through: resolveAuthBranch maps null -> error
  }

  const branch = resolveAuthBranch(authStatus, authError);
  switch (branch.kind) {
    case "setup":
      return { kind: "setup" };
    case "login-password":
      return { kind: "login-password" };
    case "login-oidc":
      return { kind: "login-oidc", authError: branch.authError };
    case "error":
      return { kind: "error" };
    case "proceed": {
      let connectionsSetupPending = false;
      try {
        const setupStatus =
          await api<SetupStatusResponse>("/api/setup/status");
        connectionsSetupPending = !setupStatus.dismissed;
      } catch {
        // non-fatal: leave the connections hint off and land in the app
      }
      return {
        kind: "app",
        noneMode: branch.noneMode,
        connectionsSetupPending,
      };
    }
  }
}
