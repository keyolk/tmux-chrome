// tmux Tab Group Bridge — Chrome Extension Background Service Worker
// Communicates with native messaging host to sync tab groups with tmux windows.
// RULE: Never call chrome.windows.update({focused: true}) — terminal must keep focus.

const BUILD_MARKER = "tmux-chrome-bridge:cycle-tab-2026-05-20";
console.log("[tmux-chrome] background loaded:", BUILD_MARKER);

const NATIVE_HOST = "com.tmux.chrome.bridge";
const RESERVED_GROUP_TITLE = "native";
let port = null;
let reconnectTimer = null;
let reconnectDelay = 5000; // start at 5s
const MAX_RECONNECT_DELAY = 300000; // cap at 5 minutes
let lastConnectTime = 0;

// Per-group memory of the last active tab id, so switching groups restores
// the tab the user was last on inside that group instead of always jumping
// to the first tab.
const lastActiveTabByGroup = new Map();

function rememberActiveTabForGroup(groupId, tabId) {
  if (typeof groupId === "number" && groupId >= 0 && typeof tabId === "number") {
    lastActiveTabByGroup.set(groupId, tabId);
  }
}

chrome.tabs.onActivated.addListener(async ({ tabId }) => {
  try {
    const tab = await chrome.tabs.get(tabId);
    rememberActiveTabForGroup(tab.groupId, tabId);
  } catch (_) {
    // tab gone
  }
});

chrome.tabs.onUpdated.addListener((tabId, changeInfo, tab) => {
  if (changeInfo.groupId !== undefined && tab.active) {
    rememberActiveTabForGroup(tab.groupId, tabId);
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  for (const [groupId, lastTabId] of lastActiveTabByGroup.entries()) {
    if (lastTabId === tabId) {
      lastActiveTabByGroup.delete(groupId);
    }
  }
});

function connect() {
  // Prevent multiple simultaneous connections
  if (port) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }

  const now = Date.now();
  lastConnectTime = now;

  try {
    port = chrome.runtime.connectNative(NATIVE_HOST);
  } catch (e) {
    console.error("Failed to connect to native host:", e);
    port = null;
    scheduleReconnect();
    return;
  }

  port.onMessage.addListener(async (msg) => {
    try {
      const result = await handleMessage(msg);
      sendToHost({ id: msg.id, success: true, data: result });
    } catch (e) {
      sendToHost({ id: msg.id, success: false, error: e.message });
    }
  });

  port.onDisconnect.addListener(() => {
    const err = chrome.runtime.lastError;
    console.log("Native host disconnected:", err?.message || "unknown");
    port = null;

    // If connection lasted > 30s, it was a real session — reset backoff
    if (Date.now() - lastConnectTime > 30000) {
      reconnectDelay = 5000;
    }
    scheduleReconnect();
  });

  // Connection succeeded and held — reset backoff
  console.log("Connected to native host");
  reconnectDelay = 5000;
}

function scheduleReconnect() {
  if (reconnectTimer) return; // already scheduled
  console.log(`Reconnecting in ${reconnectDelay / 1000}s...`);
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connect();
  }, reconnectDelay);
  // Exponential backoff: 5s → 10s → 20s → 40s → ... → 5min cap
  reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY);
}

function sendToHost(msg) {
  if (port) {
    try {
      port.postMessage(msg);
    } catch (e) {
      console.error("Failed to send to host:", e);
    }
  }
}

// --- Handlers ---

