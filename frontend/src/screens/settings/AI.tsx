// AI section — provider + model selection, shared by Adult identification and the
// Movies/Series title-guess fallback. Extracted from the original single-file
// Settings.tsx.

import {
  type Component,
  createEffect,
  createResource,
  createSignal,
  For,
} from "solid-js";
import {
  AI_PROVIDERS,
  fetchAIModel,
  fetchAIProvider,
  putAIModel,
  putAIProvider,
} from "../../api/settings";
import { Button, Muted, inputClass, labelClass } from "../../components/ui";
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
  return (
    <Card title="AI (shared by Adult identification and the Movies/Series title-guess fallback)">
      <form onSubmit={(e) => (e.preventDefault(), void save())}>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class={labelClass}>Provider</span>
            <select
              class={`${inputClass} mt-1`}
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
        Configure a connection for whichever provider you pick above (same
        Connections table) — the model must be able to return structured JSON.
      </Muted>
    </Card>
  );
};
