import { useEffect, useState } from "react";
import {
  Action,
  ActionPanel,
  Alert,
  confirmAlert,
  Icon,
  List,
  showToast,
  Toast,
} from "@raycast/api";
import { deleteGroup, listGroups, TabGroup } from "./bridge";
import { listWindows } from "./tmux";

// Chrome's own reserved group, used by `clean-tabs` to park ungrouped tabs.
// It intentionally has no tmux window, so it must never be offered for
// deletion.
const RESERVED = "native";

// The same normalisation `bin/tmux-chrome` applies before comparing a group
// title to a window name. It has to match exactly, or this command and
// `tmux-chrome sync` would disagree about what counts as an orphan: the
// status prefixes are written by the Claude Code hooks, and NFKC folding
// matters because window names are often typed with full-width characters.
const WORKING_PREFIX = "⏳";
const DONE_PREFIX = "🟢";

function normalize(title: string): string {
  let s = title;
  if (s.startsWith(WORKING_PREFIX)) s = s.slice(WORKING_PREFIX.length);
  else if (s.startsWith(DONE_PREFIX)) s = s.slice(DONE_PREFIX.length);
  return s
    .trimStart()
    .normalize("NFKC")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "");
}

interface Orphan {
  group: TabGroup;
  /** Same title can exist on several groups; this disambiguates the rows. */
  duplicateOf?: number;
}

export default function CleanOrphanGroups() {
  const [orphans, setOrphans] = useState<Orphan[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setOrphans(null);
    setError(null);
    try {
      // Windows from every tmux session, not just the attached one: a group
      // whose window lives in a detached session is not an orphan.
      const [groups, windows] = await Promise.all([
        listGroups(),
        listWindows(),
      ]);
      const live = new Set(windows.map((w) => normalize(w.windowName)));
      const seen = new Map<string, number>();
      const found: Orphan[] = [];
      for (const group of groups) {
        const key = normalize(group.title);
        if (key === RESERVED || live.has(key)) continue;
        const prior = seen.get(key);
        seen.set(key, (prior ?? 0) + 1);
        found.push({ group, duplicateOf: prior ? prior + 1 : undefined });
      }
      setOrphans(found);
    } catch (e) {
      setError(String(e));
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function removeOne(orphan: Orphan) {
    try {
      await deleteGroup(orphan.group.title);
      await showToast({
        style: Toast.Style.Success,
        title: "Removed",
        message: orphan.group.title,
      });
      await load();
    } catch (e) {
      await showToast({
        style: Toast.Style.Failure,
        title: "Failed",
        message: String(e),
      });
    }
  }

  async function removeAll() {
    if (!orphans?.length) return;
    const tabs = orphans.reduce((n, o) => n + o.group.tab_count, 0);
    if (
      !(await confirmAlert({
        title: `Remove ${orphans.length} orphan group${orphans.length !== 1 ? "s" : ""}?`,
        message: `${tabs} tab${tabs !== 1 ? "s" : ""} will be ungrouped. Tabs are not closed.`,
        primaryAction: {
          title: "Remove",
          style: Alert.ActionStyle.Destructive,
        },
        dismissAction: { title: "Cancel" },
      }))
    ) {
      return;
    }
    const toast = await showToast({
      style: Toast.Style.Animated,
      title: `Removing ${orphans.length}…`,
    });
    let removed = 0;
    const failed: string[] = [];
    // Sequentially: the bridge handles one request per connection, and a
    // parallel burst would race on group ids that shift as groups disappear.
    for (const orphan of orphans) {
      try {
        await deleteGroup(orphan.group.title);
        removed++;
      } catch {
        failed.push(orphan.group.title);
      }
    }
    toast.style = failed.length ? Toast.Style.Failure : Toast.Style.Success;
    toast.title = `Removed ${removed}`;
    if (failed.length) toast.message = `Failed: ${failed.join(", ")}`;
    await load();
  }

  if (error) {
    return (
      <List>
        <List.EmptyView
          icon={Icon.Plug}
          title="Bridge unavailable"
          description={error}
          actions={
            <ActionPanel>
              <Action
                title="Retry"
                icon={Icon.ArrowClockwise}
                onAction={load}
              />
            </ActionPanel>
          }
        />
      </List>
    );
  }

  return (
    <List
      isLoading={orphans === null}
      searchBarPlaceholder="Filter orphan groups…"
    >
      {orphans?.length === 0 ? (
        <List.EmptyView
          icon={Icon.Checkmark}
          title="No orphan groups"
          description="Every Chrome tab group has a matching tmux window."
        />
      ) : (
        orphans?.map((orphan, i) => (
          <List.Item
            key={`${orphan.group.id}-${i}`}
            icon={{ source: Icon.Circle, tintColor: orphan.group.color }}
            title={orphan.group.title}
            subtitle={
              orphan.duplicateOf
                ? `duplicate #${orphan.duplicateOf}`
                : undefined
            }
            accessories={[
              {
                text: `${orphan.group.tab_count} tab${orphan.group.tab_count !== 1 ? "s" : ""}`,
              },
            ]}
            actions={
              <ActionPanel>
                <Action
                  title="Remove This Group"
                  icon={Icon.Trash}
                  style={Action.Style.Destructive}
                  onAction={() => removeOne(orphan)}
                />
                <Action
                  title={`Remove All ${orphans.length}`}
                  icon={Icon.Trash}
                  style={Action.Style.Destructive}
                  shortcut={{ modifiers: ["cmd", "shift"], key: "delete" }}
                  onAction={removeAll}
                />
                <Action
                  title="Refresh"
                  icon={Icon.ArrowClockwise}
                  shortcut={{ modifiers: ["cmd"], key: "r" }}
                  onAction={load}
                />
              </ActionPanel>
            }
          />
        ))
      )}
    </List>
  );
}
