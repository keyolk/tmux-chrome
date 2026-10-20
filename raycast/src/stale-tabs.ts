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

  if (
    url.hostname === "sendbird.okta.com" &&
    url.pathname === "/app/UserHome" &&
    url.searchParams.get("session_hint") === "AUTHENTICATED"
  ) {
    return candidate(
      tab,
      "oauth-complete",
      "Okta returned to the authenticated app dashboard",
      false,
    );
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
