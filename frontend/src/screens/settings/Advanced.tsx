// Advanced section — the per-mode bounded-integer settings (phash threshold,
// match-confidence threshold, global recheck interval) and the Adult-only
// identify-enabled toggle. Extracted from the original single-file Settings.tsx.

import {
  type Component,
  createEffect,
  createResource,
  createSignal,
  on,
  Show,
} from "solid-js";
import type { Mode } from "../../api/discover";
import {
  fetchConfidenceThreshold,
  fetchIdentifyEnabled,
  fetchPHashThreshold,
  fetchRecheckInterval,
  putConfidenceThreshold,
  putIdentifyEnabled,
  putPHashThreshold,
  putRecheckInterval,
} from "../../api/settings";
import { Button, Muted, inputClass, labelClass } from "../../components/ui";
import { Card, MODE_LABELS, SaveStatus, useSaveStatus } from "./shared";

// NumberSetting is one bounded integer field (phash-threshold,
// match-confidence-threshold, recheck-interval). It mirrors the backend's range
// client-side (min/max) before submitting; the backend re-validates. save
// disabled while out of range so the operator sees the bound, never a 400.
const NumberSetting: Component<{
  label: string;
  help: string;
  value: () => number | undefined;
  min: number;
  max?: number;
  onSave: (v: number) => Promise<void>;
}> = (props) => {
  const [val, setVal] = createSignal(0);
  createEffect(() => {
    const v = props.value();
    if (v !== undefined) setVal(v);
  });
  const status = useSaveStatus();
  const outOfRange = () =>
    val() < props.min || (props.max !== undefined && val() > props.max);
  const save = async () => {
    if (outOfRange()) {
      status.failed(
        new Error(
          props.max !== undefined
            ? `must be between ${props.min} and ${props.max}`
            : `must be ${props.min} or greater`,
        ),
      );
      return;
    }
    try {
      await props.onSave(val());
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };
  return (
    <div class="mb-3">
      <label class="block">
        <span class={labelClass}>{props.label}</span>
        <input
          type="number"
          class={`${inputClass} mt-1 !w-40`}
          min={props.min}
          max={props.max}
          aria-label={props.label}
          value={val()}
          onInput={(e) => setVal(Number(e.currentTarget.value))}
        />
      </label>
      <div class="mt-2 flex items-center gap-2">
        <Button variant="primary" onClick={() => void save()}>
          Save
        </Button>
        <SaveStatus text={status.status().text} error={status.status().error} />
      </div>
      <Muted class="mt-1">{props.help}</Muted>
    </div>
  );
};

const IdentifyEnabledSetting: Component<{ mode: () => Mode }> = (props) => {
  const [current] = createResource(props.mode, fetchIdentifyEnabled);
  const [enabled, setEnabled] = createSignal(true);
  createEffect(
    on(current, (v) => {
      if (v !== undefined) setEnabled(v);
    }),
  );
  const status = useSaveStatus();
  const save = async () => {
    try {
      await putIdentifyEnabled(props.mode(), enabled());
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };
  return (
    <div class="mb-3">
      <label class="flex items-center gap-2">
        <input
          type="checkbox"
          aria-label="Adult phash-first identification enabled"
          checked={enabled()}
          onChange={(e) => setEnabled(e.currentTarget.checked)}
        />
        <span class="text-sm text-fg">
          Adult phash-first identification enabled
        </span>
      </label>
      <div class="mt-2 flex items-center gap-2">
        <Button variant="primary" onClick={() => void save()}>
          Save
        </Button>
        <SaveStatus text={status.status().text} error={status.status().error} />
      </div>
      <Muted class="mt-1">
        When on, Adult Rename identifies scenes by perceptual hash first (no live
        Stash required). Turn off to skip identification.
      </Muted>
    </div>
  );
};

export const AdvancedSection: Component<{ mode: () => Mode }> = (props) => {
  // recheck-interval is GLOBAL, not per-mode — fetched once, independent of the
  // mode tab.
  const [recheck] = createResource(fetchRecheckInterval);
  // phash-threshold is per-mode-generic; confidence is Movies/Series only;
  // identify-enabled is Adult only. Each keyed on the mode accessor.
  const [phash] = createResource(props.mode, fetchPHashThreshold);
  const [confidence] = createResource(
    () => (props.mode() === "adult" ? undefined : props.mode()),
    fetchConfidenceThreshold,
  );

  return (
    <Card title={`Advanced Settings (${MODE_LABELS[props.mode()]})`}>
      <NumberSetting
        label="Background recheck interval (seconds) — global"
        help="0 turns the background recheck job off (the opt-in default). Any positive number of seconds enables it; a change takes effect on the running loop's next tick, or on next restart if it was off at boot."
        value={() => recheck()}
        min={0}
        onSave={(v) => putRecheckInterval(v)}
      />
      <NumberSetting
        label="Dedup phash similarity threshold (0–64)"
        help="Per-frame average Hamming bits below which two files are treated as perceptual duplicates by Dedup. Lower is stricter."
        value={() => phash()}
        min={0}
        max={64}
        onSave={(v) => putPHashThreshold(props.mode(), v)}
      />
      <Show when={props.mode() !== "adult"}>
        <NumberSetting
          label="Rename match-confidence threshold (0–100)"
          help="Minimum TMDB match confidence (a percentage) before Rename auto-accepts a match instead of surfacing it for manual re-pick."
          value={() => confidence()}
          min={0}
          max={100}
          onSave={(v) => putConfidenceThreshold(props.mode(), v)}
        />
      </Show>
      <Show when={props.mode() === "adult"}>
        <IdentifyEnabledSetting mode={props.mode} />
      </Show>
    </Card>
  );
};
