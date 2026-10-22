import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { getPreferenceValues } from "@raycast/api";
import { cleanTabs, closeTabs, getCleanupTabs } from "./bridge";
import { findCleanupCandidates, selectUnattendedClose } from "./stale-tabs";

const execFileAsync = promisify(execFile);

interface Preferences {
  idleMinutes: string;
}

const DEFAULT_IDLE_MINUTES = 30;

function idleMs(): number {
  const minutes = Number(getPreferenceValues<Preferences>().idleMinutes);
  return (
    (Number.isFinite(minutes) && minutes >= 0 ? minutes : DEFAULT_IDLE_MINUTES) *
    60_000
  );
}

// A macOS notification, not a Raycast HUD or toast: it shows the result
// without taking keyboard focus from the terminal. Values go in as argv so
// tab counts and error text never become AppleScript source.
async function notify(message: string): Promise<void> {
  try {
    await execFileAsync("/usr/bin/osascript", [
      "-e",
      "on run argv",
      "-e",
      'display notification (item 1 of argv) with title "Auto Clean Tabs"',
      "-e",
      "end run",
      message,
    ]);
  } catch (error) {
    console.error("auto-clean: notification failed", error);
  }
}

// Runs on the interval in package.json. Stale tabs close first so the sweep
// does not file a blank or redirect tab into the native group just before it
// would have been closed.
export default async function AutoCleanTabs() {
  const minIdleMs = idleMs();

  try {
    const scan = await findCleanupCandidates(await getCleanupTabs());
    const targets = selectUnattendedClose(
      scan.candidates,
      minIdleMs,
      Date.now(),
    );
    const closed =
      targets.length === 0
        ? 0
        : (
            await closeTabs(
              targets.map((candidate) => ({
                tab_id: candidate.id,
                expected_url: candidate.url,
              })),
            )
          ).closed.length;
    const swept = (await cleanTabs(minIdleMs)).moved ?? 0;

    console.log(
      `auto-clean: closed=${closed} swept=${swept}` +
        (scan.githubError ? ` github_error=${scan.githubError}` : ""),
    );
    // Quiet runs stay quiet; notify only when tabs actually changed.
    if (closed + swept > 0) {
      await notify(`Closed ${closed} stale · moved ${swept} ungrouped`);
    }
  } catch (error) {
    console.error("auto-clean failed", error);
    await notify(`Failed: ${String(error)}`);
  }
}
