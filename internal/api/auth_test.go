package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/curtiswtaylorjr/sakms/internal/auth"
	"github.com/curtiswtaylorjr/sakms/internal/db"
	"github.com/curtiswtaylorjr/sakms/internal/secrets"
	"github.com/curtiswtaylorjr/sakms/internal/settings"
)

func testAuthStore(t *testing.T) (*auth.Store, *secrets.Store) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "sakms.db"))
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	secretStore, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// secretStore doubles as authStore's Authentik-client-secret decryptor,
	// mirroring cmd/sakms/main.go's production wiring (the same secretStore
	// instance is passed to both auth.New and api.NewAuthMux/NewAuthentikMux).
	return auth.New(settings.New(sqlDB), secretStore, http.DefaultClient), secretStore
}

func TestAuthSetup_CreatesLoginAndLogsIn(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "correct-horse-battery-staple"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("expected a session cookie to be set after setup")
	}
}

func TestAuthSetup_RejectsSecondCall(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "first-password"})
	if _, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	takeoverBody, _ := json.Marshal(authCredentialsRequest{Username: "attacker", Password: "attacker-password"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(takeoverBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 refusing to overwrite an existing login, got %d", resp.StatusCode)
	}
}

func TestAuthLogin_Succeeds(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	setupBody, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "the-password"})
	if _, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(setupBody)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loginBody, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "the-password"})
	resp, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("expected a session cookie to be set after login")
	}
}

func TestAuthLogin_WrongPasswordRejected(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	setupBody, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "the-password"})
	if _, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(setupBody)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loginBody, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "wrong-password"})
	resp, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthLogin_NoLoginConfiguredYetRejected(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "anything"})
	resp, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 when nothing is configured yet, got %d", resp.StatusCode)
	}
}

func TestAuthStatus_ReflectsConfiguredAndAuthenticated(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var status authStatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if status.Configured || status.Authenticated {
		t.Fatalf("expected a fresh instance to report neither configured nor authenticated, got %+v", status)
	}

	setupBody, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "the-password"})
	setupResp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(setupBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cookies := setupResp.Cookies()
	setupResp.Body.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/status", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp2.Body.Close()
	var status2 authStatusResponse
	json.NewDecoder(resp2.Body).Decode(&status2)
	if !status2.Configured || !status2.Authenticated {
		t.Fatalf("expected configured+authenticated after setup with the cookie attached, got %+v", status2)
	}
}

// --- Mode-aware setup/status/login (slice 1) ---

func TestSetup_PasswordWritesMode(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "password", Username: "wade", Password: "correct-horse-battery-staple"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	mode, err := authStore.AuthMode(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != auth.ModePassword {
		t.Errorf("expected auth_mode to be written as %q, got %q", auth.ModePassword, mode)
	}
}

func TestSetup_NoneRequiresAck_400(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "none"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without acknowledgeInsecure, got %d", resp.StatusCode)
	}

	configured, err := authStore.Configured(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configured {
		t.Error("expected a rejected none-mode setup to leave the instance unconfigured")
	}
}

func TestSetup_None_NoCookieNoCreds(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "none", AcknowledgeInsecure: true})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("expected no session cookie to be issued for none mode, got %+v", resp.Cookies())
	}

	mode, err := authStore.AuthMode(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != auth.ModeNone {
		t.Errorf("expected auth_mode %q, got %q", auth.ModeNone, mode)
	}
	configured, err := authStore.PasswordConfigured(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configured {
		t.Error("expected none-mode setup to write no password credentials")
	}
}

func TestSetup_AuthentikPlaceholderRejected(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "authentik"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 slice-1 placeholder for authentik mode, got %d", resp.StatusCode)
	}
}

