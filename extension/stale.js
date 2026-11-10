// Stale-tab rules, the one copy. The service worker loads this with
// importScripts and answers `stale_candidates` with it; Raycast and the
// terminal UI both ask the bridge instead of keeping their own copy, which is
// how the two used to drift apart.
//
// Only URL and title rules live here. Whether a pull request has ended needs
// the GitHub API through the user's `gh` login, which a service worker does
// not have, so this file returns the PR references and each client verifies
// them itself.
//
// Plain script, no import/export: importScripts cannot load a module, and the
// `module.exports` branch lets node's test runner require the same file.

(function (root) {
  function parsePullRequestUrl(rawUrl) {
    let url;
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

  function isLocalCallback(url) {
    return (
      (url.hostname === "127.0.0.1" || url.hostname === "localhost") &&
      /(^|\/)callback\/?$/.test(url.pathname)
    );
  }

  // OAuth callbacks carry the authorization code in the query; the display
  // copies drop it so it is never shown or logged by a client.
  function redactTitle(tab) {
    try {
      const url = new URL(tab.url);
      if (isLocalCallback(url)) return `${url.host}${url.pathname}`;
    } catch {
      // Keep the original title for malformed URLs.
    }
    return tab.title;
  }

  function redactUrl(rawUrl) {
    try {
      const url = new URL(rawUrl);
      if (isLocalCallback(url)) return `${url.origin}${url.pathname}`;
      return rawUrl;
    } catch {
      return rawUrl;
    }
  }

  function candidate(tab, kind, reason, safeForBulkClose) {
    return {
      ...tab,
      kind,
      reason,
      verified: true,
      safe_for_bulk_close: safeForBulkClose,
      display_title: redactTitle(tab),
      display_url: redactUrl(tab.url),
    };
  }

  function classifyTerminalPage(tab) {
    let url;
    try {
      url = new URL(tab.url);
    } catch {
      return null;
    }

    if (url.hostname === "claude.ai" && url.pathname === "/settings/mcp/auth_done") {
      return candidate(tab, "oauth-complete", "MCP authentication completed", true);
    }

    if (
      isLocalCallback(url) &&
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
      return candidate(tab, "oauth-complete", "Okta returned to the authenticated app dashboard", true);
    }

    // `aws sso login` opens the portal with the device-flow client in the hash.
    // A plain portal visit has no clientId, so it stays out of cleanup.
    if (
      url.hostname.endsWith(".awsapps.com") &&
      url.pathname === "/start/" &&
      new URLSearchParams(url.hash.replace(/^#\/?\??/, "")).has("clientId")
    ) {
      return candidate(tab, "oauth-complete", "AWS SSO device authorization page", true);
    }

    if (url.hostname.endsWith(".twingate.com") && url.pathname === "/client-auth-status") {
      return candidate(tab, "oauth-complete", "Twingate client authentication finished", true);
    }

    // Slack permalinks hand off to the desktop app and leave this interstitial.
    if (url.hostname.endsWith(".slack.com") && (tab.title || "").startsWith("Redirecting")) {
      return candidate(tab, "app-handoff", "Slack opened the link in the desktop app", true);
    }

    if (tab.url === "chrome://newtab/" || tab.url === "about:blank" || url.hostname === "newtab") {
      return candidate(tab, "blank-tab", "Empty new tab", true);
    }

    if (url.hostname === "github.com" && tab.title === "Page not found · GitHub") {
      return candidate(tab, "page-error", "GitHub reports that the page does not exist", false);
    }

    return null;
  }

  // scanTabs splits tabs into pages this file can judge alone and PR tabs the
  // caller must verify. A tab is in at most one of the two lists.
  function scanTabs(tabs) {
    const candidates = [];
    const pullRequests = [];
    for (const tab of tabs) {
      const terminal = classifyTerminalPage(tab);
      if (terminal) {
        candidates.push(terminal);
        continue;
      }
      const ref = parsePullRequestUrl(tab.url);
      if (ref) pullRequests.push({ ...tab, ...ref });
    }
    return { candidates, pull_requests: pullRequests };
  }

  const api = { parsePullRequestUrl, classifyTerminalPage, scanTabs };
  if (typeof module === "object" && module.exports) {
    module.exports = api;
  } else {
    root.TmuxChromeStale = api;
  }
})(typeof self !== "undefined" ? self : globalThis);
