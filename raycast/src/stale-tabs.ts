import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { Tab } from "./bridge";

const execFileAsync = promisify(execFile);
const GH_BIN = process.env.GH_BIN ?? "/opt/homebrew/bin/gh";

export type CandidateKind =
  | "github-pr-merged"
  | "github-pr-closed"
  | "oauth-complete"
  | "oauth-error"
  | "app-handoff"
  | "blank-tab"
  | "page-error";

export interface CleanupCandidate extends Tab {
  kind: CandidateKind;
  reason: string;
  verified: boolean;
  safeForBulkClose: boolean;
  displayTitle: string;
  displayUrl: string;
}

interface PullRequestRef {
  owner: string;
  repo: string;
  number: number;
}

interface PullRequestState {
  state: "OPEN" | "CLOSED" | "MERGED";
  merged: boolean;
}

export function parsePullRequestUrl(rawUrl: string): PullRequestRef | null {
  let url: URL;
  try {
    url = new URL(rawUrl);
  } catch {
    return null;
  }
  if (url.hostname !== "github.com") return null;

  const match = url.pathname.match(/^\/([^/]+)\/([^/]+)\/pull\/(\d+)(?:\/|$)/);
  if (!match) return null;
  return { owner: match[1], repo: match[2], number: Number(match[3]) };
}

