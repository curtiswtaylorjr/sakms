// AddRssFeedModal — the always-visible "+" tile's add-feed form: title, feed
// URL, protocol picker, target picker. allowedTargets restricts the picker
// to what the backend will accept for this screen (movie/tv on Mainstream,
// fixed to adult on Adult) — same "restrict the picker to what's valid"
// convention SliderAdmin.tsx's targetsAllowedFor already uses. Reuses
// shared.tsx's Modal primitive, same as every other overlay in this app.

import { type Component, For, Show, createSignal } from "solid-js";
import {
  PROTOCOLS,
  type RssFeedProtocol,
  type RssFeedTarget,
  createRssFeed,
} from "../../api/rssFeeds";
import { Button, ErrorText, inputClass, labelClass } from "../../components/ui";
import { Modal } from "./shared";

export const AddRssFeedModal: Component<{
  allowedTargets: RssFeedTarget[];
  defaultTarget: RssFeedTarget;
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const [title, setTitle] = createSignal("");
  const [feedUrl, setFeedUrl] = createSignal("");
  const [target, setTarget] = createSignal<RssFeedTarget>(props.defaultTarget);
  const [protocol, setProtocol] = createSignal<RssFeedProtocol>("usenet");
  const [saving, setSaving] = createSignal(false);
  const [error, setError] = createSignal("");

  const save = async () => {
    setError("");
    if (!title().trim()) {
      setError("Enter a title first.");
      return;
    }
    if (!feedUrl().trim()) {
      setError("Enter a feed URL first.");
      return;
    }
    setSaving(true);
    try {
      await createRssFeed({
        title: title().trim(),
        feedUrl: feedUrl().trim(),
        target: target(),
        protocol: protocol(),
        enabled: true,
      });
      props.onSaved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title="Add RSS feed" onClose={props.onClose}>
      <label class="mb-2 block">
        <span class={labelClass}>Title</span>
        <input
          type="text"
          class={`${inputClass} mt-1`}
          aria-label="Feed title"
          value={title()}
          onInput={(e) => setTitle(e.currentTarget.value)}
        />
      </label>
      <label class="mb-2 block">
        <span class={labelClass}>Feed URL</span>
        <input
          type="text"
          class={`${inputClass} mt-1`}
          aria-label="Feed URL"
          placeholder="https://nzbgeek.info/rss?..."
          value={feedUrl()}
          onInput={(e) => setFeedUrl(e.currentTarget.value)}
        />
      </label>
      <div class="grid gap-3 sm:grid-cols-2">
        <Show when={props.allowedTargets.length > 1}>
          <label class="block">
            <span class={labelClass}>Target</span>
            <select
              class={`${inputClass} mt-1`}
              aria-label="Target"
              value={target()}
              onChange={(e) => setTarget(e.currentTarget.value as RssFeedTarget)}
            >
              <For each={props.allowedTargets}>
                {(t) => <option value={t}>{t}</option>}
              </For>
            </select>
          </label>
        </Show>
        <label class="block">
          <span class={labelClass}>Protocol</span>
          <select
            class={`${inputClass} mt-1`}
            aria-label="Protocol"
            value={protocol()}
            onChange={(e) => setProtocol(e.currentTarget.value as RssFeedProtocol)}
          >
            <For each={PROTOCOLS}>{(p) => <option value={p}>{p}</option>}</For>
          </select>
        </label>
      </div>
      <Show when={error()}>
        <ErrorText>{error()}</ErrorText>
      </Show>
      <div class="mt-3 flex justify-end gap-2">
        <Button onClick={props.onClose}>Cancel</Button>
        <Button variant="primary" onClick={() => void save()} disabled={saving()}>
          {saving() ? "Saving…" : "Add feed"}
        </Button>
      </div>
    </Modal>
  );
};