async function handleMessage(msg) {
  switch (msg.type) {
    case "switch_window":
      return switchWindow(msg.window_name);
    case "create_group":
      return createGroup(msg.name);
    case "delete_group":
      return deleteGroup(msg.name);
    case "list_groups":
      return listGroups();
    case "add_tab":
      return addTab(msg.name, msg.url, msg.active);
    case "move_tab":
      return moveTab(msg.name);
    case "remove_tab":
      return removeTab();
    case "get_tabs":
      return getTabs(msg.name, msg.all, msg.active);
    case "focus_tab":
      return focusTab(msg.tab_id);
    case "raise_group_window":
      return raiseGroupWindow(msg.name);
    case "raise_tab_window":
      return raiseTabWindow(msg.tab_id);
    case "rename_group":
      return renameGroup(msg.old_name, msg.new_name);
    case "clean_tabs":
      return cleanTabs();
    case "merge_windows":
      return mergeWindows();
    case "cycle_tab_in_group":
      return cycleTabInGroup(msg.direction);
    default:
      throw new Error(`Unknown message type: ${msg.type}`);
  }
}

async function findGroupByTitle(title) {
  if (typeof title !== "string" || title.length === 0) return undefined;
  const lower = title.toLowerCase();
  const groups = await chrome.tabGroups.query({});
  return groups.find((g) => typeof g.title === "string" && g.title.toLowerCase() === lower);
}

async function collapseAllExcept(expandGroupId) {
  const groups = await chrome.tabGroups.query({});
  const promises = groups.map((g) =>
    chrome.tabGroups.update(g.id, { collapsed: g.id !== expandGroupId })
  );
  await Promise.all(promises);
}

async function switchWindow(windowName) {
  const group = await findGroupByTitle(windowName);
  if (!group) return { matched: false };

  await collapseAllExcept(group.id);

  const tabs = await chrome.tabs.query({ groupId: group.id });
  if (tabs.length === 0) {
    return { matched: true, group_id: group.id };
  }

  const remembered = lastActiveTabByGroup.get(group.id);
  const target = tabs.find((t) => t.id === remembered) ?? tabs[0];

  await chrome.tabs.update(target.id, { active: true });
  rememberActiveTabForGroup(group.id, target.id);

  return { matched: true, group_id: group.id, tab_id: target.id };
}

async function ensureReservedNativeGroup() {
  let group = await findGroupByTitle(RESERVED_GROUP_TITLE);
  if (group) return group;

  const tab = await chrome.tabs.create({ active: false });
  const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
  await chrome.tabGroups.update(groupId, {
    title: RESERVED_GROUP_TITLE,
    collapsed: true,
  });
  return chrome.tabGroups.get(groupId);
}

async function createGroup(name) {
  // Create a blank tab and group it
  const tab = await chrome.tabs.create({ active: false });
  const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
  await chrome.tabGroups.update(groupId, { title: name, collapsed: false });
  await collapseAllExcept(groupId);
  return { group_id: groupId };
}

async function deleteGroup(name) {
  if (name && name.toLowerCase() === RESERVED_GROUP_TITLE) {
    throw new Error(`Tab group "${RESERVED_GROUP_TITLE}" is reserved and cannot be deleted`);
  }

  const group = await findGroupByTitle(name);
  if (!group) throw new Error(`Tab group "${name}" not found`);

  // Ungroup all tabs in this group
  const tabs = await chrome.tabs.query({ groupId: group.id });
  if (tabs.length > 0) {
    await chrome.tabs.ungroup(tabs.map((t) => t.id));
  }
  return { ungrouped: tabs.length };
}

async function listGroups() {
  const groups = await chrome.tabGroups.query({});
  const result = [];
  for (const g of groups) {
    const tabs = await chrome.tabs.query({ groupId: g.id });
    result.push({
      id: g.id,
      title: g.title || "(untitled)",
      color: g.color,
      collapsed: g.collapsed,
      tab_count: tabs.length,
    });
  }
  return result;
}

