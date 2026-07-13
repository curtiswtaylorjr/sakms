// Small shared primitives for the auth screens (setup / login / SSO notice).
// Kept intentionally minimal — these are the only views this wave builds; the
// full Seerr re-skin lands in later waves. Styling uses the theme tokens from
// src/index.css (bg-surface, text-fg, text-muted, bg-accent, border-border,
// text-danger) so the palette stays in one place.

import { type JSX, splitProps } from "solid-js";

export const inputClass =
  "w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg " +
  "outline-none focus:border-accent";

export const labelClass = "block text-xs font-medium text-muted";

// AuthScreen centers a single auth panel on the page — the setup wizard, the
// login form, and the SSO notice all share this frame.
export function AuthScreen(props: {
  title: string;
  children: JSX.Element;
}): JSX.Element {
  return (
    <div class="flex min-h-screen items-center justify-center p-6">
      <div class="w-full max-w-md rounded-xl border border-border bg-surface p-6 shadow-lg">
        <h2 class="mb-3 text-lg font-semibold text-fg">{props.title}</h2>
        {props.children}
      </div>
    </div>
  );
}

// Field wraps a labeled control.
export function Field(props: {
  label: string;
  children: JSX.Element;
}): JSX.Element {
  return (
    <label class="mb-3 block">
      <span class={labelClass}>{props.label}</span>
      <div class="mt-1">{props.children}</div>
    </label>
  );
}

type ButtonProps = JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary";
};

// Button defaults to type="button". Every button that is NOT the form's submit
// control must keep that default (or set it explicitly) — a bare <button> in a
// <form> is type="submit", which fires the form's onSubmit. That exact trap
// wiped the break-glass reveal panel in a live incident (see the old frontend's
// note at index.html:2009). Submit buttons pass type="submit" explicitly.
export function Button(props: ButtonProps): JSX.Element {
  const [local, rest] = splitProps(props, ["variant", "class", "type"]);
  const base =
    "rounded-md px-4 py-2 text-sm font-medium transition disabled:opacity-50";
  const variant =
    local.variant === "primary"
      ? "bg-accent text-accent-fg hover:opacity-90"
      : "border border-border bg-surface-2 text-fg hover:opacity-90";
  return (
    <button
      type={local.type ?? "button"}
      class={`${base} ${variant} ${local.class ?? ""}`}
      {...rest}
    />
  );
}

// Muted renders secondary explanatory text.
export function Muted(props: {
  children: JSX.Element;
  class?: string;
}): JSX.Element {
  return (
    <p class={`text-sm text-muted ${props.class ?? ""}`}>{props.children}</p>
  );
}

// ErrorText renders an error line (empty content renders nothing).
export function ErrorText(props: { children: JSX.Element }): JSX.Element {
  return <div class="mt-2 text-sm text-danger">{props.children}</div>;
}
