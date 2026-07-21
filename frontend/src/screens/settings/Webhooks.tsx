// Webhooks Settings section — list, create, edit, delete, and test-fire
// outbound webhook subscriptions. Each subscription has a URL, an optional
// HMAC-SHA256 signing secret (encrypted at rest, never returned by the API),
// an event filter (which of the four SAK events trigger delivery), and an
// enabled toggle. Delivery is fire-and-forget; failures are logged server-side.
//
// No SectionSave batching — each webhook row saves inline (same as
// SliderAdmin's per-row mutation pattern). The "Test" button fires a one-shot
// delivery to verify the endpoint is reachable.

import {
  type Component,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import {
  ALL_WEBHOOK_EVENTS,
  WEBHOOK_EVENT_LABELS,
  browserNotificationsEnabled,
  createWebhook,
  deleteWebhook,
  fetchWebhooks,
  putBrowserNotificationsEnabled,
  setBrowserNotificationsEnabled,
  testWebhook,
  updateWebhook,
  type WebhookSummary,
} from "../../api/webhooks";
import {
  Button,
  Card,
  ErrorText,
  Muted,
  inputClass,
  labelClass,
  useSaveStatus,
} from "../../components/ui";

// EventCheckboxes renders the four event checkboxes, sharing the selection
// signal between the create form and the per-row edit form.
const EventCheckboxes: Component<{
  selected: () => string[];
  onToggle: (event: string) => void;
}> = (props) => (
  <div class="flex flex-wrap gap-3">
    <For each={ALL_WEBHOOK_EVENTS}>
      {(ev) => (
        <label class="flex cursor-pointer items-center gap-1.5 text-sm text-fg">
          <input
            type="checkbox"
            checked={props.selected().includes(ev)}
            onChange={() => props.onToggle(ev)}
            class="accent-accent"
          />
          {WEBHOOK_EVENT_LABELS[ev]}
        </label>
      )}
    </For>
  </div>
);

// WebhookRow renders one existing subscription with inline edit.
const WebhookRow: Component<{
  hook: WebhookSummary;
  onMutated: () => void;
}> = (props) => {
  const [editing, setEditing] = createSignal(false);
  const [url, setUrl] = createSignal(props.hook.url);
  // Secret follows three-state semantics: undefined = preserve, "" = clear,
  // non-empty = update. We store it as string | undefined.
  const [secret, setSecret] = createSignal<string | undefined>(undefined);
  const [secretPlaceholder, setSecretPlaceholder] = createSignal(
    props.hook.secretSet ? "••••••••" : "",
  );
  const [events, setEvents] = createSignal<string[]>(props.hook.events);
  const [enabled, setEnabled] = createSignal(props.hook.enabled);
  const save = useSaveStatus();
  const del = useSaveStatus();
  const test = useSaveStatus();

  function toggleEvent(ev: string) {
    setEvents((prev) =>
      prev.includes(ev) ? prev.filter((e) => e !== ev) : [...prev, ev],
    );
  }

  async function handleSave() {
    save.set("saving…");
    try {
      // Three-state secret: undefined = preserve, "" = clear, non-empty = update.
      await updateWebhook(props.hook.id, {
        url: url(),
        secret: secret(),
        events: events(),
        enabled: enabled(),
      });
      save.saved();
      setEditing(false);
      props.onMutated();
    } catch (e) {
      save.failed(e);
    }
  }

  async function handleDelete() {
    del.set("deleting…");
    try {
      await deleteWebhook(props.hook.id);
      props.onMutated();
    } catch (e) {
      del.failed(e);
    }
  }

  async function handleTest() {
    test.set("sending…");
    try {
      await testWebhook(props.hook.id);
      test.set("sent");
    } catch (e) {
      test.failed(e);
    }
  }

  return (
    <div class="border-b border-border py-3 last:border-b-0">
      <div class="flex items-center justify-between gap-2">
        <div class="min-w-0 flex-1">
          <div class="truncate text-sm font-medium text-fg">{props.hook.url}</div>
          <div class="mt-0.5 flex flex-wrap gap-1.5">
            <For each={props.hook.events}>
              {(ev) => (
                <span class="rounded bg-surface-2 px-1.5 py-0.5 text-xs text-muted">
                  {WEBHOOK_EVENT_LABELS[ev as keyof typeof WEBHOOK_EVENT_LABELS] ?? ev}
                </span>
              )}
            </For>
            <Show when={props.hook.events.length === 0}>
              <Muted>no events</Muted>
            </Show>
          </div>
          <div class="mt-0.5 flex gap-2 text-xs text-muted">
            <span>{props.hook.enabled ? "enabled" : "disabled"}</span>
            <Show when={props.hook.secretSet}>
              <span>· secret set</span>
            </Show>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <Button variant="secondary" onClick={handleTest}>
            Test
          </Button>
          <Button
            variant="secondary"
            onClick={() => {
              setEditing((e) => !e);
              save.set("");
            }}
          >
            {editing() ? "Cancel" : "Edit"}
          </Button>
          <Button variant="secondary" onClick={() => void handleDelete()}>
            Delete
          </Button>
        </div>
      </div>

      <Show when={test.status().text}>
        <div class={`mt-1 text-xs ${test.status().error ? "text-danger" : "text-muted"}`}>
          {test.status().text}
        </div>
      </Show>
      <Show when={del.status().error}>
        <ErrorText>{del.status().text}</ErrorText>
      </Show>

      <Show when={editing()}>
        <div class="mt-3 space-y-3 rounded border border-border bg-surface-2 p-3">
          <div>
            <label class={labelClass}>URL</label>
            <input
              class={inputClass}
              type="url"
              value={url()}
              onInput={(e) => setUrl(e.currentTarget.value)}
              placeholder="https://example.com/hook"
            />
          </div>

          <div>
            <label class={labelClass}>
              Signing secret{" "}
              <span class="font-normal text-muted">
                ({props.hook.secretSet ? "stored — leave blank to keep" : "optional"})
              </span>
            </label>
            <div class="flex gap-2">
              <input
                class={inputClass}
                type="password"
                value={secret() ?? ""}
                placeholder={secretPlaceholder()}
                onFocus={() => {
                  setSecretPlaceholder("");
                  if (secret() === undefined) setSecret("");
                }}
                onInput={(e) => setSecret(e.currentTarget.value)}
              />
              <Show when={props.hook.secretSet && secret() === undefined}>
                <Button
                  variant="secondary"
                  onClick={() => {
                    setSecret("");
                    setSecretPlaceholder("");
                  }}
                >
                  Clear
                </Button>
              </Show>
            </div>
          </div>

          <div>
            <label class={labelClass}>Events</label>
            <EventCheckboxes selected={events} onToggle={toggleEvent} />
          </div>

          <div>
            <label class="flex cursor-pointer items-center gap-2 text-sm text-fg">
              <input
                type="checkbox"
                checked={enabled()}
                onChange={(e) => setEnabled(e.currentTarget.checked)}
                class="accent-accent"
              />
              Enabled
            </label>
          </div>

          <div class="flex items-center gap-2">
            <Button variant="primary" onClick={() => void handleSave()}>
              Save
            </Button>
            <Show when={save.status().text}>
              <span
                class={`text-sm ${save.status().error ? "text-danger" : "text-muted"}`}
              >
                {save.status().text}
              </span>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
};

// AddWebhookForm renders the create-new-subscription form.
const AddWebhookForm: Component<{ onCreated: () => void }> = (props) => {
  const [open, setOpen] = createSignal(false);
  const [url, setUrl] = createSignal("");
  const [secret, setSecret] = createSignal("");
  const [events, setEvents] = createSignal<string[]>([...ALL_WEBHOOK_EVENTS]);
  const [enabled, setEnabled] = createSignal(true);
  const save = useSaveStatus();

  function toggleEvent(ev: string) {
    setEvents((prev) =>
      prev.includes(ev) ? prev.filter((e) => e !== ev) : [...prev, ev],
    );
  }

  async function handleCreate() {
    save.set("saving…");
    try {
      await createWebhook({
        url: url(),
        secret: secret(),
        events: events(),
        enabled: enabled(),
      });
      setUrl("");
      setSecret("");
      setEvents([...ALL_WEBHOOK_EVENTS]);
      setEnabled(true);
      save.set("");
      setOpen(false);
      props.onCreated();
    } catch (e) {
      save.failed(e);
    }
  }

  return (
    <div class="mt-3">
      <Show
        when={open()}
        fallback={
          <Button variant="primary" onClick={() => setOpen(true)}>
            Add webhook
          </Button>
        }
      >
        <div class="rounded border border-border bg-surface-2 p-3 space-y-3">
          <div>
            <label class={labelClass}>URL</label>
            <input
              class={inputClass}
              type="url"
              value={url()}
              onInput={(e) => setUrl(e.currentTarget.value)}
              placeholder="https://example.com/hook"
            />
          </div>

          <div>
            <label class={labelClass}>Signing secret <span class="font-normal text-muted">(optional)</span></label>
            <input
              class={inputClass}
              type="password"
              value={secret()}
              onInput={(e) => setSecret(e.currentTarget.value)}
              placeholder="leave blank for no signing"
            />
          </div>

          <div>
            <label class={labelClass}>Events</label>
            <EventCheckboxes selected={events} onToggle={toggleEvent} />
          </div>

          <div>
            <label class="flex cursor-pointer items-center gap-2 text-sm text-fg">
              <input
                type="checkbox"
                checked={enabled()}
                onChange={(e) => setEnabled(e.currentTarget.checked)}
                class="accent-accent"
              />
              Enabled
            </label>
          </div>

          <div class="flex items-center gap-2">
            <Button
              variant="primary"
              disabled={!url()}
              onClick={() => void handleCreate()}
            >
              Create
            </Button>
            <Button variant="secondary" onClick={() => { setOpen(false); save.set(""); }}>
              Cancel
            </Button>
            <Show when={save.status().text}>
              <span
                class={`text-sm ${save.status().error ? "text-danger" : "text-muted"}`}
              >
                {save.status().text}
              </span>
            </Show>
          </div>
        </div>
      </Show>
    </div>
  );
};

// BrowserNotificationsToggle is the opt-in "Enable browser notifications"
// switch. It reads/writes the SHARED browserNotificationsEnabled signal (from
// api/webhooks) — the same instance the shell-mounted BrowserNotifications
// component reacts to — so a flip here opens/closes that component's EventSource
// without a page reload. The browser's own Notification permission is a SEPARATE
// state from this preference (plan Principle 4): the preference is persisted
// regardless of the permission outcome, but a desktop notification only actually
// appears once permission === "granted".
const BrowserNotificationsToggle: Component = () => {
  const save = useSaveStatus();
  // Local mirror of Notification.permission so the "blocked" message re-renders
  // after requestPermission() resolves — the native property is not reactive.
  const [permission, setPermission] = createSignal<NotificationPermission>(
    typeof Notification !== "undefined" ? Notification.permission : "default",
  );

  async function handleToggle(checked: boolean) {
    save.set("saving…");
    try {
      if (checked) {
        // Turning on: resolve permission FIRST, THEN flip the shared signal.
        // BrowserNotifications' effect tracks the signal but reads
        // Notification.permission untracked (only when the signal transitions),
        // so setting the signal before permission resolves would run the effect
        // with permission still "default" (no stream opens), and re-setting the
        // same value afterward is a Solid no-op → no stream until a reload.
        if (
          typeof Notification !== "undefined" &&
          Notification.permission === "default"
        ) {
          await Notification.requestPermission();
        }
        if (typeof Notification !== "undefined") {
          setPermission(Notification.permission);
        }
        // Persist regardless of the permission outcome (preference and
        // permission are separate states, per plan Principle 4).
        await putBrowserNotificationsEnabled(true);
        setBrowserNotificationsEnabled(true);
      } else {
        await putBrowserNotificationsEnabled(false);
        setBrowserNotificationsEnabled(false);
      }
      save.saved();
    } catch (e) {
      save.failed(e);
    }
  }

  const blocked = () =>
    browserNotificationsEnabled() && permission() === "denied";

  return (
    <div class="mb-4 border-b border-border pb-4">
      <label class="flex cursor-pointer items-center gap-2 text-sm text-fg">
        <input
          type="checkbox"
          checked={browserNotificationsEnabled()}
          onChange={(e) => void handleToggle(e.currentTarget.checked)}
          class="accent-accent"
        />
        Enable browser notifications
      </label>
      <p class="mt-1 text-sm text-muted">
        Pop a desktop notification when SAK finishes a Rename, Purge, or Dedup,
        or completes a grab — as long as at least one SAK tab is open. Works
        alongside any webhooks below.
      </p>
      <Show when={blocked()}>
        <p class="mt-1 text-sm text-danger">
          Blocked — enable notifications for this site in your browser settings.
        </p>
      </Show>
      <Show when={save.status().text}>
        <span
          class={`mt-1 block text-sm ${
            save.status().error ? "text-danger" : "text-muted"
          }`}
        >
          {save.status().text}
        </span>
      </Show>
    </div>
  );
};

// WebhooksSection is the top-level Settings → Webhooks tab content.
export const WebhooksSection: Component = () => {
  const [hooks, { refetch }] = createResource(fetchWebhooks);

  return (
    <Card title="Notifications">
      <BrowserNotificationsToggle />

      <p class="mb-3 text-sm text-muted">
        Get pinged in other apps — Discord, Home Assistant, anything that can
        receive a webhook — whenever SAK finishes a Rename, Purge, or Dedup,
        or completes a grab. If you set a signing secret below, each
        notification includes an{" "}
        <code class="rounded bg-surface-2 px-1 py-0.5 text-xs">
          X-SAK-Signature
        </code>{" "}
        header (HMAC-SHA256) so the receiving app can verify it really came
        from SAK.
      </p>

      <Show when={hooks.error}>
        <ErrorText>Failed to load webhooks: {String(hooks.error)}</ErrorText>
      </Show>

      <Show when={!hooks.loading && hooks()?.length === 0}>
        <Muted>No webhooks configured.</Muted>
      </Show>

      <Show when={(hooks()?.length ?? 0) > 0}>
        <div>
          <For each={hooks()}>
            {(hook) => (
              <WebhookRow hook={hook} onMutated={() => void refetch()} />
            )}
          </For>
        </div>
      </Show>

      <AddWebhookForm onCreated={() => void refetch()} />
    </Card>
  );
};