async function addTab(groupName, url, active = false) {
  let group = await findGroupByTitle(groupName);

  let result;
  // Create group if it doesn't exist
  if (!group) {
    const tab = await chrome.tabs.create({ url, active });
    const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
    await chrome.tabGroups.update(groupId, {
      title: groupName,
      collapsed: false,
    });
    await collapseAllExcept(groupId);
    result = { group_id: groupId, tab_id: tab.id, window_id: tab.windowId, created_group: true };
  } else {
    // Add tab to existing group, in the same window the group lives in
    const tab = await chrome.tabs.create({ url, active, windowId: group.windowId });
    await chrome.tabs.group({ tabIds: [tab.id], groupId: group.id });
    await collapseAllExcept(group.id);
    result = { group_id: group.id, tab_id: tab.id, window_id: group.windowId, created_group: false };
  }

  // If the caller wants the new tab active, also raise the owning Chrome window
  // within Chrome. OS-level app activation is handled by the CLI caller.
  if (active && result.window_id != null) {
    try {
      await chrome.windows.update(result.window_id, { focused: true });
    } catch (_) {
      // best-effort
    }
  }
  return result;
}

async function moveTab(groupName) {
  let group = await findGroupByTitle(groupName);

  // Get active tab in the focused Chrome window
  const [activeTab] = await chrome.tabs.query({
    active: true,
    lastFocusedWindow: true,
  });
  if (!activeTab) throw new Error("No active tab found");

  if (!group) {
    // Create group with the active tab
    const groupId = await chrome.tabs.group({ tabIds: [activeTab.id] });
    await chrome.tabGroups.update(groupId, {
      title: groupName,
      collapsed: false,
    });
    await collapseAllExcept(groupId);
    return { group_id: groupId, created_group: true };
  }

  await chrome.tabs.group({ tabIds: [activeTab.id], groupId: group.id });
  await collapseAllExcept(group.id);
  return { group_id: group.id, created_group: false };
}

async function removeTab() {
  const [activeTab] = await chrome.tabs.query({
    active: true,
    lastFocusedWindow: true,
  });
  if (!activeTab) throw new Error("No active tab found");
  if (activeTab.groupId === chrome.tabGroups.TAB_GROUP_ID_NONE) {
    throw new Error("Active tab is not in a group");
  }

  await chrome.tabs.ungroup([activeTab.id]);
  return { tab_id: activeTab.id };
}

async function getActiveGroup() {
  // Prefer the currently focused (last-focused) Chrome window when available.
  let chromeWindow = null;
  try {
    chromeWindow = await chrome.windows.getLastFocused({ windowTypes: ["normal"] });
  } catch (_) {
    chromeWindow = null;
  }

  let activeTab = null;
  if (chromeWindow) {
    const [tab] = await chrome.tabs.query({ active: true, windowId: chromeWindow.id });
    activeTab = tab ?? null;
  }
  if (!activeTab) {
    const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
    activeTab = tab ?? null;
  }
  if (!activeTab) return null;

  const groupId = activeTab.groupId;
  if (groupId === undefined || groupId === chrome.tabGroups.TAB_GROUP_ID_NONE) {
    return null;
  }

  const group = await chrome.tabGroups.get(groupId);
  return group ?? null;
}

async function getTabs(groupName, all, active) {
  let tabs;
  let group = null;
  if (all) {
    // All tabs across all groups
    const groups = await chrome.tabGroups.query({});
    tabs = [];
    for (const g of groups) {
      const groupTabs = await chrome.tabs.query({ groupId: g.id });
      for (const t of groupTabs) {
        tabs.push({
          id: t.id,
          title: t.title,
          url: t.url,
          group_title: g.title || "(untitled)",
          group_id: g.id,
        });
      }
    }
    return tabs;
  }

  if (active) {
    group = await getActiveGroup();
    if (!group) throw new Error("No active tab group");
  } else {
    group = await findGroupByTitle(groupName);
    if (!group) throw new Error(`Tab group "${groupName}" not found`);
  }

  const groupTabs = await chrome.tabs.query({ groupId: group.id });
  tabs = groupTabs.map((t) => ({
    id: t.id,
    title: t.title,
    url: t.url,
    group_title: group.title || "(untitled)",
    group_id: group.id,
  }));
  return tabs;
}

async function focusTab(tabId) {
  const tab = await chrome.tabs.get(tabId);

  // Set tab as active — NO window focus
  await chrome.tabs.update(tabId, { active: true });

  // Expand its group, collapse all others
  if (tab.groupId !== chrome.tabGroups.TAB_GROUP_ID_NONE) {
    await collapseAllExcept(tab.groupId);
  }

  return { tab_id: tabId, group_id: tab.groupId };
}

