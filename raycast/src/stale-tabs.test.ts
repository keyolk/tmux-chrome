import { describe, expect, test } from "bun:test";
import { Tab } from "./bridge";
import {
  classifyTerminalPage,
  parsePullRequestUrl,
  selectUnattendedClose,
} from "./stale-tabs";

function tab(url: string, title = "Example"): Tab {
  return {
    id: 1,
    title,
    url,
    group_id: 2,
    group_title: "test",
  };
}

describe("parsePullRequestUrl", () => {
  test("parses PR URLs and subpages", () => {
    expect(
      parsePullRequestUrl(
        "https://github.com/sendbird/ops-k8s/pull/9413/files",
      ),
    ).toEqual({ owner: "sendbird", repo: "ops-k8s", number: 9413 });
  });

  test("rejects lists, malformed paths, and other hosts", () => {
    expect(
      parsePullRequestUrl("https://github.com/sendbird/ops-k8s/pulls"),
    ).toBeNull();
    expect(
      parsePullRequestUrl("https://github.com/sendbird/ops-k8s/pull/nope"),
    ).toBeNull();
    expect(parsePullRequestUrl("https://example.com/a/b/pull/1")).toBeNull();
  });
});

describe("classifyTerminalPage", () => {
  test("classifies exact MCP completion URL", () => {
    const result = classifyTerminalPage(
      tab("https://claude.ai/settings/mcp/auth_done?authed_mcp_server_id=abc"),
    );
    expect(result?.kind).toBe("oauth-complete");
    expect(result?.safeForBulkClose).toBe(true);
  });

  test("redacts OAuth callback credentials from title and URL", () => {
    const result = classifyTerminalPage(
      tab(
        "http://127.0.0.1:51200/callback?code=secret&state=csrf",
        "127.0.0.1:51200/callback?code=secret&state=csrf",
      ),
    );
    expect(result?.displayTitle).toBe("127.0.0.1:51200/callback");
    expect(result?.displayUrl).toBe("http://127.0.0.1:51200/callback");
  });

  test("classifies callback errors", () => {
    const result = classifyTerminalPage(
      tab("http://localhost:3000/oauth/callback?error=access_denied"),
    );
    expect(result?.kind).toBe("oauth-error");
    expect(result?.safeForBulkClose).toBe(false);
  });

  test("closes the Okta dashboard left after SSO", () => {
    const result = classifyTerminalPage(
      tab("https://sendbird.okta.com/app/UserHome?session_hint=AUTHENTICATED"),
    );
    expect(result?.kind).toBe("oauth-complete");
    expect(result?.safeForBulkClose).toBe(true);
  });

  test("classifies AWS SSO device authorization and Twingate auth", () => {
    expect(
      classifyTerminalPage(
        tab(
          "https://d-123.awsapps.com/start/#/?clientId=abc&clientType=cHVibGlj&deviceContextId=x",
        ),
      )?.kind,
    ).toBe("oauth-complete");
    expect(
      classifyTerminalPage(
        tab("https://sendbird.us1.twingate.com/client-auth-status"),
      )?.kind,
    ).toBe("oauth-complete");
  });

  test("classifies Slack desktop handoff interstitials", () => {
    const result = classifyTerminalPage(
      tab(
        "https://sendbird.slack.com/archives/C1/p123",
        "Redirecting… | Slack",
      ),
    );
    expect(result?.kind).toBe("app-handoff");
    expect(result?.safeForBulkClose).toBe(true);
    expect(
      classifyTerminalPage(
        tab("https://sendbird.slack.com/archives/C1/p123", "general | Slack"),
      ),
    ).toBeNull();
  });

  test("classifies empty new tabs", () => {
    expect(classifyTerminalPage(tab("chrome://newtab/"))?.kind).toBe(
      "blank-tab",
    );
    expect(classifyTerminalPage(tab("about:blank"))?.kind).toBe("blank-tab");
  });

  test("does not classify login and AWS portal pages", () => {
    expect(
      classifyTerminalPage(tab("https://sendbird.okta.com/login/login.htm")),
    ).toBeNull();
    expect(
      classifyTerminalPage(tab("https://example.awsapps.com/start/#/")),
    ).toBeNull();
  });
});

describe("selectUnattendedClose", () => {
  const now = 10 * 60_000;
  function blank(overrides: Partial<Tab>) {
    const result = classifyTerminalPage({ ...tab("chrome://newtab/"), ...overrides });
    if (!result) throw new Error("expected a candidate");
    return result;
  }

  test("closes only idle, unprotected, bulk-safe tabs", () => {
    const idle = blank({ id: 1, last_accessed: 0 });
    const recent = blank({ id: 2, last_accessed: now - 60_000 });
    const unknown = blank({ id: 3 });
    const active = blank({ id: 4, last_accessed: 0, active: true });
    const pinned = blank({ id: 5, last_accessed: 0, pinned: true });
    const okta = classifyTerminalPage({
      ...tab("http://localhost:3000/oauth/callback?error=denied"),
      last_accessed: 0,
    });
    expect(
      selectUnattendedClose(
        [idle, recent, unknown, active, pinned, okta!],
        5 * 60_000,
        now,
      ).map((candidate) => candidate.id),
    ).toEqual([1]);
  });
});
