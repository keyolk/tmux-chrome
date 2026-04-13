import { Action, ActionPanel, Color, Icon, List, showToast, Toast } from "@raycast/api";
import { usePromise } from "@raycast/utils";
import { focusTab, getAllTabs, type Tab } from "./bridge";

export default function BrowseTabs() {
  const { data, isLoading, error, revalidate } = usePromise(getAllTabs);

  if (error) {
    return (
      <List>
        <List.EmptyView icon={Icon.Warning} title="Bridge not connected" description={error.message} />
      </List>
    );
  }

  // Group tabs by group_title for sections
  const sections = new Map<string, Tab[]>();
  for (const tab of data ?? []) {
    const key = tab.group_title;
    if (!sections.has(key)) sections.set(key, []);
    sections.get(key)!.push(tab);
  }

  return (
    <List isLoading={isLoading} searchBarPlaceholder="Search tabs...">
      {[...sections.entries()].map(([groupTitle, tabs]) => (
        <List.Section key={groupTitle} title={groupTitle} subtitle={`${tabs.length} tab${tabs.length !== 1 ? "s" : ""}`}>
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
                    title="Focus Tab"
                    icon={Icon.Eye}
                    onAction={async () => {
                      try {
                        await focusTab(tab.id);
                        await showToast({ style: Toast.Style.Success, title: `Focused: ${tab.title}` });
                        revalidate();
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
      ))}
    </List>
  );
}
