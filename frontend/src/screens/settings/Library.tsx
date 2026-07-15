// Library section — the per-mode (Movies/Series) panels: root folder, search
// quality preferences, file/folder naming preset, and kids classification path.
// Extracted from the original single-file Settings.tsx.

import {
  type Component,
  createEffect,
  createResource,
  createSignal,
  on,
  For,
} from "solid-js";
import type { Mode } from "../../api/discover";
import {
  MAX_RESOLUTIONS,
  NAMING_PRESETS,
  QUALITY_TIERS,
  fetchKidsRootPath,
  fetchLibraryRootFolder,
  fetchNamingPreset,
  fetchQualityPrefs,
  putKidsRootPath,
  putLibraryRootFolder,
  putNamingPreset,
  putQualityPrefs,
} from "../../api/settings";
import { Button, Muted, inputClass, labelClass } from "../../components/ui";
import { FolderPicker } from "../../components/FolderPicker";
import { Card, MODE_LABELS, SaveStatus, useSaveStatus } from "./shared";

// ---- Per-mode: library root folder ----------------------------------------

export const LibraryRootFolderSection: Component<{ mode: () => Mode }> = (
  props,
) => {
  const [current] = createResource(props.mode, fetchLibraryRootFolder);
  const [path, setPath] = createSignal("");
  createEffect(
    on(current, (p) => {
      if (p !== undefined) setPath(p ?? "");
    }),
  );
  const status = useSaveStatus();
  const save = async () => {
    try {
      await putLibraryRootFolder(props.mode(), path());
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };
  return (
    <Card title={`${MODE_LABELS[props.mode()]} library`}>
      <form onSubmit={(e) => (e.preventDefault(), void save())}>
        <label class="block">
          <span class={labelClass}>Root folder</span>
          <FolderPicker
            value={path}
            onChange={setPath}
            ariaLabel="Library root folder"
            placeholder={`/path/to/${MODE_LABELS[props.mode()]}`}
          />
        </label>
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
        Where Rename/Purge/Dedup and Search's Check &amp; Import look for and
        place {MODE_LABELS[props.mode()]} files — no{" "}
        {props.mode() === "movies"
          ? "Radarr"
          : props.mode() === "series"
            ? "Sonarr"
            : "Whisparr"}{" "}
        involved.
      </Muted>
    </Card>
  );
};

// ---- Per-mode: quality preferences ----------------------------------------

export const QualityPrefsSection: Component<{ mode: () => Mode }> = (props) => {
  const [prefs] = createResource(props.mode, fetchQualityPrefs);
  const [tier, setTier] = createSignal("high");
  const [maxRes, setMaxRes] = createSignal(0);
  createEffect(
    on(prefs, (p) => {
      if (p) {
        setTier(p.tier);
        setMaxRes(p.maxResolution);
      }
    }),
  );
  const status = useSaveStatus();
  const save = async () => {
    try {
      await putQualityPrefs(props.mode(), {
        tier: tier(),
        maxResolution: maxRes(),
      });
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };
  return (
    <Card title={`Search quality preferences (${MODE_LABELS[props.mode()]})`}>
      <form onSubmit={(e) => (e.preventDefault(), void save())}>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block">
            <span class={labelClass}>Tier (bitrate/codec)</span>
            <select
              class={`${inputClass} mt-1`}
              value={tier()}
              onChange={(e) => setTier(e.currentTarget.value)}
            >
              <For each={QUALITY_TIERS}>{(t) => <option value={t}>{t}</option>}</For>
            </select>
          </label>
          <label class="block">
            <span class={labelClass}>Maximum resolution</span>
            <select
              class={`${inputClass} mt-1`}
              value={String(maxRes())}
              onChange={(e) => setMaxRes(Number(e.currentTarget.value))}
            >
              <For each={MAX_RESOLUTIONS}>
                {(r) => (
                  <option value={String(r)}>{r === 0 ? "no cap" : `${r}p`}</option>
                )}
              </For>
            </select>
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
        Tier prefers smaller/more-compressed releases (Low) up to the
        least-compressed remux/Blu-ray (Lossless) — it never changes what
        resolution is preferred. Maximum resolution softly prefers at-or-below-cap
        results, falling back to whatever's available if nothing meets it.
      </Muted>
    </Card>
  );
};

// ---- Per-mode: naming preset ----------------------------------------------

export const NamingPresetSection: Component<{ mode: () => Mode }> = (props) => {
  const [current] = createResource(props.mode, fetchNamingPreset);
  const [preset, setPreset] = createSignal("jellyfin");
  createEffect(
    on(current, (p) => {
      if (p) setPreset(p);
    }),
  );
  const status = useSaveStatus();
  const save = async () => {
    try {
      await putNamingPreset(props.mode(), preset());
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };
  return (
    <Card title={`File/folder naming (${MODE_LABELS[props.mode()]})`}>
      <form onSubmit={(e) => (e.preventDefault(), void save())}>
        <label class="block">
          <span class={labelClass}>Naming convention</span>
          <select
            class={`${inputClass} mt-1`}
            value={preset()}
            onChange={(e) => setPreset(e.currentTarget.value)}
          >
            <For each={NAMING_PRESETS}>
              {(p) => <option value={p.value}>{p.label}</option>}
            </For>
          </select>
        </label>
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
        Jellyfin/Emby standard renames into "Title (Year) [tmdbid-N]"
        folders/files. Legacy keeps this project's original convention, so an
        already-renamed library's shape never silently changes after an upgrade.
      </Muted>
    </Card>
  );
};

// ---- Per-mode: kids root path ---------------------------------------------

export const KidsRootPathSection: Component<{ mode: () => Mode }> = (props) => {
  const [current] = createResource(props.mode, fetchKidsRootPath);
  const [path, setPath] = createSignal("");
  createEffect(
    on(current, (p) => {
      if (p !== undefined) setPath(p ?? "");
    }),
  );
  const status = useSaveStatus();
  const save = async () => {
    try {
      await putKidsRootPath(props.mode(), path());
      status.saved();
    } catch (e) {
      status.failed(e);
    }
  };
  return (
    <Card title={`Kids classification (${MODE_LABELS[props.mode()]})`}>
      <form onSubmit={(e) => (e.preventDefault(), void save())}>
        <label class="block">
          <span class={labelClass}>Kids root folder path</span>
          <FolderPicker
            value={path}
            onChange={setPath}
            ariaLabel="Kids root folder path"
            placeholder={`/path/to/${MODE_LABELS[props.mode()]} (Kids)`}
          />
        </label>
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
        Leave blank to turn Kids classification off. Applies to both newly-found
        files and already-tracked items whose classification has drifted.
      </Muted>
    </Card>
  );
};