// raiseChromeWindow — explicit user-driven window focus.
// Only invoked from Raycast commands where the user *picked* a target.
// The "no chrome.windows.update({focused:true})" rule applies to passive
// auto-sync flows (port messages), not to deliberate user selection.
async function raiseChromeWindow(windowId) {
  await chrome.windows.update(windowId, { focused: true, drawAttention: true });
  return { window_id: windowId };
}

async function raiseGroupWindow(name) {
  const group = await findGroupByTitle(name);
  if (!group) throw new Error(`Tab group "${name}" not found`);
  const tabs = await chrome.tabs.query({ groupId: group.id });
  if (tabs.length === 0) throw new Error(`Tab group "${name}" has no tabs`);
  return raiseChromeWindow(tabs[0].windowId);
}

async function raiseTabWindow(tabId) {
  const tab = await chrome.tabs.get(tabId);
  return raiseChromeWindow(tab.windowId);
}

async function renameGroup(oldName, newName) {
  const group = await findGroupByTitle(oldName);
  if (!group) throw new Error(`Tab group "${oldName}" not found`);

  await chrome.tabGroups.update(group.id, { title: newName });
  return { group_id: group.id, old_name: oldName, new_name: newName };
}

async function cleanTabs() {
  const nativeGroup = await ensureReservedNativeGroup();
  const allTabs = await chrome.tabs.query({});
  const ungrouped = allTabs.filter(
    (t) => t.groupId === chrome.tabGroups.TAB_GROUP_ID_NONE
  );

  if (ungrouped.length === 0) return { moved: 0, group: RESERVED_GROUP_TITLE };

  const tabIds = ungrouped
    .map((t) => t.id)
    .filter((id) => id !== undefined && id !== null);

  if (tabIds.length > 0) {
    await chrome.tabs.group({ tabIds, groupId: nativeGroup.id });
    await chrome.tabGroups.update(nativeGroup.id, {
      title: RESERVED_GROUP_TITLE,
      collapsed: true,
    });
  }

  return { moved: tabIds.length, group: RESERVED_GROUP_TITLE };
}

