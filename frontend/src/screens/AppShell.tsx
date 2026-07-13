// The authed app shell. This wave deliberately ships only a placeholder past
// auth (per the task: "app shell past auth can be a minimal placeholder proving
// you land there correctly") — the real Discover / Settings / workflow views
// are later waves. It exists to prove the boot sequence lands here correctly and
// to stand up the client-side router without it ever claiming an /api/* path.

import { type Component, createSignal, Show } from "solid-js";
import { Route, Router } from "@solidjs/router";
import { Button, ErrorText, Muted } from "../components/ui";

// APP_ROUTES is the exhaustive list of client-side route patterns the router
// serves. Guardrail #2 / requirement #7: the router must NEVER claim any
// /api/* path (the OIDC callback /api/auth/oidc/callback is a real server
// route). A unit test asserts none of these start with "/api".
export const APP_ROUTES = ["/", "/discover"] as const;

const Placeholder: Component<{ view: string }> = (props) => (
  <div class="rounded-xl border border-border bg-surface p-6">
    <h1 class="text-xl font-semibold text-fg">SAK Media Server</h1>
    <Muted class="mt-2">
      You're in. Authenticated app shell (placeholder). Current view:{" "}
      <span class="text-fg">{props.view}</span>. Real views land in later waves.
    </Muted>
  </div>
);

const NotFound: Component = () => (
  <div class="rounded-xl border border-border bg-surface p-6">
    <h1 class="text-xl font-semibold text-fg">Not found</h1>
    <Muted class="mt-2">No such view. This is the SPA catch-all fallback.</Muted>
  </div>
);

export const AppShell: Component<{
  noneMode: boolean;
  connectionsSetupPending: boolean;
  onLoggedOut: () => void;
}> = (props) => {
  const [logoutError, setLogoutError] = createSignal("");

  const logout = async () => {
    setLogoutError("");
    try {
      await fetch("/api/auth/logout", { method: "POST" });
      props.onLoggedOut();
    } catch (err) {
      setLogoutError((err as Error).message);
    }
  };

  return (
    <div class="min-h-screen">
      <header class="flex items-center gap-4 border-b border-border bg-surface px-6 py-3">
        <span class="font-semibold text-fg">SAK Media Server</span>
        <div class="ml-auto">
          <Button onClick={logout}>Log out</Button>
        </div>
      </header>

      <Show when={props.noneMode}>
        <div class="border-b border-border bg-surface-2 px-6 py-2">
          <span class="text-sm text-danger">
            Authentication is disabled for this instance — it and every connected
            service is reachable by anyone who can reach it. Switch to a different
            mode in Settings to fix this.
          </span>
        </div>
      </Show>

      <Show when={props.connectionsSetupPending}>
        <div class="border-b border-border bg-surface-2 px-6 py-2">
          <span class="text-sm text-muted">
            First-run connections setup hasn't been dismissed yet — the setup
            wizard lands in a later wave.
          </span>
        </div>
      </Show>

      <main class="p-6">
        {logoutError() && <ErrorText>{logoutError()}</ErrorText>}
        <Router>
          <Route path="/" component={() => <Placeholder view="home" />} />
          <Route
            path="/discover"
            component={() => <Placeholder view="discover" />}
          />
          <Route path="*" component={NotFound} />
        </Router>
      </main>
    </div>
  );
};
