import {
  environment,
  getPreferenceValues,
  LaunchType,
  showHUD,
} from "@raycast/api";
import { cleanTabs, closeTabs, getCleanupTabs } from "./bridge";
import { findCleanupCandidates, selectUnattendedClose } from "./stale-tabs";

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

// Runs on the interval in package.json. Stale tabs close first so the sweep
// does not file a blank or redirect tab into the native group just before it
// would have been closed.
export default async function AutoCleanTabs() {
  const minIdleMs = idleMs();
  const background = environment.launchType === LaunchType.Background;

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
    // Stay silent on the interval; a HUD every few minutes would interrupt.
    if (!background) {
      await showHUD(`Closed ${closed} stale · swept ${swept} ungrouped`);
    }
  } catch (error) {
    console.error("auto-clean failed", error);
    if (!background) await showHUD(`Auto clean failed: ${String(error)}`);
  }
}
