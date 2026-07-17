// Collections — read-only list of TMDB movie collections derived from the
// tracked library. Movies-only: TMDB has no equivalent collection concept for
// Series or Adult, so there are no mode tabs here. Each row shows the
// collection name and how many tracked movies belong to it.
//
// This is a display screen only — no staged-for-approval queue, no mutating
// actions. Collections are populated automatically when a movie is applied
// (enrichMovieCollection in internal/api/proposals.go) and cannot be edited
// here. Any movie that belongs to a collection also shows the collection name
// as a subtitle on the Tag screen.

import { type Component, createResource, For, Show } from "solid-js";
import { fetchCollections } from "../api/collections";
import { ErrorText, Muted } from "../components/ui";

const Collections: Component = () => {
  const [collections] = createResource(fetchCollections);

  return (
    <div class="p-4">
      <h2 class="mb-4 text-lg font-semibold text-fg">Movie Collections</h2>
      <Show when={collections.error}>
        <ErrorText>{String(collections.error)}</ErrorText>
      </Show>
      <Show when={collections.loading}>
        <Muted>Loading…</Muted>
      </Show>
      <Show when={collections() && !collections.loading}>
        <Show
          when={(collections()?.length ?? 0) > 0}
          fallback={
            <Muted>
              No collections yet — they appear here once a movie with a TMDB
              collection is applied.
            </Muted>
          }
        >
          <div class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead>
                <tr class="border-b border-border text-xs uppercase tracking-wide text-muted">
                  <th class="px-2 py-2 font-medium">Collection</th>
                  <th class="px-2 py-2 font-medium text-right">Movies</th>
                </tr>
              </thead>
              <tbody>
                <For each={collections()}>
                  {(col) => (
                    <tr class="border-b border-border/60">
                      <td class="px-2 py-2 text-fg">{col.name}</td>
                      <td class="px-2 py-2 text-right text-muted">
                        {col.count}
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
        </Show>
      </Show>
    </div>
  );
};

export { Collections };