// mergeWindows — collapse all normal Chrome windows of this profile into the
// single window with the most tabs. Tab groups are preserved; groups whose
// title already exists in the target window absorb the source group's tabs
// (their group entity is dropped), otherwise the entire group is moved over.
// Returns counts so the CLI can report what happened.
async function mergeWindows() {
  const windows = await chrome.windows.getAll({
    populate: true,
    windowTypes: ["normal"],
  });

  if (windows.length <= 1) {
    return {
      windows_merged: 0,
      target_window_id: windows[0]?.id ?? null,
      moved_tabs: 0,
      merged_groups: 0,
    };
  }

  let target = windows[0];
  for (const w of windows) {
    if ((w.tabs?.length ?? 0) > (target.tabs?.length ?? 0)) target = w;
  }

  const sourceWindows = windows.filter((w) => w.id !== target.id);

  // Index of group titles already present in the target window so we can
  // absorb same-named source groups into them instead of creating duplicates.
  const targetGroupsByTitle = new Map();
  {
    const targetGroups = await chrome.tabGroups.query({ windowId: target.id });
    for (const g of targetGroups) {
      const key = (g.title ?? "").toLowerCase();
      if (!targetGroupsByTitle.has(key)) targetGroupsByTitle.set(key, g);
    }
  }

  let movedTabs = 0;
  let mergedGroups = 0;

  for (const sw of sourceWindows) {
    const sourceGroups = await chrome.tabGroups.query({ windowId: sw.id });
    const handledGroupIds = new Set();

    // Tabs that belong to a source group: handle group-by-group so we can
    // either move the whole group or merge into a same-titled target group.
    for (const sg of sourceGroups) {
      handledGroupIds.add(sg.id);
      const titleKey = (sg.title ?? "").toLowerCase();
      const matching = targetGroupsByTitle.get(titleKey);
      const tabs = await chrome.tabs.query({ groupId: sg.id });
      if (tabs.length === 0) continue;
      const tabIds = tabs.map((t) => t.id);

      if (matching) {
        // Move tabs into target window first, then re-group into the
        // matching target group. chrome.tabs.move handles the cross-window
        // hop in one call.
        await chrome.tabs.move(tabIds, { windowId: target.id, index: -1 });
        await chrome.tabs.group({ tabIds, groupId: matching.id });
        movedTabs += tabIds.length;
      } else {
        // No same-titled group on the target: move the entire group entity.
        await chrome.tabGroups.move(sg.id, { windowId: target.id, index: -1 });
        // After the group lands on the target window it becomes a candidate
        // for absorbing future same-titled groups from later source windows.
        const refreshed = await chrome.tabGroups.get(sg.id);
        targetGroupsByTitle.set((refreshed.title ?? "").toLowerCase(), refreshed);
        movedTabs += tabIds.length;
        mergedGroups += 1;
      }
    }

    // Ungrouped tabs in the source window: just move them over.
    const looseTabs = (sw.tabs ?? []).filter(
      (t) => !handledGroupIds.has(t.groupId) && t.groupId === chrome.tabGroups.TAB_GROUP_ID_NONE
    );
    if (looseTabs.length > 0) {
      const ids = looseTabs.map((t) => t.id);
      await chrome.tabs.move(ids, { windowId: target.id, index: -1 });
      movedTabs += ids.length;
    }

    // Source window is now empty (or only has the placeholder new-tab Chrome
    // may have created during the move dance) — close it.
    try {
      await chrome.windows.remove(sw.id);
    } catch (_) {
      // best-effort: some windows refuse to close (e.g. devtools) — leave them.
    }
  }

  return {
    windows_merged: sourceWindows.length,
    target_window_id: target.id,
    moved_tabs: movedTabs,
    merged_groups: mergedGroups,
  };
}

// cycleTabInGroup — Move focus to the previous or next tab inside the
// active tab's group, wrapping around at the boundaries. No-op if the
// active tab is not in any group, or the group has only one tab.
async function cycleTabInGroup(direction) {
  if (direction !== "prev" && direction !== "next") {
    throw new Error(`Unknown direction: ${direction}`);
  }

  const group = await getActiveGroup();
  if (!group) {
    return { skipped: true, reason: "no_active_group" };
  }

  // chrome.tabs.query returns tabs in their visual (left-to-right) order
  // within the window, which matches what the user perceives as "next" / "prev".
  const tabs = await chrome.tabs.query({ groupId: group.id });
  if (tabs.length <= 1) {
    return { skipped: true, reason: "single_tab", group_id: group.id };
  }

  const [activeTab] = await chrome.tabs.query({
    active: true,
    windowId: group.windowId,
  });
  if (!activeTab) {
    return { skipped: true, reason: "no_active_tab" };
  }

  const idx = tabs.findIndex((t) => t.id === activeTab.id);
  if (idx < 0) {
    // Active tab is not in this group — shouldn't happen since we derived
    // the group from the active tab, but bail out defensively.
    return { skipped: true, reason: "active_outside_group" };
  }

  const offset = direction === "next" ? 1 : -1;
  const nextIdx = (idx + offset + tabs.length) % tabs.length;
  const target = tabs[nextIdx];

  await chrome.tabs.update(target.id, { active: true });
  rememberActiveTabForGroup(group.id, target.id);

  return {
    group_id: group.id,
    from_tab_id: activeTab.id,
    to_tab_id: target.id,
    direction,
    index: nextIdx,
    total: tabs.length,
  };
}

// --- Init ---
// connect() already guards against duplicate connections, safe to call from multiple events
connect();
chrome.runtime.onStartup.addListener(connect);
chrome.runtime.onInstalled.addListener(connect);
