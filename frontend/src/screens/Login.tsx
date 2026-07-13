// Password login screen — reachable only when the active mode is "password"
// and there is no valid session (the backend 400s /api/auth/login in every
// other mode, so boot() never routes here for oidc/none). Ported from the
// current frontend's renderCredentialsGate(container, "login").
//
// This posts with a raw fetch rather than the api() client on purpose: it needs
// to distinguish a 401 ("Invalid username or password.") from other errors, and
// must NOT trip api()'s global session-expiry fallback (a failed login is not an
// expired session).

import { type Component, createSignal } from "solid-js";
import type { LoginRequest } from "@dto";
import { AuthScreen, Button, ErrorText, Field, Muted, inputClass } from "../components/ui";

export const Login: Component<{ onAuthenticated: () => void }> = (props) => {
  const [username, setUsername] = createSignal("");
  const [password, setPassword] = createSignal("");
  const [error, setError] = createSignal("");

  const submit = async (e: Event) => {
    e.preventDefault();
    setError("");
    if (!username() || !password()) {
      setError("Username and password are both required.");
      return;
    }
    const body: LoginRequest = { username: username(), password: password() };
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        const text = await res.text();
        setError(
          res.status === 401
            ? "Invalid username or password."
            : text || "HTTP " + res.status,
        );
        return;
      }
      props.onAuthenticated();
    } catch (err) {
      setError((err as Error).message);
    }
  };

  return (
    <AuthScreen title="Log in to SAK">
      <Muted class="mb-4">This is the one login that protects this instance.</Muted>
      <form onSubmit={submit}>
        <Field label="Username">
          <input
            type="text"
            autocomplete="username"
            class={inputClass}
            value={username()}
            onInput={(e) => setUsername(e.currentTarget.value)}
          />
        </Field>
        <Field label="Password">
          <input
            type="password"
            autocomplete="current-password"
            class={inputClass}
            value={password()}
            onInput={(e) => setPassword(e.currentTarget.value)}
          />
        </Field>
        <Button type="submit" variant="primary">
          Log in
        </Button>
        {error() && <ErrorText>{error()}</ErrorText>}
      </form>
    </AuthScreen>
  );
};
