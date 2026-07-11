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
	return auth.New(settings.New(sqlDB)), secretStore
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

func TestSetup_ForwardAndAuthentik_PlaceholderRejected(t *testing.T) {
	for _, mode := range []string{"forward", "authentik"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			authStore, tokenEnc := testAuthStore(t)
			srv := httptest.NewServer(NewAuthMux(authStore, tokenEnc))
			defer srv.Close()

			body, _ := json.Marshal(authCredentialsRequest{Mode: mode})
			resp, err := http.Post(srv.URL+"/api/auth/setup", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400 slice-1 placeholder for mode %q, got %d", mode, resp.StatusCode)
			}
		})
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
