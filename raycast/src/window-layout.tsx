import {
  Action,
  ActionPanel,
  Color,
  Icon,
  List,
  showHUD,
  showToast,
  Toast,
} from "@raycast/api";
import { execFile } from "node:child_process";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { useEffect, useState } from "react";

const execFileAsync = promisify(execFile);

const TMUX_CHROME_BIN =
  process.env.TMUX_CHROME_BIN ?? `${os.homedir()}/.local/bin/tmux-chrome`;
const LAYOUTS_FILE =
  process.env.TMUX_CHROME_LAYOUTS_FILE ??
  path.join(os.homedir(), ".config", "tmux-chrome", "window-layouts.json");
const COMMAND_ENV = {
  ...process.env,
  PATH: `/opt/homebrew/bin:/usr/bin:/bin:${process.env.PATH ?? ""}`,
};

interface WindowLayout {
  id: string;
  title?: string;
  name?: string;
  mode?: "display" | "split";
  left: string;
  right: string;
  gap?: number;
}

interface LayoutConfig {
  layouts?: WindowLayout[];
}

function layoutTitle(layout: WindowLayout): string {
  return layout.title || layout.name || layout.id;
}

async function runTmuxChrome(args: string[]) {
  return execFileAsync(TMUX_CHROME_BIN, args, {
    env: COMMAND_ENV,
    maxBuffer: 1024 * 1024,
    timeout: 10_000,
  });
}

async function ensureLayoutConfig(): Promise<void> {
  await runTmuxChrome(["window-layout", "init"]);
}

async function loadLayouts(): Promise<WindowLayout[]> {
  await ensureLayoutConfig();
  const raw = await fs.readFile(LAYOUTS_FILE, "utf8");
  const parsed = JSON.parse(raw) as LayoutConfig;
  return (parsed.layouts ?? []).filter(
    (layout) => layout.id && layout.left && layout.right,
  );
}

export default function WindowLayoutCommand() {
  const [layouts, setLayouts] = useState<WindowLayout[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  async function reload() {
    setIsLoading(true);
    setError(null);
    try {
      setLayouts(await loadLayouts());
    } catch (e) {
      const err = e as NodeJS.ErrnoException & { stderr?: string };
      setError(err.stderr?.trim() || err.message || String(e));
    } finally {
      setIsLoading(false);
    }
  }

  useEffect(() => {
    void reload();
  }, []);

  if (error) {
    return (
      <List>
        <List.EmptyView
          icon={Icon.Warning}
          title="Could not load layouts"
          description={error}
        />
      </List>
    );
  }

  return (
    <List isLoading={isLoading} searchBarPlaceholder="Search window layouts...">
      {layouts.map((layout) => (
        <List.Item
          key={layout.id}
          title={layoutTitle(layout)}
          subtitle={
            layout.mode === "split"
              ? `${layout.left} → left half, ${layout.right} → right half`
              : `${layout.left} → left display, ${layout.right} → right display`
          }
          icon={{ source: Icon.AppWindow, tintColor: Color.Blue }}
          accessories={[
            {
              tag: {
                value: layout.mode === "split" ? "split" : "displays",
                color: layout.mode === "split" ? Color.Orange : Color.Blue,
              },
            },
            { tag: { value: layout.left, color: Color.Green } },
            { tag: { value: layout.right, color: Color.Purple } },
          ]}
          keywords={[
            layout.id,
            layout.left,
            layout.right,
            layout.mode ?? "display",
          ]}
          actions={
            <ActionPanel>
              <Action
                title="Apply Layout"
                icon={Icon.AppWindow}
                onAction={async () => {
                  try {
                    const { stdout } = await runTmuxChrome([
                      "window-layout",
                      "apply",
                      layout.id,
                    ]);
                    await showHUD(
                      stdout.trim() || `Applied ${layoutTitle(layout)}`,
                    );
                  } catch (e) {
                    const err = e as NodeJS.ErrnoException & {
                      stderr?: string;
                    };
                    await showToast({
                      style: Toast.Style.Failure,
                      title: "Layout failed",
                      message:
                        err.stderr?.trim() ||
                        err.message ||
                        `Failed to apply ${layoutTitle(layout)}`,
                    });
                  }
                }}
              />
              <Action.Open
                title="Edit Layout JSON"
                icon={Icon.Pencil}
                target={LAYOUTS_FILE}
                application="Visual Studio Code"
                shortcut={{ modifiers: ["cmd"], key: "e" }}
              />
              <Action
                title="Refresh"
                icon={Icon.ArrowClockwise}
                shortcut={{ modifiers: ["cmd"], key: "r" }}
                onAction={reload}
              />
            </ActionPanel>
          }
        />
      ))}
    </List>
  );
}
