// URL and title rules are tested with the rules themselves, in
// extension/stale.test.js. This file covers what Raycast still decides.
import { describe, expect, test } from "bun:test";
import {
  BridgeCandidate,
  BridgePullRequest,
  fromBridge,
  selectUnattendedClose,
  verifyPullRequests,
} from "./stale-tabs";

function blank(overrides: Partial<BridgeCandidate>): BridgeCandidate {
  return {
    id: 1,
    title: "New Tab",
    url: "chrome://newtab/",
    group_id: 2,
    group_title: "test",
    kind: "blank-tab",
    reason: "Empty new tab",
    verified: true,
    safe_for_bulk_close: true,
    display_title: "New Tab",
    display_url: "chrome://newtab/",
    ...overrides,
  };
}

function pr(id: number, number: number): BridgePullRequest {
  return {
    id,
    title: `PR ${number}`,
    url: `https://github.com/o/r/pull/${number}`,
    group_id: 2,
    group_title: "test",
    owner: "o",
    repo: "r",
    number,
  };
}

describe("fromBridge", () => {
  test("maps the wire names to the Raycast names", () => {
    const c = fromBridge(blank({ display_title: "shown" }));
    expect(c.safeForBulkClose).toBe(true);
    expect(c.displayTitle).toBe("shown");
    expect(c.displayUrl).toBe("chrome://newtab/");
  });
});

describe("verifyPullRequests", () => {
  test("keeps only PRs the API reports as ended", async () => {
    const scan = await verifyPullRequests(
      {
        candidates: [blank({ id: 9 })],
        pull_requests: [pr(1, 10), pr(2, 11), pr(3, 12)],
      },
      async () =>
        new Map([
          ["o/r#10", { state: "MERGED", merged: true }],
          ["o/r#11", { state: "OPEN", merged: false }],
          ["o/r#12", { state: "CLOSED", merged: false }],
        ]),
    );
    expect(scan.candidates.map((c) => [c.id, c.kind])).toEqual([
      [9, "blank-tab"],
      [1, "github-pr-merged"],
      [3, "github-pr-closed"],
    ]);
    expect(scan.githubError).toBeUndefined();
  });

  test("a failed lookup keeps every PR open and reports why", async () => {
    const scan = await verifyPullRequests(
      { candidates: [], pull_requests: [pr(1, 10)] },
      async () => {
        throw new Error("gh: not logged in");
      },
    );
    expect(scan.candidates).toEqual([]);
    expect(scan.githubError).toBe("gh: not logged in");
  });
});

describe("selectUnattendedClose", () => {
  const now = 10 * 60_000;

  test("closes only idle, unprotected, bulk-safe tabs", () => {
    const idle = fromBridge(blank({ id: 1, last_accessed: 0 }));
    const recent = fromBridge(blank({ id: 2, last_accessed: now - 60_000 }));
    const unknown = fromBridge(blank({ id: 3 }));
    const active = fromBridge(blank({ id: 4, last_accessed: 0, active: true }));
    const pinned = fromBridge(blank({ id: 5, last_accessed: 0, pinned: true }));
    const failed = fromBridge(
      blank({
        id: 6,
        last_accessed: 0,
        kind: "oauth-error",
        safe_for_bulk_close: false,
      }),
    );
    expect(
      selectUnattendedClose(
        [idle, recent, unknown, active, pinned, failed],
        5 * 60_000,
        now,
      ).map((candidate) => candidate.id),
    ).toEqual([1]);
  });
});
