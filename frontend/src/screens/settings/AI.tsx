// AI section — provider + model selection, shared by Adult identification and the
// Movies/Series title-guess fallback. Extracted from the original single-file
// Settings.tsx. Owns the connection fields for its providers too: the AI
// providers (ollama/openai/gemini/anthropic) and Brave web-search grounding
// were moved out of the generic Connections table into this tab so a provider's
// URL/API-key fields sit next to the selector that governs which one matters.
// The connection data is fetched here the same way ConnectionsTable does
// (fetchConnections/fetchNetscanKnown resources + byService/findingByService
// lookup maps) and rendered through the shared ConnectionRow / ConnectionMiniTable
// so the safety-critical three-state secret handling is reused verbatim.

import {
  type Component,
  createEffect,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import {
  AI_PROVIDERS,
  fetchAIModel,
  fetchAIProvider,
  fetchConnections,
  fetchNetscanKnown,
  putAIModel,
  putAIProvider,
} from "../../api/settings";
import type { ConnectionSummary, NetscanFinding } from "../../api/settings";
import { Button, Muted, inputClass, labelClass } from "../../components/ui";
import { ConnectionMiniTable, ConnectionRow } from "./Connections";
import { Card, SaveStatus, useSaveStatus } from "./shared";

export const AISection: Component = () => {
  const [provider] = createResource(fetchAIProvider);
  const [model] = createResource(fetchAIModel);
  const [prov, setProv] = createSignal("ollama");
  const [mdl, setMdl] = createSignal("");
  createEffect(() => {
    const p = provider();
    if (p) setProv(p);
  });
  createEffect(() => {
    const m = model();
    if (m !== undefined) setMdl(m);
  });
  const status = useSaveStatus();
  const save = async () => {
    try {
      await putAIProvider(prov());
      await putAIModel(mdl());
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };

  // Connection data owned here the same way ConnectionsTable does it. Rows must
  // mount only AFTER conns() resolves — each ConnectionRow seeds its local
  // signals (URL, hasExistingKey) from props.existing at mount, so mounting
  // while conns() is still undefined would seed hasExistingKey=false and an
  // untouched save would send apiKey="" and WIPE the stored secret (Guardrail
  // #5). Same gate the Connections table uses.
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
    <>
      <Card title="AI (shared by Adult identification and the Movies/Series title-guess fallback)">
        <form onSubmit={(e) => (e.preventDefault(), void save())}>
          <div class="grid gap-3 sm:grid-cols-2">
            <label class="block">
              <span class={labelClass}>Provider</span>
              <select
                class={`${inputClass} mt-1`}
                aria-label="AI provider"
                value={prov()}
                onChange={(e) => setProv(e.currentTarget.value)}
              >
                <For each={AI_PROVIDERS}>
                  {(p) => <option value={p}>{p}</option>}
                </For>
              </select>
            </label>
            <label class="block">
              <span class={labelClass}>Model</span>
              <input
                type="text"
                class={`${inputClass} mt-1`}
                placeholder="e.g. qwen2.5vl:7b, gpt-4o-mini, gemini-2.5-flash, claude-haiku-4-5"
                value={mdl()}
                onInput={(e) => setMdl(e.currentTarget.value)}
              />
            </label>
          </div>
          <div class="mt-3 flex items-center gap-2">
            <Button variant="primary" type="submit">
              Save
            </Button>
            <SaveStatus
              text={status.status().text}
              error={status.status().error}
            />
          </div>
        </form>
        <Muted class="mt-2">
          The model must be able to return structured JSON. Configure the
          selected provider's connection below — only that provider's fields
          show, since it's the one this selector governs.
        </Muted>
      </Card>

      <Card title="Selected provider connection">
        <Show when={conns() !== undefined}>
          <ConnectionMiniTable>
            {/* keyed on the provider signal so switching the dropdown remounts
                the row against the new service's stored connection (each
                ConnectionRow seeds from props.existing only at mount). */}
            <Show when={prov()} keyed>
              {(p) => (
                <ConnectionRow
                  service={p}
                  existing={byService()[p]}
                  finding={findingByService()[p]}
                  onChanged={() => void refetch()}
                />
              )}
            </Show>
          </ConnectionMiniTable>
        </Show>
      </Card>

      <Card title="Web search grounding (Brave)">
        <Muted class="mb-3">
          Used for Adult identification regardless of which AI provider above is
          active.
        </Muted>
        <Show when={conns() !== undefined}>
          <ConnectionMiniTable>
            <ConnectionRow
              service="brave"
              existing={byService()["brave"]}
              finding={findingByService()["brave"]}
              onChanged={() => void refetch()}
            />
          </ConnectionMiniTable>
        </Show>
      </Card>
    </>
  );
};
