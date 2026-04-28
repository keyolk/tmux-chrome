import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

const TMUX_BIN = process.env.TMUX_BIN ?? "/opt/homebrew/bin/tmux";

const TERMINAL_APP_PATTERNS: Array<[RegExp, string]> = [
  [/\/Ghostty\.app\//i, "Ghostty"],
  [/(^|\/)ghostty(\s|$)/i, "Ghostty"],
  [/\/Alacritty\.app\//i, "Alacritty"],
  [/(^|\/)alacritty(\s|$)/i, "Alacritty"],
  [/\/kitty\.app\//i, "kitty"],
  [/(^|\/)kitty(\s|$)/i, "kitty"],
  [/\/(iTerm|iTerm2)\.app\//i, "iTerm"],
  [/\/Terminal\.app\//i, "Terminal"],
  [/\/WezTerm\.app\//i, "WezTerm"],
  [/(^|\/)wezterm-gui(\s|$)/i, "WezTerm"],
  [/\/Warp\.app\//i, "Warp"],
];

export interface TmuxWindow {
  sessionName: string;
  windowIndex: number;
  windowId: string;
  windowName: string;
  active: boolean;
  paneCount: number;
  zoomed: boolean;
  attached: boolean;
}

async function tmux(args: string[]): Promise<string> {
  try {
    const { stdout } = await execFileAsync(TMUX_BIN, args, { maxBuffer: 1024 * 1024 });
    return stdout;
  } catch (error) {
    const err = error as NodeJS.ErrnoException & { stderr?: string };
    if (err.code === "ENOENT") {
      throw new Error(`tmux binary not found at ${TMUX_BIN}. Set TMUX_BIN env to override.`);
    }
    const stderr = err.stderr?.toString().trim();
    if (stderr?.includes("no server running")) {
      throw new Error("No tmux server is running.");
    }
    throw new Error(stderr || err.message);
  }
}

export async function listWindows(): Promise<TmuxWindow[]> {
  const SEP = "@@@";
  const format = [
    "#{session_name}",
    "#{window_index}",
    "#{window_id}",
    "#{window_active}",
    "#{?window_zoomed_flag,1,0}",
    "#{window_panes}",
    "#{session_attached}",
    "#{window_name}",
  ].join(SEP);

  const stdout = await tmux(["list-windows", "-a", "-F", format]);

  return stdout
    .split("\n")
    .map((line) => line.replace(/\r$/, ""))
    .filter(Boolean)
    .map((line) => {
      const parts = line.split(SEP);
      if (parts.length < 8) {
        throw new Error(`Unexpected tmux line: ${line}`);
      }
      const [sessionName, windowIndex, windowId, active, zoomed, paneCount, attached, ...rest] = parts;
      const windowName = rest.join(SEP);
      return {
        sessionName,
        windowIndex: Number(windowIndex),
        windowId,
        windowName,
        active: active === "1",
        paneCount: Number(paneCount),
        zoomed: zoomed === "1",
        attached: Number(attached) > 0,
      };
    });
}

export async function selectWindow(windowId: string): Promise<void> {
  await tmux(["select-window", "-t", windowId]);
}

export async function switchClient(sessionName: string): Promise<void> {
  await tmux(["switch-client", "-t", sessionName]);
}

export async function killWindow(windowId: string): Promise<void> {
  await tmux(["kill-window", "-t", windowId]);
}

export async function focusTmuxWindow(window: TmuxWindow): Promise<void> {
  if (!window.attached) {
    await switchClient(window.sessionName);
  }
  await selectWindow(window.windowId);
}

export async function getActiveWindowName(): Promise<string | null> {
  const windows = await listWindows();
  const active = windows.find((win) => win.active && win.attached);
  return active?.windowName?.trim() || null;
}

export async function detectTmuxTerminalApp(): Promise<string | null> {
  let clientPid: number | null = null;
  try {
    const stdout = await tmux(["display-message", "-p", "#{client_pid}"]);
    const parsed = Number(stdout.trim());
    if (Number.isFinite(parsed) && parsed > 0) {
      clientPid = parsed;
    }
  } catch (_) {
    // ignore — fallback to ps walk
  }

  const ancestry: number[] = [];
  let pid = clientPid;
  let depth = 0;
  while (pid && pid > 1 && depth < 25) {
    ancestry.push(pid);
    try {
      const { stdout } = await execFileAsync("ps", ["-o", "ppid=,args=", "-p", String(pid)], {
        maxBuffer: 1024 * 1024,
      });
      const trimmed = stdout.trim();
      if (!trimmed) break;
      const space = trimmed.indexOf(" ");
      if (space < 0) break;
      const ppid = Number(trimmed.slice(0, space).trim());
      const args = trimmed.slice(space + 1).trim();
      const matched = matchTerminalApp(args);
      if (matched) return matched;
      pid = Number.isFinite(ppid) ? ppid : null;
    } catch (_) {
      break;
    }
    depth += 1;
  }

  // Fallback: scan all processes for a known terminal binary
  try {
    const { stdout } = await execFileAsync("ps", ["-axo", "comm,args"], { maxBuffer: 4 * 1024 * 1024 });
    for (const line of stdout.split("\n")) {
      const matched = matchTerminalApp(line);
      if (matched) return matched;
    }
  } catch (_) {
    // ignore
  }

  return null;
}

function matchTerminalApp(text: string): string | null {
  for (const [pattern, name] of TERMINAL_APP_PATTERNS) {
    if (pattern.test(text)) return name;
  }
  return null;
}

export async function activateMacApp(appName: string): Promise<void> {
  await execFileAsync("/usr/bin/osascript", ["-e", `tell application "${appName}" to activate`]);
}
