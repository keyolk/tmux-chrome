import { describe, expect, test } from "bun:test";
import { Tab } from "./bridge";
import { classifyTerminalPage, parsePullRequestUrl } from "./stale-tabs";

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

  test("keeps Okta dashboard out of bulk cleanup", () => {
    const result = classifyTerminalPage(
      tab("https://sendbird.okta.com/app/UserHome?session_hint=AUTHENTICATED"),
    );
    expect(result?.kind).toBe("oauth-complete");
    expect(result?.safeForBulkClose).toBe(false);
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
