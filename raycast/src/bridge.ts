import net from "node:net";

const SOCKET_PATH = "/tmp/tmux-chrome-bridge.sock";
const TIMEOUT_MS = 5000;

let requestCounter = 0;

function makeId(): string {
  return `ray_${Date.now()}_${++requestCounter}`;
}

export async function bridgeRequest<T = unknown>(msg: Record<string, unknown>): Promise<T> {
  const id = makeId();
  const payload = JSON.stringify({ ...msg, id }) + "\n";

  return new Promise<T>((resolve, reject) => {
    const socket = net.createConnection(SOCKET_PATH);
    let data = "";

    const timer = setTimeout(() => {
      socket.destroy();
      reject(new Error("Bridge timeout"));
    }, TIMEOUT_MS);

    socket.on("connect", () => {
      socket.write(payload);
    });

    socket.on("data", (chunk) => {
      data += chunk.toString();
      if (data.includes("\n")) {
        clearTimeout(timer);
        socket.destroy();
        try {
          const response = JSON.parse(data.trim());
          if (response.success) {
            resolve(response.data as T);
          } else {
            reject(new Error(response.error || "Bridge request failed"));
          }
        } catch (e) {
          reject(new Error(`Invalid response: ${data}`));
        }
      }
    });

    socket.on("error", (err) => {
      clearTimeout(timer);
      if ((err as NodeJS.ErrnoException).code === "ENOENT") {
        reject(new Error("Chrome bridge not running. Open Chrome with the tmux bridge extension."));
      } else {
        reject(err);
      }
    });
  });
}

export interface TabGroup {
  id: number;
  title: string;
  color: string;
  collapsed: boolean;
  tab_count: number;
}

export interface Tab {
  id: number;
  title: string;
  url: string;
  group_title: string;
  group_id: number;
}

export async function listGroups(): Promise<TabGroup[]> {
  return bridgeRequest<TabGroup[]>({ type: "list_groups" });
}

export async function switchGroup(name: string): Promise<void> {
  await bridgeRequest({ type: "switch_window", window_name: name });
}

export async function getAllTabs(): Promise<Tab[]> {
  return bridgeRequest<Tab[]>({ type: "get_tabs", all: true });
}

export async function getTabsForGroup(name: string): Promise<Tab[]> {
  return bridgeRequest<Tab[]>({ type: "get_tabs", name });
}

export async function getActiveGroupTabs(): Promise<Tab[]> {
  return bridgeRequest<Tab[]>({ type: "get_tabs", active: true });
}

export async function focusTab(tabId: number): Promise<void> {
  await bridgeRequest({ type: "focus_tab", tab_id: tabId });
}

export async function deleteGroup(name: string): Promise<void> {
  await bridgeRequest({ type: "delete_group", name });
}
