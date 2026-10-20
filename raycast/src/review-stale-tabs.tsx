import { useEffect, useState } from "react";
import {
  Action,
  ActionPanel,
  Alert,
  Clipboard,
  Color,
  confirmAlert,
  Icon,
  List,
  showToast,
  Toast,
} from "@raycast/api";
import { closeTabs, focusTab, getAllTabs, raiseTabWindow } from "./bridge";
import { CleanupCandidate, findCleanupCandidates } from "./stale-tabs";

function kindLabel(candidate: CleanupCandidate): string {
  switch (candidate.kind) {
    case "github-pr-merged":
      return "Merged PR";
    case "github-pr-closed":
      return "Closed PR";
    case "oauth-complete":
      return "Auth complete";
    case "oauth-error":
      return "Auth error";
    case "page-error":
      return "Page error";
  }
}

function kindColor(candidate: CleanupCandidate): Color {
  switch (candidate.kind) {
    case "github-pr-merged":
      return Color.Purple;
    case "github-pr-closed":
      return Color.Red;
    case "oauth-complete":
      return Color.Green;
    case "oauth-error":
    case "page-error":
      return Color.Orange;
  }
}

function isProtected(candidate: CleanupCandidate): boolean {
  return Boolean(candidate.active || candidate.pinned || candidate.audible);
}

function protectionReason(candidate: CleanupCandidate): string | null {
  if (candidate.active) return "active";
  if (candidate.pinned) return "pinned";
  if (candidate.audible) return "playing audio";
  return null;
}

