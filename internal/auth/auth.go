// Package auth manages SAK's single local login — one username +
// bcrypt-hashed password gating access to the API (and therefore every
// review workflow) — plus stateless signed session tokens (see session.go)
// so a browser doesn't need to resend credentials on every request.
//
// Single login, not a user table: SAK is a self-hosted, single-operator
// tool (see the design's trust model), so there is exactly one account, the
// same way Settings has exactly one AI provider — no per-user permissions
// to model.
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/curtiswtaylorjr/sakms/internal/settings"
)

const (
	usernameKey     = "auth_username"
	passwordHashKey = "auth_password_hash"
	authModeKey     = "auth_mode"
)

// The four auth strategies a first-run install can pick from and switch
// between later (see GET/PUT /api/auth/mode). ModeForward/ModeAuthentik are
// defined here so the mode-aware Middleware dispatch (session.go) and the
// setup/switch handlers (internal/api) can reference them starting in slice
// 1, even though their actual auth logic doesn't land until slices 2/3.
const (
	ModePassword  = "password"
	ModeForward   = "forward"   // used by slice 2
	ModeAuthentik = "authentik" // used by slice 3
	ModeNone      = "none"
)

// ErrNotConfigured is returned by Verify when no login has been set up yet.
var ErrNotConfigured = errors.New("auth: no login configured yet")

// Store persists the single login's credentials in internal/settings' flat
// KV store — a username and a bcrypt hash are just two more scalar values,
// no schema of their own needed (the hash is already safe to store as
// plaintext-in-DB; that's the entire point of a one-way hash).
type Store struct {
	settings *settings.Store

	// envKeyHash/envKeySuffix hold an externally-supplied API key
	// (SAKMS_API_KEY) for this process's lifetime only — see
	// UseEnvAPIKey in apikey.go for why these are never persisted.
	// envKeyHash is nil unless UseEnvAPIKey has been called.
	envKeyHash   []byte
	envKeySuffix string
}

func New(settingsStore *settings.Store) *Store {
	return &Store{settings: settingsStore}
}

// Configured reports whether a login has been created yet — the API layer
// uses this to refuse a second SetCredentials call (see internal/api's
// setup handler): once an instance has an owner, a later unauthenticated
// visitor must not be able to silently take it over by "setting up" a new
// login of their own.
//
// Defined as auth_mode set OR auth_username set — NOT auth_mode alone. An
// auth_mode-only definition would be a migration/instance-takeover
// regression: every pre-existing install has auth_username set but no
// auth_mode row (that setting didn't exist yet), so it would report
// Configured=false, re-show "Create your login" on next boot, AND make the
// setup handler's already-configured 409 guard stop firing — letting an
// unauthenticated visitor re-POST /api/auth/setup and overwrite the owner's
// credentials. The OR keeps existing password installs correctly
// "configured" (effective mode defaults to "password", see AuthMode) while
// still marking a fresh none/forward/authentik first-run choice as
// configured too, since those write auth_mode without ever writing
// auth_username.
func (s *Store) Configured(ctx context.Context) (bool, error) {
	if _, err := s.settings.Get(ctx, authModeKey); err == nil {
		return true, nil
	} else if !errors.Is(err, settings.ErrNotFound) {
		return false, err
	}
	_, err := s.settings.Get(ctx, usernameKey) // legacy/password path
	if errors.Is(err, settings.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AuthMode returns the effective auth mode: the stored auth_mode value, or
// "password" when unset (settings.ErrNotFound) — every pre-existing install
// has no auth_mode row yet, and "password" is exactly what it was already
// doing, so this default requires no migration. Any OTHER read error is
// propagated as-is (fail-closed per G1): the caller (Middleware) must never
// treat "the store couldn't tell us" as "assume password" or any other
// passing default.
func (s *Store) AuthMode(ctx context.Context) (string, error) {
	v, err := s.settings.Get(ctx, authModeKey)
	if errors.Is(err, settings.ErrNotFound) {
		return ModePassword, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetAuthMode persists the active auth mode. This is a raw write — the
// switch-into preconditions (a password hash must exist before switching to
// "password", forward/authentik must have their config, "none" needs an
// explicit acknowledgement) live in the API handler layer
// (internal/api/authmode.go), not here, mirroring SetCredentials/Verify's
// existing split between storage and validation.
func (s *Store) SetAuthMode(ctx context.Context, mode string) error {
	return s.settings.Set(ctx, authModeKey, mode)
}

// PasswordConfigured reports whether a password hash exists, independent of
// which mode is currently active — used by the mode-switch handler's G4
// precondition for switching INTO "password" (switching away from password
// never clears the hash, so switching back doesn't require re-entering
// credentials).
func (s *Store) PasswordConfigured(ctx context.Context) (bool, error) {
	_, err := s.settings.Get(ctx, passwordHashKey)
	if errors.Is(err, settings.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// SetCredentials creates or replaces the single login.
func (s *Store) SetCredentials(ctx context.Context, username, password string) error {
	if username == "" || password == "" {
		return errors.New("auth: username and password are both required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}
	if err := s.settings.Set(ctx, usernameKey, username); err != nil {
		return fmt.Errorf("saving username: %w", err)
	}
	if err := s.settings.Set(ctx, passwordHashKey, string(hash)); err != nil {
		return fmt.Errorf("saving password hash: %w", err)
	}
	return nil
}

// Verify reports whether username/password match the configured login.
// Username is compared in constant time so a failed login can't leak
// anything about the real username via response timing; the password check
// is bcrypt's own constant-time comparison. Returns ErrNotConfigured (not a
// false negative) when no login exists yet — a caller must be able to tell
// "wrong password" apart from "there's nothing to check against."
func (s *Store) Verify(ctx context.Context, username, password string) (bool, error) {
	wantUsername, err := s.settings.Get(ctx, usernameKey)
	if errors.Is(err, settings.ErrNotFound) {
		return false, ErrNotConfigured
	}
	if err != nil {
		return false, err
	}
	hash, err := s.settings.Get(ctx, passwordHashKey)
	if err != nil {
		return false, err
	}

	usernameMatch := subtle.ConstantTimeCompare([]byte(username), []byte(wantUsername)) == 1
	passwordMatch := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	return usernameMatch && passwordMatch, nil
}
