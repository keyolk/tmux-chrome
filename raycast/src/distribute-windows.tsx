import { showHUD, showToast, Toast } from "@raycast/api";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import os from "node:os";

const execFileAsync = promisify(execFile);

// Resolve the tmux-chrome CLI: prefer an explicit override, then the symlink
// in ~/.local/bin. The CLI owns all display-enumeration and window-move logic
// (NSScreen via JXA), so this command is a thin launcher.
const TMUX_CHROME_BIN =
  process.env.TMUX_CHROME_BIN ?? `${os.homedir()}/.local/bin/tmux-chrome`;

export default async function Command() {
  try {
    const { stdout } = await execFileAsync(TMUX_CHROME_BIN, ["distribute"], {
      // Ensure /opt/homebrew/bin (tmux, osascript deps) is on PATH for the CLI.
      env: {
        ...process.env,
        PATH: `/opt/homebrew/bin:/usr/bin:/bin:${process.env.PATH ?? ""}`,
      },
      maxBuffer: 1024 * 1024,
      timeout: 10_000,
    });
    await showHUD(stdout.trim() || "Distributed terminal & Chrome");
  } catch (error) {
    const err = error as NodeJS.ErrnoException & { stderr?: string };
    const message =
      err.stderr?.trim() ||
      err.message ||
      "Failed to distribute terminal & Chrome windows";
    await showToast({
      style: Toast.Style.Failure,
      title: "Distribute failed",
      message,
    });
  }
}
