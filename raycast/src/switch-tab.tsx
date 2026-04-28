import { Action, ActionPanel, closeMainWindow, Color, Icon, List, PopToRootType, showToast, Toast } from "@raycast/api";
import { usePromise } from "@raycast/utils";
import { focusTab, getActiveGroupTabs, type Tab } from "./bridge";
import { activateMacApp } from "./tmux";

async function loadActiveGroupTabs(): Promise<{ groupTitle: string; tabs: Tab[] }> {
  const tabs = await getActiveGroupTabs();
  const groupTitle = tabs[0]?.group_title ?? "";
  return { groupTitle, tabs };
}

export default function SwitchTab() {
  const { data, isLoading, error, revalidate } = usePromise(loadActiveGroupTabs);

  if (error) {
    return (
      <List>
        <List.EmptyView icon={Icon.Warning} title="Tabs unavailable" description={error.message} />
      </List>
    );
  }

  const groupTitle = data?.groupTitle ?? "";
  const tabs = data?.tabs ?? [];

  return (
    <List isLoading={isLoading} searchBarPlaceholder={groupTitle ? `Search tabs in ${groupTitle}...` : "Search tabs..."}>
      {tabs.length === 0 && !isLoading ? (
        <List.EmptyView
          icon={Icon.Globe}
          title={groupTitle ? `No tabs in ${groupTitle}` : "No active tab group"}
          description={
            groupTitle
              ? `The currently active Chrome tab group is empty.`
              : `Could not determine the active Chrome tab group.`
          }
        />
      ) : (
        <List.Section title={groupTitle} subtitle={`${tabs.length} tab${tabs.length !== 1 ? "s" : ""}`}>
          {tabs.map((tab) => (
            <List.Item
              key={tab.id}
              title={tab.title || "(untitled)"}
              subtitle={tab.url}
              icon={Icon.Globe}
              accessories={[{ tag: { value: groupTitle, color: Color.Blue } }]}
              actions={
                <ActionPanel>
                  <Action
                    title="Switch to Tab"
                    icon={Icon.Eye}
                    onAction={async () => {
                      try {
                        await focusTab(tab.id);
                        await activateMacApp("Google Chrome");
                        await closeMainWindow({ popToRootType: PopToRootType.Immediate });
                      } catch (e) {
                        await showToast({ style: Toast.Style.Failure, title: "Failed", message: String(e) });
                      }
                    }}
                  />
                  <Action.CopyToClipboard title="Copy URL" content={tab.url} shortcut={{ modifiers: ["cmd"], key: "c" }} />
                  <Action.OpenInBrowser title="Open in Browser" url={tab.url} shortcut={{ modifiers: ["cmd"], key: "o" }} />
                  <Action title="Refresh" icon={Icon.ArrowClockwise} shortcut={{ modifiers: ["cmd"], key: "r" }} onAction={revalidate} />
                </ActionPanel>
              }
            />
          ))}
        </List.Section>
      )}
    </List>
  );
}
