import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { bridgeRequest, Tab } from "./bridge";

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

// The extension's stale.js owns the URL and title rules, so Raycast and the
// terminal UI cannot disagree on what is stale. Its answer is snake_case;
// these are the wire shapes.
export interface BridgeCandidate extends Tab {
  kind: CandidateKind;
  reason: string;
  verified: boolean;
  safe_for_bulk_close: boolean;
  display_title: string;
  display_url: string;
}

export interface BridgePullRequest extends Tab, PullRequestRef {}

export interface BridgeStaleScan {
  candidates: BridgeCandidate[];
  pull_requests: BridgePullRequest[];
}

export function fromBridge(c: BridgeCandidate): CleanupCandidate {
  const {
    safe_for_bulk_close,
    display_title,
    display_url,
    ...rest
  } = c;
  return {
    ...rest,
    safeForBulkClose: safe_for_bulk_close,
    displayTitle: display_title,
    displayUrl: display_url,
  };
}

function pullRequestKey(ref: PullRequestRef): string {
  return `${ref.owner}/${ref.repo}#${ref.number}`;
}

function graphqlString(value: string): string {
  return JSON.stringify(value);
}

export async function getPullRequestStates(
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

export async function findCleanupCandidates(): Promise<CleanupScan> {
  const scan = await bridgeRequest<BridgeStaleScan>({
    type: "stale_candidates",
  });
  return verifyPullRequests(scan);
}

// verifyPullRequests turns the extension's scan into candidates: terminal
// pages as judged, and PR tabs only once the GitHub API says the PR ended.
export async function verifyPullRequests(
  scan: BridgeStaleScan,
  lookup: typeof getPullRequestStates = getPullRequestStates,
): Promise<CleanupScan> {
  let states = new Map<string, PullRequestState>();
  let githubError: string | undefined;
  try {
    states = await lookup(scan.pull_requests);
  } catch (error) {
    githubError = error instanceof Error ? error.message : String(error);
  }
  const candidates = scan.candidates.map(fromBridge);

  for (const pr of scan.pull_requests) {
    const state = states.get(pullRequestKey(pr));
    if (!state || state.state === "OPEN") continue;
    const merged = state.merged || state.state === "MERGED";
    const { owner: _owner, repo: _repo, number: _number, ...tab } = pr;
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
