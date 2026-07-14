// Connections — the safety-critical Settings section. Its save path goes through
// buildConnectionUpsertBody (src/api/settings.ts), which OMITS the apiKey
// property when the operator didn't touch the key field of an already-configured
// connection — so an unrelated edit (e.g. changing the URL) never wipes the
// stored secret. See settings.test.tsx's dedicated assertion. LAN-discovery
// (netscan) hints live here too. Extracted from the original single-file
// Settings.tsx.

import {
  type Component,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import {
  CONNECTION_SERVICES,
  SERVICES_WITH_USERNAME,
  buildConnectionUpsertBody,
  deleteConnection,
  fetchConnections,
  fetchNetscanKnown,
  fetchProwlarrKey,
  probeNetscanHost,
  testConnection,
  upsertConnection,
} from "../../api/settings";
import type { ConnectionSummary, NetscanFinding } from "../../api/settings";
import { Button, ErrorText, inputClass } from "../../components/ui";
import { Card, SaveStatus, useSaveStatus } from "./shared";

// ConnectionRow is one service's controls: URL / Username (if needed) / key or
// password, plus Test / Save / Delete and, when a netscan finding exists, the
// LAN-discovery hint buttons. keyTouched tracks whether the operator (or the
// Fetch-key button) actually edited the key field — the input for a configured
// connection is blank (the real key is never sent back), so an untouched blank
// key must NOT be persisted as "".
const ConnectionRow: Component<{
  service: string;
  existing: ConnectionSummary | undefined;
  finding: NetscanFinding | undefined;
  onChanged: () => void;
}> = (props) => {
  const needsUsername = SERVICES_WITH_USERNAME.includes(props.service);
  const allowHostProbe = props.service === "jellyfin";
  const [url, setUrl] = createSignal(props.existing?.url ?? "");
  const [username, setUsername] = createSignal(props.existing?.username ?? "");
  const [key, setKey] = createSignal("");
  const [keyTouched, setKeyTouched] = createSignal(false);
  const status = useSaveStatus();
  const [hint, setHint] = createSignal("");

  const hasExistingKey = () => !!props.existing?.hasApiKey;
  const keyPlaceholder = () =>
    hasExistingKey()
      ? `unchanged (••••${props.existing?.keySuffix ?? ""})`
      : needsUsername
        ? "password"
        : "api key (if needed)";

  const body = () =>
    buildConnectionUpsertBody({
      url: url(),
      username: username(),
      needsUsername,
      keyTouched: keyTouched(),
      keyValue: key(),
      hasExistingKey: hasExistingKey(),
    });

  const test = async () => {
    status.set("testing…");
    try {
      const b = body();
      const r = await testConnection({
        service: props.service,
        url: b.url,
        username: b.username,
        apiKey: b.apiKey,
      });
      setStatusFromTest(r.ok, r.error);
    } catch (e) {
      status.failed(e);
    }
  };
  const setStatusFromTest = (ok: boolean, err?: string) => {
    if (ok) status.set("✓ ok");
    else status.failed(new Error(err || "connection failed"));
  };

  const save = async () => {
    try {
      await upsertBody();
      status.set("✓ saved");
      props.onChanged();
    } catch (e) {
      status.failed(e);
    }
  };
  // upsertBody is split out so the URL-required guard mirrors the backend
  // (url is required) with a clear inline message rather than a 400 round-trip.
  const upsertBody = async () => {
    if (!url().trim()) throw new Error("url is required");
    await upsertConnection(props.service, body());
  };

  const remove = async () => {
    if (!confirm(`Remove the ${props.service} connection?`)) return;
    try {
      await deleteConnection(props.service);
      props.onChanged();
    } catch (e) {
      status.failed(e);
    }
  };

  const useURL = (u: string) => {
    setUrl(u);
    setHint("URL pre-filled — verify it's really yours, then Save.");
  };
  const fetchKey = async (u: string) => {
    setHint("fetching key…");
    try {
      const k = await fetchProwlarrKey(u);
      setKey(k);
      setKeyTouched(true); // survive the three-state gate (no DOM event to lean on)
      setHint(`API key retrieved from ${u} — verify before saving.`);
    } catch (e) {
      status.failed(e);
    }
  };

  // host-probe (Jellyfin lives off SAK's docker network) — fills the row's URL
  // from a discovered finding, same as a known-host finding does.
  const [probeHost, setProbeHost] = createSignal("");
  const [probed, setProbed] = createSignal<NetscanFinding | undefined>();
  const doProbe = async () => {
    setHint("probing…");
    setProbed(undefined);
    try {
      const findings = await probeNetscanHost(probeHost());
      const match = findings.find((f) => f.service === props.service);
      if (match) {
        setProbed(match);
        setHint("");
      } else if (findings.length) {
        setHint(
          `Found other services there (${findings
            .map((f) => f.service)
            .join(", ")}) but no ${props.service}.`,
        );
      } else {
        setHint(`No ${props.service} found at that host.`);
      }
    } catch (e) {
      status.failed(e);
    }
  };

  return (
    <tr class="border-b border-border/60 align-top">
      <td class="px-2 py-2 text-fg">{props.service}</td>
      <td class="px-2 py-2">
        <input
          type="text"
          class={`${inputClass} !w-52`}
          placeholder="https://..."
          aria-label={`${props.service} URL`}
          value={url()}
          onInput={(e) => setUrl(e.currentTarget.value)}
        />
        <Show when={props.finding || allowHostProbe}>
          <div class="mt-1 rounded border border-dashed border-border p-2 text-xs text-muted">
            <Show when={props.finding}>
              <div>
                Possible {props.service} at {props.finding!.url} — a hint only,
                verify it's yours.
              </div>
              <div class="mt-1 flex gap-2">
                <Button
                  class="!px-2 !py-1 !text-xs"
                  onClick={() => useURL(props.finding!.url)}
                >
                  Use this URL
                </Button>
                <Show when={props.service === "prowlarr"}>
                  <Button
                    class="!px-2 !py-1 !text-xs"
                    onClick={() => void fetchKey(props.finding!.url)}
                  >
                    Fetch API key
                  </Button>
                </Show>
              </div>
            </Show>
            <Show when={allowHostProbe}>
              <div class="mt-1">
                On a different host? Probe a specific LAN IP:
                <div class="mt-1 flex gap-2">
                  <input
                    type="text"
                    class={`${inputClass} !w-40 !py-1 !text-xs`}
                    placeholder="e.g. 10.1.10.4"
                    aria-label={`Probe host for ${props.service}`}
                    value={probeHost()}
                    onInput={(e) => setProbeHost(e.currentTarget.value)}
                  />
                  <Button
                    class="!px-2 !py-1 !text-xs"
                    onClick={() => void doProbe()}
                  >
                    Probe
                  </Button>
                </div>
                <Show when={probed()}>
                  <div class="mt-1 flex items-center gap-2">
                    <span>Found at {probed()!.url}</span>
                    <Button
                      class="!px-2 !py-1 !text-xs"
                      onClick={() => useURL(probed()!.url)}
                    >
                      Use this URL
                    </Button>
                  </div>
                </Show>
              </div>
            </Show>
            <Show when={hint()}>
              <div class="mt-1">{hint()}</div>
            </Show>
          </div>
        </Show>
      </td>
      <td class="px-2 py-2">
        <Show when={needsUsername}>
          <input
            type="text"
            class={`${inputClass} !w-32`}
            placeholder="username"
            aria-label={`${props.service} username`}
            value={username()}
            onInput={(e) => setUsername(e.currentTarget.value)}
          />
        </Show>
      </td>
      <td class="px-2 py-2">
        <input
          type="password"
          class={`${inputClass} !w-52`}
          placeholder={keyPlaceholder()}
          aria-label={`${props.service} API key`}
          value={key()}
          onInput={(e) => {
            setKey(e.currentTarget.value);
            setKeyTouched(true);
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void save();
            }
          }}
        />
      </td>
      <td class="px-2 py-2">
        <div class="flex gap-1">
          <Button class="!px-2 !py-1 !text-xs" onClick={() => void test()}>
            Test
          </Button>
          <Button
            variant="primary"
            class="!px-2 !py-1 !text-xs"
            onClick={() => void save()}
          >
            Save
          </Button>
          <Button
            class="!px-2 !py-1 !text-xs"
            disabled={!props.existing}
            onClick={() => void remove()}
          >
            Delete
          </Button>
        </div>
      </td>
      <td class="px-2 py-2">
        <SaveStatus text={status.status().text} error={status.status().error} />
      </td>
    </tr>
  );
};

