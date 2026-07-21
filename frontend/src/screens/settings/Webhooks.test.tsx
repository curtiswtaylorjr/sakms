// Browser-notifications toggle tests (plan Step 9). The load-bearing behaviors:
// (1) the native Notification permission is requested ONLY when turning the
// preference on from an unresolved ("default") state; (2) the preference is
// persisted regardless of the permission outcome (preference and permission are
// separate states, per plan Principle 4); (3) an enabled-but-denied preference
// renders a distinct "Blocked" message, never a plain on-state; and (4) flipping
// the toggle updates the SAME shared browserNotificationsEnabled signal instance
// that the shell-mounted BrowserNotifications component reads — a signal-level
// integration check, not just a render.
//
// Permission-stub note: both the toggle's local mirror AND worker-2's shell
// effect read Notification.permission (the property), not requestPermission()'s
// return value — real browsers update the property synchronously when the promise
// resolves, so the stub must mutate MockNotification.permission inside
// requestPermission or the enable-from-default path would be mis-tested.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import {
  browserNotificationsEnabled,
  setBrowserNotificationsEnabled,
} from "../../api/webhooks";
import { WebhooksSection } from "./Webhooks";

// MockNotification stands in for the browser Notification API. It is constructible
// (the shell's emit path calls `new Notification(...)`) and its static
// `permission` is mutated by requestPermission the way a real browser does.
class MockNotification {
  static permission: NotificationPermission = "default";
  static requestPermission = vi.fn(async () => MockNotification.permission);
  constructor(
    public title: string,
    public options?: NotificationOptions,
  ) {}
}

// setPermissionOnRequest makes requestPermission resolve to (and set the property
// to) the given outcome, matching real synchronous property-update behavior.
const setPermissionOnRequest = (outcome: NotificationPermission) => {
  MockNotification.requestPermission.mockImplementation(async () => {
    MockNotification.permission = outcome;
    return outcome;
  });
};

type Call = { url: string; method: string; body: unknown };

const stubFetch = () => {
  const calls: Call[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({
      url,
      method,
      body: init?.body ? JSON.parse(init.body as string) : undefined,
    });
    // WebhooksSection mounts createResource(fetchWebhooks) → GET /api/webhooks.
    if (url.includes("/api/webhooks") && method === "GET")
      return new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    // Every mutation (the PUT preference) defaults to a clean 204.
    return new Response(null, { status: 204 });
  });
  vi.stubGlobal("fetch", fn);
  return calls;
};

beforeEach(() => {
  MockNotification.permission = "default";
  MockNotification.requestPermission = vi.fn(
    async () => MockNotification.permission,
  );
  vi.stubGlobal("Notification", MockNotification);
});

afterEach(() => {
  // The shared signal is module-level state that persists across tests — reset
  // it so test order can't leak an enabled preference into the next test.
  setBrowserNotificationsEnabled(false);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const toggle = () =>
  screen.getByLabelText("Enable browser notifications") as HTMLInputElement;

describe("Browser notifications toggle", () => {
  it("requests permission when turning on from the 'default' state", async () => {
    MockNotification.permission = "default";
    setPermissionOnRequest("granted");
    const calls = stubFetch();
    render(() => <WebhooksSection />);

    fireEvent.click(toggle());

    await waitFor(() =>
      expect(
        calls.some(
          (c) =>
            c.method === "PUT" &&
            c.url.includes("/api/settings/browser-notifications-enabled"),
        ),
      ).toBe(true),
    );
    expect(MockNotification.requestPermission).toHaveBeenCalledTimes(1);
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.body).toEqual({ enabled: true });
    await waitFor(() => expect(browserNotificationsEnabled()).toBe(true));
  });

  it("does NOT request permission when it is already granted", async () => {
    MockNotification.permission = "granted";
    const calls = stubFetch();
    render(() => <WebhooksSection />);

    fireEvent.click(toggle());

    await waitFor(() =>
      expect(calls.some((c) => c.method === "PUT")).toBe(true),
    );
    expect(MockNotification.requestPermission).not.toHaveBeenCalled();
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.body).toEqual({ enabled: true });
    await waitFor(() => expect(browserNotificationsEnabled()).toBe(true));
  });

  it("persists the preference even when permission is denied on enable", async () => {
    MockNotification.permission = "default";
    setPermissionOnRequest("denied");
    const calls = stubFetch();
    render(() => <WebhooksSection />);

    fireEvent.click(toggle());

    await waitFor(() =>
      expect(calls.some((c) => c.method === "PUT")).toBe(true),
    );
    // Preference persisted (enabled=true) regardless of the denied permission.
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.body).toEqual({ enabled: true });
    await waitFor(() => expect(browserNotificationsEnabled()).toBe(true));
    // ...and because permission ended up denied, the blocked message shows.
    expect(
      await screen.findByText(/Blocked — enable notifications for this site/),
    ).toBeInTheDocument();
  });

  it("renders the 'blocked' state distinctly from off when enabled + denied", async () => {
    MockNotification.permission = "denied";
    stubFetch();
    // Preference already enabled (e.g. seeded from the server on another device),
    // but this browser has permanently denied permission.
    setBrowserNotificationsEnabled(true);
    render(() => <WebhooksSection />);

    expect(
      await screen.findByText(/Blocked — enable notifications for this site/),
    ).toBeInTheDocument();
    // The checkbox still reflects the enabled preference (it is not silently off).
    expect(toggle().checked).toBe(true);
  });

  it("does NOT show 'blocked' when the preference is off, even if permission is denied", async () => {
    MockNotification.permission = "denied";
    stubFetch();
    setBrowserNotificationsEnabled(false);
    render(() => <WebhooksSection />);

    await screen.findByLabelText("Enable browser notifications");
    expect(screen.queryByText(/Blocked — enable notifications/)).toBeNull();
    expect(toggle().checked).toBe(false);
  });

  it("turning off persists enabled=false and requests no permission", async () => {
    MockNotification.permission = "granted";
    const calls = stubFetch();
    setBrowserNotificationsEnabled(true);
    render(() => <WebhooksSection />);
    expect(toggle().checked).toBe(true);

    fireEvent.click(toggle());

    await waitFor(() =>
      expect(calls.some((c) => c.method === "PUT")).toBe(true),
    );
    expect(MockNotification.requestPermission).not.toHaveBeenCalled();
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.body).toEqual({ enabled: false });
    await waitFor(() => expect(browserNotificationsEnabled()).toBe(false));
  });

  it("flips the SAME shared signal the shell reads (signal-level integration)", async () => {
    MockNotification.permission = "granted";
    stubFetch();
    expect(browserNotificationsEnabled()).toBe(false);
    render(() => <WebhooksSection />);

    fireEvent.click(toggle());

    // The imported accessor (the exact instance BrowserNotifications subscribes
    // to) reflects the flip — proving one shared signal, not a parallel copy.
    await waitFor(() => expect(browserNotificationsEnabled()).toBe(true));
  });
});
