// App is the single root: it runs the boot sequence on mount, re-runs it after
// every auth transition (login / setup / logout / mode-switch) and whenever a
// mid-app /api/* request 401s (global session-expiry fallback), and renders
// exactly one of the boot branches. This replaces the toolchain-scaffold
// placeholder; it stays the app's single root component.

import { type Component, createSignal, Match, onCleanup, onMount, Switch } from "solid-js";
import { setOnSessionExpired } from "./api/client";
import { type BootState, loadBootState } from "./auth/boot";
import { AppShell } from "./screens/AppShell";
import { Login } from "./screens/Login";
import { OidcLogin } from "./screens/OidcLogin";
import { SetupWizard } from "./screens/SetupWizard";
import { AuthScreen, Button, Muted } from "./components/ui";

// narrowing helpers: return the state object only when it matches the kind, so
// Solid's <Match> children get a correctly-typed accessor.
type Of<K extends BootState["kind"]> = Extract<BootState, { kind: K }>;
const asKind =
  <K extends BootState["kind"]>(kind: K) =>
  (s: BootState | null): Of<K> | null =>
    s && s.kind === kind ? (s as Of<K>) : null;

const App: Component = () => {
  // null = boot in flight (first load or a re-boot); every settled value is a
  // BootState variant.
  const [state, setState] = createSignal<BootState | null>(null);

  const boot = () => {
    void loadBootState().then(setState);
  };

  onMount(() => {
    // A 401 on any non-/api/auth request re-runs boot, which re-checks
    // /api/auth/status and falls back to the login branch instead of a broken
    // view (session-expiry handling, requirement #6).
    setOnSessionExpired(boot);
    boot();
  });
  onCleanup(() => setOnSessionExpired(null));

  return (
    <Switch fallback={<Loading />}>
      <Match when={asKind("setup")(state())}>
        <SetupWizard onSetupComplete={boot} />
      </Match>
      <Match when={asKind("login-password")(state())}>
        <Login onAuthenticated={boot} />
      </Match>
      <Match when={asKind("login-oidc")(state())}>
        {(s) => (
          <OidcLogin authError={s().authError} onSwitchedToPassword={boot} />
        )}
      </Match>
      <Match when={asKind("app")(state())}>
        {(s) => (
          <AppShell
            noneMode={s().noneMode}
            connectionsSetupPending={s().connectionsSetupPending}
            onLoggedOut={boot}
          />
        )}
      </Match>
      <Match when={asKind("error")(state())}>
        <AuthScreen title="Couldn't reach SAK">
          <Muted class="mb-4">
            The server didn't respond to the initial status check. This is a
            connectivity or server problem — retry once it's reachable.
          </Muted>
          <Button variant="primary" onClick={boot}>
            Retry
          </Button>
        </AuthScreen>
      </Match>
    </Switch>
  );
};

const Loading: Component = () => (
  <div class="flex min-h-screen items-center justify-center">
    <span class="text-sm text-muted">Loading…</span>
  </div>
);

export default App;
