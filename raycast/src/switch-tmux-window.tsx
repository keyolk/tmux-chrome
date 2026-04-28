import { Action, ActionPanel, closeMainWindow, Color, Icon, List, PopToRootType, showToast, Toast } from "@raycast/api";
import { usePromise } from "@raycast/utils";
import { activateMacApp, detectTmuxTerminalApp, focusTmuxWindow, killWindow, listWindows, type TmuxWindow } from "./tmux";

function windowTitle(window: TmuxWindow): string {
  const name = window.windowName?.trim();
  if (name) return name;
  return `${window.sessionName}:${window.windowIndex}`;
}

function windowAccessories(window: TmuxWindow): List.Item.Accessory[] {
  const accessories: List.Item.Accessory[] = [];
  if (window.active) {
    accessories.push({ tag: { value: "active", color: Color.Green } });
  }
  if (window.zoomed) {
    accessories.push({ tag: { value: "zoomed", color: Color.Orange } });
  }
  accessories.push({ text: `${window.paneCount} pane${window.paneCount !== 1 ? "s" : ""}` });
  return accessories;
}

export default function SwitchTmuxWindow() {
  const { data, isLoading, error, revalidate } = usePromise(listWindows);

  if (error) {
    return (
      <List>
        <List.EmptyView icon={Icon.Warning} title="tmux unavailable" description={error.message} />
      </List>
    );
  }

  const grouped = (data ?? []).reduce<Record<string, TmuxWindow[]>>((acc, win) => {
    (acc[win.sessionName] ??= []).push(win);
    return acc;
  }, {});

  const sessionNames = Object.keys(grouped).sort((a, b) => a.localeCompare(b));

  return (
    <List isLoading={isLoading} searchBarPlaceholder="Search tmux windows...">
      {sessionNames.map((sessionName) => {
        const windows = [...grouped[sessionName]].sort((a, b) => a.windowIndex - b.windowIndex);
        return (
          <List.Section key={sessionName} title={sessionName}>
            {windows.map((window) => (
              <List.Item
                key={`${sessionName}:${window.windowId}:${window.windowIndex}`}
                title={windowTitle(window)}
                subtitle={`#${window.windowIndex}`}
                icon={{
                  source: window.active ? Icon.Window : Icon.AppWindow,
                  tintColor: window.active ? Color.Green : Color.PrimaryText,
                }}
                accessories={windowAccessories(window)}
                keywords={[sessionName, window.windowId, String(window.windowIndex)]}
                actions={
                  <ActionPanel>
                    <Action
                      title="Switch to Window"
                      icon={Icon.ArrowRight}
                      onAction={async () => {
                        try {
                          await focusTmuxWindow(window);
                          const terminalApp = await detectTmuxTerminalApp();
                          if (terminalApp) {
                            await activateMacApp(terminalApp);
                          }
                          await closeMainWindow({ popToRootType: PopToRootType.Immediate });
                        } catch (e) {
                          await showToast({ style: Toast.Style.Failure, title: "Failed", message: String(e) });
                        }
                      }}
                    />
                    <Action
                      title="Refresh"
                      icon={Icon.ArrowClockwise}
                      shortcut={{ modifiers: ["cmd"], key: "r" }}
                      onAction={revalidate}
                    />
                    <Action
                      title="Kill Window"
                      icon={Icon.Trash}
                      style={Action.Style.Destructive}
                      shortcut={{ modifiers: ["cmd"], key: "d" }}
                      onAction={async () => {
                        try {
                          await killWindow(window.windowId);
                          await showToast({
                            style: Toast.Style.Success,
                            title: `Killed ${windowTitle(window)}`,
                            message: `${sessionName}:${window.windowIndex}`,
                          });
                          revalidate();
                        } catch (e) {
                          await showToast({ style: Toast.Style.Failure, title: "Failed", message: String(e) });
                        }
                      }}
                    />
                  </ActionPanel>
                }
              />
            ))}
          </List.Section>
        );
      })}
    </List>
  );
}
