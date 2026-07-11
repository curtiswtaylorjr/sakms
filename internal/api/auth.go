package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/curtiswtaylorjr/sakms/internal/auth"
)

// NewAuthMux returns the handful of routes that must stay reachable without
// a session — setup, login, logout, and status — kept on their OWN mux,
// deliberately separate from NewMux's business-logic routes. cmd/sakms
// wraps NewMux's result in auth.Middleware but mounts this one unwrapped;
// keeping them apart means that middleware never needs an exemption list,
// and NewMux's own large existing test suite never has to know auth exists
// at all.
func NewAuthMux(authStore *auth.Store, tokenEnc auth.TokenEncryptor) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/setup", authSetupHandler(authStore, tokenEnc))
	mux.HandleFunc("POST /api/auth/login", authLoginHandler(authStore, tokenEnc))
	mux.HandleFunc("POST /api/auth/logout", authLogoutHandler())
	mux.HandleFunc("GET /api/auth/status", authStatusHandler(authStore, tokenEnc))
	return mux
}

type authCredentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Mode selects the auth strategy at first run — "" means "password"
	// (today's exact back-compat behavior). "forward"/"authentik" are
	// accepted here too (per the plan's first-run bootstrap fix: their
	// config can't go through a protected endpoint before any credential
	// exists) but return a 400 placeholder until slices 2/3 land.
	Mode string `json:"mode"`
	// AcknowledgeInsecure must be true to select Mode "none" — a genuine
	// no-auth instance requires an explicit, unmissable opt-in (G2).
	AcknowledgeInsecure bool `json:"acknowledgeInsecure"`
}

// authSetupHandler creates SAK's one login — refuses once a login
// already exists (checked fresh on every call, not cached) so a visitor who
// reaches an already-configured instance can't silently take it over by
// "setting up" a login of their own; they need /api/auth/login instead.
func authSetupHandler(authStore *auth.Store, tokenEnc auth.TokenEncryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		configured, err := authStore.Configured(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if configured {
			http.Error(w, "a login is already configured — use /api/auth/login instead", http.StatusConflict)
			return
		}

		var req authCredentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		mode := req.Mode
		if mode == "" {
			mode = auth.ModePassword // back-compat: today's exact default
		}

		switch mode {
		case auth.ModePassword:
			if err := authStore.SetCredentials(ctx, req.Username, req.Password); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := authStore.SetAuthMode(ctx, auth.ModePassword); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			token, err := auth.IssueToken(tokenEnc)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			auth.SetSessionCookie(w, token)
			w.WriteHeader(http.StatusNoContent)
		case auth.ModeNone:
			if !req.AcknowledgeInsecure {
				http.Error(w, "acknowledgeInsecure must be true to select the none auth mode", http.StatusBadRequest)
				return
			}
			// No credentials, no cookie — "none" mode has nothing to
			// authenticate.
			if err := authStore.SetAuthMode(ctx, auth.ModeNone); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case auth.ModeForward, auth.ModeAuthentik:
			// Slice-1 placeholder (plan §0.7/§1.3): slices 2/3 replace this
			// branch with real first-run config handling carried in this
			// same public setup body.
			http.Error(w, "mode not selectable yet", http.StatusBadRequest)
		default:
			http.Error(w, "unknown auth mode", http.StatusBadRequest)
		}
	}
}

func authLoginHandler(authStore *auth.Store, tokenEnc auth.TokenEncryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		mode, err := authStore.AuthMode(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if mode != auth.ModePassword {
			// No cookie concept in forward/authentik/none — minting one
			// here would create exactly the stale-cookie path Edge Case #3
			// forbids.
			http.Error(w, "login is not applicable in the current auth mode", http.StatusBadRequest)
			return
		}

		var req authCredentialsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		ok, err := authStore.Verify(ctx, req.Username, req.Password)
		if err != nil && !errors.Is(err, auth.ErrNotConfigured) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}

		token, err := auth.IssueToken(tokenEnc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		auth.SetSessionCookie(w, token)
		w.WriteHeader(http.StatusNoContent)
	}
}

// authLogoutHandler always succeeds — clearing a cookie that may not exist
// is harmless, and there's no server-side session state to invalidate (see
// session.go's doc comment on why tokens are stateless).
func authLogoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth.ClearSessionCookie(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

type authStatusResponse struct {
	Configured    bool   `json:"configured"`
	Authenticated bool   `json:"authenticated"`
	Mode          string `json:"mode"`
}

// authStatusHandler is the one endpoint the frontend calls before it knows
// anything else about the instance — it decides which of "create your
// login," "log in," or "proceed" to show. Authenticated is computed
// relative to the active mode: "none" is always true (nothing to check),
// "password" is today's cookie check unchanged. forward/authentik aren't
// reachable as the active mode until slices 2/3 (setup's placeholder
// branch above refuses to select them), so their status branches land
// alongside those slices' helpers — the default case below is a safe
// fallback (today's cookie check) until then.
func authStatusHandler(authStore *auth.Store, tokenEnc auth.TokenEncryptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		configured, err := authStore.Configured(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mode, err := authStore.AuthMode(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var authenticated bool
		switch mode {
		case auth.ModeNone:
			authenticated = true
		case auth.ModePassword:
			authenticated = auth.Authenticated(tokenEnc, r)
		default:
			// forward/authentik status branches land in slices 2/3.
			authenticated = auth.Authenticated(tokenEnc, r)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(authStatusResponse{
			Configured:    configured,
			Authenticated: authenticated,
			Mode:          mode,
		})
	}
}