export function classifyTerminalPage(tab: Tab): CleanupCandidate | null {
  let url: URL;
  try {
    url = new URL(tab.url);
  } catch {
    return null;
  }

  if (
    url.hostname === "claude.ai" &&
    url.pathname === "/settings/mcp/auth_done"
  ) {
    return candidate(
      tab,
      "oauth-complete",
      "MCP authentication completed",
      true,
    );
  }

  if (
    (url.hostname === "127.0.0.1" || url.hostname === "localhost") &&
    /(^|\/)callback\/?$/.test(url.pathname) &&
    (url.searchParams.has("code") || url.searchParams.has("error"))
  ) {
    const failed = url.searchParams.has("error");
    return candidate(
      tab,
      failed ? "oauth-error" : "oauth-complete",
      failed ? "OAuth callback returned an error" : "OAuth callback completed",
      !failed,
    );
  }

  // Okta lands here after an SP-initiated SSO finishes; the user never opens
  // the dashboard on purpose, so the tab is leftover from a login.
  if (
    url.hostname === "sendbird.okta.com" &&
    url.pathname === "/app/UserHome" &&
    url.searchParams.get("session_hint") === "AUTHENTICATED"
  ) {
    return candidate(
      tab,
      "oauth-complete",
      "Okta returned to the authenticated app dashboard",
      true,
    );
  }

  // `aws sso login` opens the portal with the device-flow client in the hash.
  // A plain portal visit has no clientId, so it stays out of cleanup.
  if (
    url.hostname.endsWith(".awsapps.com") &&
    url.pathname === "/start/" &&
    new URLSearchParams(url.hash.replace(/^#\/?\??/, "")).has("clientId")
  ) {
    return candidate(
      tab,
      "oauth-complete",
      "AWS SSO device authorization page",
      true,
    );
  }

  if (
    url.hostname.endsWith(".twingate.com") &&
    url.pathname === "/client-auth-status"
  ) {
    return candidate(
      tab,
      "oauth-complete",
      "Twingate client authentication finished",
      true,
    );
  }

  // Slack permalinks hand off to the desktop app and leave this interstitial.
  if (
    url.hostname.endsWith(".slack.com") &&
    tab.title.startsWith("Redirecting")
  ) {
    return candidate(
      tab,
      "app-handoff",
      "Slack opened the link in the desktop app",
      true,
    );
  }

  if (
    tab.url === "chrome://newtab/" ||
    tab.url === "about:blank" ||
    url.hostname === "newtab"
  ) {
    return candidate(tab, "blank-tab", "Empty new tab", true);
  }

  if (
    url.hostname === "github.com" &&
    tab.title === "Page not found · GitHub"
  ) {
    return candidate(
      tab,
      "page-error",
      "GitHub reports that the page does not exist",
      false,
    );
  }

  return null;
}

function candidate(
  tab: Tab,
  kind: CandidateKind,
  reason: string,
  safeForBulkClose: boolean,
): CleanupCandidate {
  return {
    ...tab,
    kind,
    reason,
    verified: true,
    safeForBulkClose,
    displayTitle: redactTitle(tab),
    displayUrl: redactUrl(tab.url),
  };
}

function redactTitle(tab: Tab): string {
  try {
    const url = new URL(tab.url);
    if (
      (url.hostname === "127.0.0.1" || url.hostname === "localhost") &&
      /(^|\/)callback\/?$/.test(url.pathname)
    ) {
      return `${url.host}${url.pathname}`;
    }
  } catch {
    // Keep the original title for malformed URLs.
  }
  return tab.title;
}

function redactUrl(rawUrl: string): string {
  try {
    const url = new URL(rawUrl);
    if (
      (url.hostname === "127.0.0.1" || url.hostname === "localhost") &&
      /(^|\/)callback\/?$/.test(url.pathname)
    ) {
      return `${url.origin}${url.pathname}`;
    }
    return rawUrl;
  } catch {
    return rawUrl;
  }
}

function pullRequestKey(ref: PullRequestRef): string {
  return `${ref.owner}/${ref.repo}#${ref.number}`;
}

function graphqlString(value: string): string {
  return JSON.stringify(value);
}

async function getPullRequestStates(
  refs: PullRequestRef[],
): Promise<Map<string, PullRequestState>> {
  const unique = [
    ...new Map(refs.map((ref) => [pullRequestKey(ref), ref])).values(),
  ];
  if (unique.length === 0) return new Map();

  const fields = unique
    .map(
      (ref, index) =>
        `p${index}: repository(owner: ${graphqlString(ref.owner)}, name: ${graphqlString(ref.repo)}) { pullRequest(number: ${ref.number}) { state merged } }`,
    )
    .join("\n");
  const query = `query CleanupPullRequests {\n${fields}\n}`;
  const { stdout } = await execFileAsync(
    GH_BIN,
    ["api", "graphql", "-f", `query=${query}`],
    { maxBuffer: 4 * 1024 * 1024 },
  );
  const response = JSON.parse(stdout) as {
    data?: Record<string, { pullRequest: PullRequestState | null } | null>;
  };

  const states = new Map<string, PullRequestState>();
  unique.forEach((ref, index) => {
    const state = response.data?.[`p${index}`]?.pullRequest;
    if (state) states.set(pullRequestKey(ref), state);
  });
  return states;
}

export interface CleanupScan {
  candidates: CleanupCandidate[];
  githubError?: string;
}

export async function findCleanupCandidates(tabs: Tab[]): Promise<CleanupScan> {
  const refs = tabs
    .map((tab) => parsePullRequestUrl(tab.url))
    .filter((ref): ref is PullRequestRef => ref !== null);
  let states = new Map<string, PullRequestState>();
  let githubError: string | undefined;
  try {
    states = await getPullRequestStates(refs);
  } catch (error) {
    githubError = error instanceof Error ? error.message : String(error);
  }
  const candidates: CleanupCandidate[] = [];

  for (const tab of tabs) {
    const terminal = classifyTerminalPage(tab);
    if (terminal) {
      candidates.push(terminal);
      continue;
    }

    const ref = parsePullRequestUrl(tab.url);
    if (!ref) continue;
    const state = states.get(pullRequestKey(ref));
    if (!state || state.state === "OPEN") continue;
    const merged = state.merged || state.state === "MERGED";
    candidates.push({
      ...tab,
      kind: merged ? "github-pr-merged" : "github-pr-closed",
      reason: merged
        ? "GitHub API verified that the PR is merged"
        : "GitHub API verified that the PR is closed",
      verified: true,
      safeForBulkClose: true,
      displayTitle: tab.title,
      displayUrl: tab.url,
    });
  }

  return { candidates, githubError };
}

// The periodic cleanup runs with nobody confirming, so on top of the bulk-close
// rules it only takes tabs the user has not touched for minIdleMs. A tab whose
// last access time is unknown is treated as recently used.
export function selectUnattendedClose(
  candidates: CleanupCandidate[],
  minIdleMs: number,
  now: number,
): CleanupCandidate[] {
  return candidates.filter(
    (candidate) =>
      candidate.safeForBulkClose &&
      !candidate.active &&
      !candidate.pinned &&
      !candidate.audible &&
      typeof candidate.last_accessed === "number" &&
      now - candidate.last_accessed >= minIdleMs,
  );
}
