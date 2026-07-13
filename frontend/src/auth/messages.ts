// AUTH_ERROR_MESSAGES maps the fixed set of reason codes the OIDC callback can
// redirect back with (?auth_error=<code>, see internal/api/oidc.go's
// redirectAuthError) to short human text. Ported verbatim from the current
// frontend. The lookup is deliberately CLOSED with a generic fallback: an
// unrecognized (or attacker-crafted) code renders the generic line rather than
// the raw value. Every message is rendered through Solid's JSX text
// interpolation (auto-escaped), never innerHTML, so a hand-forged ?auth_error=
// link can't inject markup.
export const AUTH_ERROR_MESSAGES: Record<string, string> = {
  idp_error: "Your identity provider rejected or cancelled the login.",
  state_mismatch:
    "The login response didn't match this browser's request. Please try again.",
  missing_code: "The identity provider didn't return an authorization code.",
  flow_expired: "The login took too long and expired. Please try again.",
  no_flow: "No active login was in progress. Please try again.",
  exchange_failed: "The identity provider's token couldn't be verified.",
  not_configured: "Single sign-on isn't fully configured on this instance.",
  internal_error: "Something went wrong completing the login.",
};

// authErrorMessage returns the human text for a callback reason code, falling
// back to a generic line for any unrecognized code.
export function authErrorMessage(code: string): string {
  return AUTH_ERROR_MESSAGES[code] || "Login failed. Please try again.";
}
