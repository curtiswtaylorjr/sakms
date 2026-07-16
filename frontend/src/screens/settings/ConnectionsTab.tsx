// ConnectionsTab — the Settings "Connections" tab's own inline sub-tab split:
// Connections (the connection table + Trakt) and AI (provider/model config +
// the AI-provider/Brave connection rows + Entity Database), folded together
// under one top-level tab since AI configuration is conceptually a kind of
// connection setup. Both panels are relocated here unchanged — ConnectionsSection
// and AISection each keep their OWN independent SectionSave/Save button, exactly
// as before; this tab only reparents them under one nav home, it does not merge
// their save behavior.
//
// The inner Connections/AI switch is a PLAIN ScreenTabBar, NOT ScreenTabs/
// useScreenTabs, for the same reason UI.tsx's Discover subsection uses one: the
// app shell has a single global tab-bar slot, already held by Settings' own
// SECTION_TABS. ScreenTabs registers a tab set with that slot, so using it here
// would overwrite Settings' section tabs the moment this tab mounts. ScreenTabBar
// renders inline and never touches the shell registration.

import { type Component, createSignal, Match, Switch } from "solid-js";
import { ScreenTabBar, type TabDef } from "../../components/ui";
import { ConnectionsSection } from "./Connections";
import { AISection } from "./AI";

const CONNECTIONS_TABS: TabDef[] = [
  { id: "connections", label: "Connections" },
  { id: "ai", label: "AI" },
];

export const ConnectionsTabSection: Component = () => {
  const [tab, setTab] = createSignal("connections");

  return (
    <div>
      <ScreenTabBar
        tabs={CONNECTIONS_TABS}
        current={tab}
        onSelect={setTab}
        class="mb-4 flex gap-1"
      />
      <Switch>
        <Match when={tab() === "connections"}>
          <ConnectionsSection />
        </Match>
        <Match when={tab() === "ai"}>
          <AISection />
        </Match>
      </Switch>
    </div>
  );
};
