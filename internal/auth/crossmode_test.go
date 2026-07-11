package auth

import (
	"context"
	"net/http"
	"testing"
)

// crossmode_test.go — slice 5 (Docs + cross-mode hardening). Collects the
// specific AC/edge-case gaps identified during the final coverage audit
// against the plan's §5.4 matrix: tests that no prior slice's suite
// actually exercises, not a re-test of everything the matrix already
// cites as covered elsewhere (TestPutMode_SwitchAwayKeepsConfig already
// proves AC6's password→none→password no-wipe round trip;
// TestMiddleware_RejectsUnauthenticatedRequest/TestMiddleware_
// NoCookieNoKey_401 already prove AC2's "auth_mode unset → gated as
// password" at the full HTTP level against a genuinely fresh store — see
// those files instead of duplicating them here).

// TestMiddleware_EnvAPIKeyUniversal_AcrossModes closes the gap in
// Edge Case #6's reconciliation (§0.6 of the plan): the env-supplied
// SAKMS_API_KEY works in every mode, exactly like a settings-generated
// key. TestMiddleware_APIKeyWorksRegardlessOfMode already proves this for
// a settings-generated key; this test proves it specifically for the env
// key (a distinct code path — UseEnvAPIKey/envKeyHash, not
// EnsureAPIKey/the persisted hash) across two different genuinely active
// modes (none and forward, the latter additionally configured with its
// own — deliberately WRONG-when-presented — secret, so a pass here can
// only be explained by the universal key check, never by the mode's own
// credential).
func TestMiddleware_EnvAPIKeyUniversal_AcrossModes(t *testing.T) {
	enc := testEncryptor(t)
	store := newTestStore(t)
	ctx := context.Background()

	store.UseEnvAPIKey("env-supplied-key-value")

	// Configure forward mode for real (so it's a genuinely active,
	// correctly-configured mode, not merely "active but unconfigured" —
	// see TestMiddleware_ForwardMode_NotConfigured_401 for that separate
	// case).
	if _, err := store.GenerateForwardSecret(ctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, mode := range []string{ModeNone, ModeForward} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			if err := store.SetAuthMode(ctx, mode); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			srv, called := middlewareTestServer(t, enc, store)
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
			req.Header.Set("X-Api-Key", "env-supplied-key-value")
			// Deliberately do NOT present a valid forward secret even in
			// forward mode — a pass must come from the universal env key,
			// not from also satisfying the mode's own credential.
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected the env-supplied X-Api-Key to pass in mode %q, got %d", mode, resp.StatusCode)
			}
			if !*called {
				t.Error("expected the inner handler to run for a valid env-supplied key")
			}
		})
	}
}

// TestMiddleware_ForwardMode_NotConfigured_401 covers Edge Case #2: the
// switch-into precondition (G4, ForwardConfigured) is supposed to prevent
// "forward" from ever becoming the active mode without a secret
// configured, but the gate itself must independently fail closed if that
// invariant is ever bypassed (e.g. auth_mode written directly, a bug
// elsewhere, manual DB surgery) rather than relying solely on the
// precondition holding. No prior slice's suite sets ModeForward via
// SetAuthMode without first calling GenerateForwardSecret.
func TestMiddleware_ForwardMode_NotConfigured_401(t *testing.T) {
	enc := testEncryptor(t)
	store := newTestStore(t)
	ctx := context.Background()

	// Deliberately skip GenerateForwardSecret — simulates the precondition
	// having been bypassed.
	if err := store.SetAuthMode(ctx, ModeForward); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv, called := middlewareTestServer(t, enc, store)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("X-Proxy-Secret", "anything-at-all")
	req.Header.Set("Remote-User", "wade")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 (fail closed) for forward mode with no secret configured, got %d", resp.StatusCode)
	}
	if *called {
		t.Error("inner handler must not run when forward mode has no secret configured")
	}
}

// TestMiddleware_AuthentikMode_NotConfigured_401 is AuthentikMode's
// counterpart to TestMiddleware_ForwardMode_NotConfigured_401 — Edge
// Case #2 for the authentikClient nil-but-no-error ("active mode, no
// config yet") return shape (session.go's authentikClient doc comment).
// No prior slice's suite sets ModeAuthentik via SetAuthMode without also
// calling SetAuthentikConfig first.
func TestMiddleware_AuthentikMode_NotConfigured_401(t *testing.T) {
	enc := testEncryptor(t)
	store := newTestStore(t)
	ctx := context.Background()

	// Deliberately skip SetAuthentikConfig — simulates the precondition
	// having been bypassed.
	if err := store.SetAuthMode(ctx, ModeAuthentik); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv, called := middlewareTestServer(t, enc, store)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer any-token-at-all")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 (fail closed) for authentik mode with no config configured, got %d", resp.StatusCode)
	}
	if *called {
		t.Error("inner handler must not run when authentik mode has no config configured")
	}
}