export const ConnectionsSection: Component = () => {
  const [conns, { refetch }] = createResource(fetchConnections);
  const [findings] = createResource(fetchNetscanKnown);
  const byService = () => {
    const m: Record<string, ConnectionSummary> = {};
    for (const c of conns() ?? []) m[c.service] = c;
    return m;
  };
  const findingByService = () => {
    const m: Record<string, NetscanFinding> = {};
    for (const f of findings() ?? []) m[f.service] = f;
    return m;
  };

  return (
    <Card title="Connections">
      <Show when={conns.error}>
        <ErrorText>{(conns.error as Error)?.message}</ErrorText>
      </Show>
      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm">
          <thead>
            <tr class="border-b border-border text-xs uppercase tracking-wide text-muted">
              <th class="px-2 py-2 font-medium">Service</th>
              <th class="px-2 py-2 font-medium">URL</th>
              <th class="px-2 py-2 font-medium">Username</th>
              <th class="px-2 py-2 font-medium">API Key / Password</th>
              <th class="px-2 py-2 font-medium" />
              <th class="px-2 py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {/* Rows must mount only AFTER the connections resource resolves —
                each ConnectionRow seeds its local signals (URL, hasExistingKey)
                from props.existing at mount. Mounting while conns() is still
                undefined would seed hasExistingKey=false and a blank URL, so an
                untouched save would send apiKey="" and WIPE the stored secret
                (the exact Guardrail #5 bug). */}
            <Show when={conns() !== undefined}>
              <For each={CONNECTION_SERVICES}>
                {(service) => (
                  <ConnectionRow
                    service={service}
                    existing={byService()[service]}
                    finding={findingByService()[service]}
                    onChanged={() => void refetch()}
                  />
                )}
              </For>
            </Show>
          </tbody>
        </table>
      </div>
    </Card>
  );
};
