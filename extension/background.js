// tmux Tab Group Bridge — Chrome Extension Background Service Worker
// Communicates with native messaging host to sync tab groups with tmux windows.
// RULE: Never call chrome.windows.update({focused: true}) — terminal must keep focus.

const NATIVE_HOST = "com.tmux.chrome.bridge";
let port = null;

function connect() {
  try {
    port = chrome.runtime.connectNative(NATIVE_HOST);
  } catch (e) {
    console.error("Failed to connect to native host:", e);
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
    scheduleReconnect();
  });

  console.log("Connected to native host");
}

function scheduleReconnect() {
  setTimeout(connect, 5000);
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
      return addTab(msg.name, msg.url);
    case "move_tab":
      return moveTab(msg.name);
    case "remove_tab":
      return removeTab();
    case "get_tabs":
      return getTabs(msg.name, msg.all);
    case "focus_tab":
      return focusTab(msg.tab_id);
    default:
      throw new Error(`Unknown message type: ${msg.type}`);
  }
}

async function findGroupByTitle(title) {
  const groups = await chrome.tabGroups.query({});
  return groups.find(
    (g) => g.title && g.title.toLowerCase() === title.toLowerCase()
  );
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

  // Set first tab in group as active — NO window focus
  const tabs = await chrome.tabs.query({ groupId: group.id });
  if (tabs.length > 0) {
    await chrome.tabs.update(tabs[0].id, { active: true });
  }

  return { matched: true, group_id: group.id };
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

async function addTab(groupName, url) {
  let group = await findGroupByTitle(groupName);

  // Create group if it doesn't exist
  if (!group) {
    const tab = await chrome.tabs.create({ url, active: false });
    const groupId = await chrome.tabs.group({ tabIds: [tab.id] });
    await chrome.tabGroups.update(groupId, {
      title: groupName,
      collapsed: false,
    });
    await collapseAllExcept(groupId);
    return { group_id: groupId, tab_id: tab.id, created_group: true };
  }

  // Add tab to existing group
  const tab = await chrome.tabs.create({ url, active: false });
  await chrome.tabs.group({ tabIds: [tab.id], groupId: group.id });
  await collapseAllExcept(group.id);
  return { group_id: group.id, tab_id: tab.id, created_group: false };
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

async function getTabs(groupName, all) {
  let tabs;
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
  } else {
    const group = await findGroupByTitle(groupName);
    if (!group) throw new Error(`Tab group "${groupName}" not found`);
    const groupTabs = await chrome.tabs.query({ groupId: group.id });
    tabs = groupTabs.map((t) => ({
      id: t.id,
      title: t.title,
      url: t.url,
      group_title: group.title,
      group_id: group.id,
    }));
  }
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

// --- Init ---
connect();
