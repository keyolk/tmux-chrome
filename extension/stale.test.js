// Run with: node --test extension/
const { describe, test } = require("node:test");
const assert = require("node:assert/strict");
const { parsePullRequestUrl, classifyTerminalPage, scanTabs } = require("./stale.js");

function tab(url, title = "Example") {
  return { id: 1, title, url, group_id: 2, group_title: "test" };
}

describe("parsePullRequestUrl", () => {
  test("parses PR URLs and subpages", () => {
    assert.deepEqual(parsePullRequestUrl("https://github.com/sendbird/ops-k8s/pull/9413/files"), {
      owner: "sendbird",
      repo: "ops-k8s",
      number: 9413,
    });
  });

  test("rejects lists, malformed paths, and other hosts", () => {
    assert.equal(parsePullRequestUrl("https://github.com/sendbird/ops-k8s/pulls"), null);
    assert.equal(parsePullRequestUrl("https://github.com/sendbird/ops-k8s/pull/nope"), null);
    assert.equal(parsePullRequestUrl("https://example.com/a/b/pull/1"), null);
  });
});

describe("classifyTerminalPage", () => {
  test("classifies exact MCP completion URL", () => {
    const result = classifyTerminalPage(tab("https://claude.ai/settings/mcp/auth_done?authed_mcp_server_id=abc"));
    assert.equal(result?.kind, "oauth-complete");
    assert.equal(result?.safe_for_bulk_close, true);
  });

  test("redacts OAuth callback credentials from title and URL", () => {
    const result = classifyTerminalPage(
      tab("http://127.0.0.1:51200/callback?code=secret&state=csrf", "127.0.0.1:51200/callback?code=secret&state=csrf"),
    );
    assert.equal(result?.display_title, "127.0.0.1:51200/callback");
    assert.equal(result?.display_url, "http://127.0.0.1:51200/callback");
  });

  test("classifies callback errors", () => {
    const result = classifyTerminalPage(tab("http://localhost:3000/oauth/callback?error=access_denied"));
    assert.equal(result?.kind, "oauth-error");
    assert.equal(result?.safe_for_bulk_close, false);
  });

  test("closes the Okta dashboard left after SSO", () => {
    const result = classifyTerminalPage(tab("https://sendbird.okta.com/app/UserHome?session_hint=AUTHENTICATED"));
    assert.equal(result?.kind, "oauth-complete");
    assert.equal(result?.safe_for_bulk_close, true);
  });

  test("classifies AWS SSO device authorization and Twingate auth", () => {
    assert.equal(
      classifyTerminalPage(tab("https://d-123.awsapps.com/start/#/?clientId=abc&clientType=cHVibGlj&deviceContextId=x"))
        ?.kind,
      "oauth-complete",
    );
    assert.equal(
      classifyTerminalPage(tab("https://sendbird.us1.twingate.com/client-auth-status"))?.kind,
      "oauth-complete",
    );
  });

  test("classifies Slack desktop handoff interstitials", () => {
    const result = classifyTerminalPage(tab("https://sendbird.slack.com/archives/C1/p123", "Redirecting… | Slack"));
    assert.equal(result?.kind, "app-handoff");
    assert.equal(result?.safe_for_bulk_close, true);
    assert.equal(classifyTerminalPage(tab("https://sendbird.slack.com/archives/C1/p123", "general | Slack")), null);
  });

  test("classifies empty new tabs", () => {
    assert.equal(classifyTerminalPage(tab("chrome://newtab/"))?.kind, "blank-tab");
    assert.equal(classifyTerminalPage(tab("about:blank"))?.kind, "blank-tab");
  });

  test("does not classify login and AWS portal pages", () => {
    assert.equal(classifyTerminalPage(tab("https://sendbird.okta.com/login/login.htm")), null);
    assert.equal(classifyTerminalPage(tab("https://example.awsapps.com/start/#/")), null);
  });
});

describe("scanTabs", () => {
  test("splits terminal pages from PRs the caller must verify", () => {
    const scan = scanTabs([
      { ...tab("chrome://newtab/"), id: 1 },
      { ...tab("https://github.com/keyolk/tmux-chrome/pull/20"), id: 2 },
      { ...tab("https://example.com/"), id: 3 },
    ]);
    assert.deepEqual(
      scan.candidates.map((c) => c.id),
      [1],
    );
    assert.deepEqual(
      scan.pull_requests.map((p) => [p.id, p.owner, p.repo, p.number]),
      [[2, "keyolk", "tmux-chrome", 20]],
    );
  });
});