// TestSetup_ForwardGeneratesSecretAndWritesMode is the first-run bootstrap
// fix's end-to-end proof (plan §0.7/§2.2b): POST /api/auth/setup with
// mode:"forward" and NO prior credential must succeed through the PUBLIC
// setup endpoint, generate a shared secret server-side, persist it, write
// auth_mode atomically, and reveal the generated secret once in the
// response body — all in one request, with no protected round-trip needed.
func TestSetup_ForwardGeneratesSecretAndWritesMode(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	configuredBefore, err := authStore.Configured(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if configuredBefore {
		t.Fatal("expected a fresh instance to be unconfigured before setup")
	}

	body, _ := json.Marshal(authCredentialsRequest{Mode: "forward"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with a generated secret in the body, got %d", resp.StatusCode)
	}
	var setupResp authSetupResponse
	if err := json.NewDecoder(resp.Body).Decode(&setupResp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if setupResp.ForwardSecret == "" {
		t.Fatal("expected a generated forward secret in the setup response")
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("expected no session cookie for forward mode, got %+v", resp.Cookies())
	}

	mode, err := authStore.AuthMode(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != auth.ModeForward {
		t.Errorf("expected auth_mode to be written as %q, got %q", auth.ModeForward, mode)
	}
	configuredAfter, err := authStore.Configured(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !configuredAfter {
		t.Fatal("expected the instance to report Configured=true after forward-mode setup")
	}

	ok, err := authStore.VerifyForwardSecret(context.Background(), setupResp.ForwardSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected the secret returned in the setup response to verify against what was persisted")
	}
}

// TestSetup_ForwardAcceptsProvidedSecret covers the "operator supplies
// their own secret" branch of the same first-run bootstrap path.
func TestSetup_ForwardAcceptsProvidedSecret(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "forward", ForwardSecret: "operator-supplied-secret-value"})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var setupResp authSetupResponse
	if err := json.NewDecoder(resp.Body).Decode(&setupResp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if setupResp.ForwardSecret != "operator-supplied-secret-value" {
		t.Errorf("expected the provided secret to be echoed back, got %q", setupResp.ForwardSecret)
	}

	ok, err := authStore.VerifyForwardSecret(context.Background(), "operator-supplied-secret-value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected the operator-provided secret to verify")
	}
}

func TestStatus_ReturnsMode(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var status authStatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()
	if status.Mode != auth.ModePassword {
		t.Errorf("expected an unconfigured instance to report the default mode %q, got %q", auth.ModePassword, status.Mode)
	}

	body, _ := json.Marshal(authCredentialsRequest{Mode: "none", AcknowledgeInsecure: true})
	setupResp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	setupResp.Body.Close()

	resp2, err := http.Get(srv.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var status2 authStatusResponse
	json.NewDecoder(resp2.Body).Decode(&status2)
	resp2.Body.Close()
	if status2.Mode != auth.ModeNone {
		t.Errorf("expected mode %q after switching to none, got %q", auth.ModeNone, status2.Mode)
	}
	if !status2.Authenticated {
		t.Error("expected authenticated:true in none mode")
	}
}

func TestLogin_RejectedInNonPasswordMode(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "none", AcknowledgeInsecure: true})
	setupResp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	setupResp.Body.Close()

	loginBody, _ := json.Marshal(authCredentialsRequest{Username: "wade", Password: "anything"})
	resp, err := http.Post(srv.URL+"/api/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 rejecting login in a non-password mode, got %d", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Errorf("expected no cookie to be minted, got %+v", resp.Cookies())
	}
}

func TestAuthLogout_ClearsCookie(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/auth/logout", "application/json", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 even with no prior session, got %d", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 || resp.Cookies()[0].MaxAge >= 0 {
		t.Fatalf("expected a cookie-clearing response, got %+v", resp.Cookies())
	}
}

// TestSetup_NoneMode_SecondCallRejected_409 closes an AC8 gap found during
// slice 5's final coverage audit: TestConfigured_TrueAfterModeSetOnly
// (internal/auth) proves Configured() flips true at the store level once
// auth_mode is set with no password, and TestAuthSetup_RejectsSecondCall
// proves the setup gate doesn't reappear for the PASSWORD path — but
// nothing exercised the full HTTP round trip for a non-password first-run
// mode: does a REAL second POST to /api/auth/setup actually 409 after a
// none-mode setup, i.e. does authSetupHandler's already-configured guard
// really fire off Configured()'s OR-based redefinition for a mode that
// never wrote auth_username? This is the concrete, end-to-end version of
// AC8 ("Configured() returns true after a non-password mode is chosen at
// first run — the setup gate does not reappear").
func TestSetup_NoneMode_SecondCallRejected_409(t *testing.T) {
	authStore, tokenEnc := testAuthStore(t)
	srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
	defer srv.Close()

	body, _ := json.Marshal(authCredentialsRequest{Mode: "none", AcknowledgeInsecure: true})
	resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for the first none-mode setup call, got %d", resp.StatusCode)
	}

	takeoverBody, _ := json.Marshal(authCredentialsRequest{Username: "attacker", Password: "attacker-password"})
	resp2, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(takeoverBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 refusing a second setup call after a none-mode first run, got %d", resp2.StatusCode)
	}

	// The rejected second call must not have altered the active mode.
	statusResp, err := http.Get(srv.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer statusResp.Body.Close()
	var status authStatusResponse
	json.NewDecoder(statusResp.Body).Decode(&status)
	if !status.Configured {
		t.Error("expected the instance to still report configured:true")
	}
	if status.Mode != auth.ModeNone {
		t.Errorf("expected mode to remain %q after the rejected takeover attempt, got %q", auth.ModeNone, status.Mode)
	}
}