export default function ReviewStaleTabs() {
  const [candidates, setCandidates] = useState<CleanupCandidate[] | null>(null);
  const [githubError, setGithubError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setCandidates(null);
    setGithubError(null);
    setError(null);
    try {
      const tabs = await getAllTabs();
      const scan = await findCleanupCandidates(tabs);
      setCandidates(scan.candidates);
      setGithubError(scan.githubError ?? null);
    } catch (loadError) {
      setError(
        loadError instanceof Error ? loadError.message : String(loadError),
      );
    }
  }

  useEffect(() => {
    void load();
  }, []);

  async function revalidate(
    selected: CleanupCandidate[],
  ): Promise<CleanupCandidate[]> {
    const fresh = await findCleanupCandidates(await getAllTabs());
    return selected.filter((candidate) =>
      fresh.candidates.some(
        (current) =>
          current.id === candidate.id &&
          current.url === candidate.url &&
          current.kind === candidate.kind &&
          !isProtected(current),
      ),
    );
  }

  async function closeOne(candidate: CleanupCandidate) {
    if (isProtected(candidate)) return;
    if (
      !(await confirmAlert({
        title: "Close this stale tab?",
        message: `${candidate.displayTitle}\n\n${candidate.reason}\n\n${candidate.displayUrl}`,
        primaryAction: {
          title: "Close Tab",
          style: Alert.ActionStyle.Destructive,
        },
        dismissAction: { title: "Cancel" },
      }))
    ) {
      return;
    }

    const fresh = await revalidate([candidate]);
    if (fresh.length === 0) {
      await showToast({
        style: Toast.Style.Failure,
        title: "Tab changed during review",
        message: "Refresh the list before trying again.",
      });
      await load();
      return;
    }
    const result = await closeTabs([
      { tab_id: candidate.id, expected_url: candidate.url },
    ]);
    if (result.closed.length === 1) {
      await showToast({
        style: Toast.Style.Success,
        title: "Closed stale tab",
        message: candidate.displayTitle,
      });
    } else {
      await showToast({
        style: Toast.Style.Failure,
        title: "Tab was not closed",
        message: result.skipped[0]?.reason ?? "Chrome rejected the request",
      });
    }
    await load();
  }

  async function closeVerified() {
    const closable =
      candidates?.filter(
        (candidate) => candidate.safeForBulkClose && !isProtected(candidate),
      ) ?? [];
    if (closable.length === 0) return;
    if (
      !(await confirmAlert({
        title: `Close ${closable.length} verified stale tabs?`,
        message:
          "Only API-verified ended PRs and exact OAuth completion URLs are included. Active, pinned, audible, and heuristic candidates are excluded. Each tab URL is checked again before Chrome closes it.",
        primaryAction: {
          title: "Close Verified Tabs",
          style: Alert.ActionStyle.Destructive,
        },
        dismissAction: { title: "Cancel" },
      }))
    ) {
      return;
    }

    const fresh = await revalidate(closable);
    if (fresh.length === 0) {
      await showToast({
        style: Toast.Style.Failure,
        title: "No candidates remained after revalidation",
      });
      await load();
      return;
    }
    const result = await closeTabs(
      fresh.map((candidate) => ({
        tab_id: candidate.id,
        expected_url: candidate.url,
      })),
    );
    await showToast({
      style: result.skipped.length ? Toast.Style.Failure : Toast.Style.Success,
      title: `Closed ${result.closed.length} stale tabs`,
      message: result.skipped.length
        ? `${result.skipped.length} changed or protected tabs were skipped`
        : undefined,
    });
    await load();
  }

  async function showInChrome(candidate: CleanupCandidate) {
    await focusTab(candidate.id);
    await raiseTabWindow(candidate.id);
  }

  if (error) {
    return (
      <List>
        <List.EmptyView
          icon={Icon.Plug}
          title="Could not scan Chrome tabs"
          description={error}
          actions={
            <ActionPanel>
              <Action
                title="Retry"
                icon={Icon.ArrowClockwise}
                onAction={load}
              />
            </ActionPanel>
          }
        />
      </List>
    );
  }

  const bulkCount =
    candidates?.filter(
      (candidate) => candidate.safeForBulkClose && !isProtected(candidate),
    ).length ?? 0;

  return (
    <List
      isLoading={candidates === null}
      isShowingDetail
      searchBarPlaceholder="Review merged PRs and completed authentication tabs…"
    >
      {githubError ? (
        <List.Section title="GitHub verification unavailable">
          <List.Item
            icon={{ source: Icon.ExclamationMark, tintColor: Color.Orange }}
            title="PR tabs were not included"
            subtitle="Run gh auth status and refresh"
            detail={
              <List.Item.Detail markdown={`\`\`\`\n${githubError}\n\`\`\``} />
            }
            actions={
              <ActionPanel>
                <Action
                  title="Refresh"
                  icon={Icon.ArrowClockwise}
                  onAction={load}
                />
              </ActionPanel>
            }
          />
        </List.Section>
      ) : null}
      {candidates?.length === 0 && !githubError ? (
        <List.EmptyView
          icon={Icon.Checkmark}
          title="No stale tabs found"
          description="No ended PR or completed authentication tabs matched the safe rules."
          actions={
            <ActionPanel>
              <Action
                title="Refresh"
                icon={Icon.ArrowClockwise}
                onAction={load}
              />
            </ActionPanel>
          }
        />
      ) : (
        <List.Section
          title="Review before closing"
          subtitle={`${candidates?.length ?? 0} candidates`}
        >
          {candidates?.map((candidate) => {
            const protectedBy = protectionReason(candidate);
            return (
              <List.Item
                key={candidate.id}
                icon={{
                  source: Icon.XMarkCircle,
                  tintColor: kindColor(candidate),
                }}
                title={candidate.displayTitle || candidate.displayUrl}
                subtitle={candidate.group_title}
                accessories={[
                  ...(protectedBy
                    ? [{ tag: { value: protectedBy, color: Color.Orange } }]
                    : []),
                  {
                    tag: {
                      value: kindLabel(candidate),
                      color: kindColor(candidate),
                    },
                  },
                ]}
                detail={
                  <List.Item.Detail
                    markdown={`## ${candidate.displayTitle}\n\n${candidate.reason}\n\n\`${candidate.displayUrl}\``}
                    metadata={
                      <List.Item.Detail.Metadata>
                        <List.Item.Detail.Metadata.Label
                          title="Tab Group"
                          text={candidate.group_title}
                        />
                        <List.Item.Detail.Metadata.Label
                          title="Classification"
                          text={kindLabel(candidate)}
                        />
                        <List.Item.Detail.Metadata.Label
                          title="Verification"
                          text={candidate.verified ? "Verified" : "Heuristic"}
                        />
                      </List.Item.Detail.Metadata>
                    }
                  />
                }
                actions={
                  <ActionPanel>
                    {!protectedBy ? (
                      <Action
                        title="Close This Tab"
                        icon={Icon.Trash}
                        style={Action.Style.Destructive}
                        onAction={() => closeOne(candidate)}
                      />
                    ) : null}
                    <Action
                      title="Show in Chrome"
                      icon={Icon.Eye}
                      onAction={() => showInChrome(candidate)}
                    />
                    {bulkCount > 0 ? (
                      <Action
                        title={`Close All ${bulkCount} Verified Tabs`}
                        icon={Icon.Trash}
                        style={Action.Style.Destructive}
                        shortcut={{
                          modifiers: ["cmd", "shift"],
                          key: "delete",
                        }}
                        onAction={closeVerified}
                      />
                    ) : null}
                    <Action
                      title="Copy URL"
                      icon={Icon.Clipboard}
                      onAction={() => Clipboard.copy(candidate.url)}
                    />
                    <Action
                      title="Refresh"
                      icon={Icon.ArrowClockwise}
                      shortcut={{ modifiers: ["cmd"], key: "r" }}
                      onAction={load}
                    />
                  </ActionPanel>
                }
              />
            );
          })}
        </List.Section>
      )}
    </List>
  );
}
