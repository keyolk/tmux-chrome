import { Alert, confirmAlert, showHUD, showToast, Toast } from "@raycast/api";
import { closeTabs, getAllTabs } from "./bridge";
import { CleanupCandidate, findCleanupCandidates } from "./stale-tabs";

function isProtected(candidate: CleanupCandidate): boolean {
  return Boolean(candidate.active || candidate.pinned || candidate.audible);
}

function closable(candidates: CleanupCandidate[]): CleanupCandidate[] {
  return candidates.filter(
    (candidate) => candidate.safeForBulkClose && !isProtected(candidate),
  );
}

export default async function CleanStaleTabs() {
  const initial = await findCleanupCandidates(await getAllTabs());
  const candidates = closable(initial.candidates);
  if (candidates.length === 0) {
    await showHUD(
      initial.githubError
        ? "No safe stale tabs found; GitHub verification failed"
        : "No stale tabs found",
    );
    return;
  }

  const groups = new Set(candidates.map((candidate) => candidate.group_title));
  const githubCount = candidates.filter((candidate) =>
    candidate.kind.startsWith("github-pr-"),
  ).length;
  const authCount = candidates.length - githubCount;
  const details = [
    githubCount
      ? `${githubCount} ended PR tab${githubCount === 1 ? "" : "s"}`
      : null,
    authCount
      ? `${authCount} completed auth tab${authCount === 1 ? "" : "s"}`
      : null,
    `${groups.size} tab group${groups.size === 1 ? "" : "s"}`,
  ]
    .filter(Boolean)
    .join(" · ");

  if (
    !(await confirmAlert({
      title: `Close all ${candidates.length} stale tabs?`,
      message: `${details}\n\nOpen PRs, failed authentication, and currently active, pinned, audible, or unverified tabs are excluded. Chrome rechecks tab state and URL immediately before each close.`,
      primaryAction: {
        title: `Close All ${candidates.length}`,
        style: Alert.ActionStyle.Destructive,
      },
      dismissAction: { title: "Cancel" },
    }))
  ) {
    return;
  }

  const fresh = await findCleanupCandidates(await getAllTabs());
  const freshById = new Map(
    closable(fresh.candidates).map((candidate) => [candidate.id, candidate]),
  );
  const revalidated = candidates.filter((candidate) => {
    const current = freshById.get(candidate.id);
    return current?.url === candidate.url && current.kind === candidate.kind;
  });
  if (revalidated.length === 0) {
    await showHUD("No stale tabs remained after revalidation");
    return;
  }

  const result = await closeTabs(
    revalidated.map((candidate) => ({
      tab_id: candidate.id,
      expected_url: candidate.url,
    })),
  );
  if (result.skipped.length > 0) {
    await showToast({
      style: Toast.Style.Failure,
      title: `Closed ${result.closed.length} stale tabs`,
      message: `${result.skipped.length} changed or protected tabs were skipped`,
    });
    return;
  }

  await showHUD(`Closed ${result.closed.length} stale tabs`);
}
