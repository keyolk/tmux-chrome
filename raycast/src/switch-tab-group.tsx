import { Action, ActionPanel, closeMainWindow, Color, Icon, List, PopToRootType, showToast, Toast } from "@raycast/api";
import { usePromise } from "@raycast/utils";
import { deleteGroup, listGroups, switchGroup, type TabGroup } from "./bridge";
import { activateMacApp } from "./tmux";

const COLOR_MAP: Record<string, Color> = {
  grey: Color.SecondaryText,
  blue: Color.Blue,
  red: Color.Red,
  yellow: Color.Yellow,
  green: Color.Green,
  pink: Color.Magenta,
  purple: Color.Purple,
  cyan: Color.Blue,
  orange: Color.Orange,
};

function groupColor(color: string): Color {
  return COLOR_MAP[color] ?? Color.PrimaryText;
}

export default function SwitchTabGroup() {
  const { data, isLoading, error, revalidate } = usePromise(listGroups);

  if (error) {
    return (
      <List>
        <List.EmptyView icon={Icon.Warning} title="Bridge not connected" description={error.message} />
      </List>
    );
  }

  return (
    <List isLoading={isLoading} searchBarPlaceholder="Search tab groups...">
      {data?.map((group: TabGroup) => (
        <List.Item
          key={group.id}
          title={group.title}
          subtitle={`${group.tab_count} tab${group.tab_count !== 1 ? "s" : ""}`}
          icon={{ source: group.collapsed ? Icon.ChevronRight : Icon.ChevronDown, tintColor: groupColor(group.color) }}
          accessories={[
            {
              tag: {
                value: group.collapsed ? "collapsed" : "expanded",
                color: group.collapsed ? Color.SecondaryText : Color.Green,
              },
            },
          ]}
          actions={
            <ActionPanel>
              <Action
                title="Switch to Group"
                icon={Icon.ArrowRight}
                onAction={async () => {
                  try {
                    await switchGroup(group.title);
                    await activateMacApp("Google Chrome");
                    await closeMainWindow({ popToRootType: PopToRootType.Immediate });
                  } catch (e) {
                    await showToast({ style: Toast.Style.Failure, title: "Failed", message: String(e) });
                  }
                }}
              />
              <Action
                title="Delete Group"
                icon={Icon.Trash}
                style={Action.Style.Destructive}
                shortcut={{ modifiers: ["cmd"], key: "d" }}
                onAction={async () => {
                  try {
                    await deleteGroup(group.title);
                    await showToast({ style: Toast.Style.Success, title: `Deleted ${group.title}` });
                    revalidate();
                  } catch (e) {
                    await showToast({ style: Toast.Style.Failure, title: "Failed", message: String(e) });
                  }
                }}
              />
              <Action title="Refresh" icon={Icon.ArrowClockwise} shortcut={{ modifiers: ["cmd"], key: "r" }} onAction={revalidate} />
            </ActionPanel>
          }
        />
      ))}
    </List>
  );
}
