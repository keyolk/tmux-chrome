# tmux-chrome

Bridge tmux windows to Chrome tab groups. Switch a tmux window and the matching Chrome tab group expands automatically — the rest collapse. Terminal never loses focus.

## How it works

```
tmux window switch
  → tmux-chrome CLI (Unix socket client)
    → bridge.py (native messaging host)
      → Chrome extension (tabGroups API)
```

Each tmux window name maps to a Chrome tab group title. When you switch windows, the bridge expands the matching group and collapses all others. All operations happen in the background — Chrome never steals focus from your terminal.

## Components

| Component | Path | Purpose |
|-----------|------|---------|
| CLI | `bin/tmux-chrome` | Terminal interface, tmux pane URL grabbing |
| Chrome extension | `extension/` | Tab group control via `chrome.tabGroups` API |
| Native messaging host | `host/bridge.py` | Unix socket ↔ Chrome native messaging bridge |
| Raycast extension | `raycast/` | GUI tab group switching and tab browsing |

## Install

```bash
git clone https://github.com/keyolk/tmux-chrome.git
cd tmux-chrome
make install
./install.sh
```

`make install` symlinks `tmux-chrome` to `~/.local/bin/`.

`install.sh` walks you through:
1. Loading the Chrome extension (Developer mode → Load unpacked)
2. Entering the extension ID
3. Installing the native messaging host manifest
4. Adding the tmux `after-select-window` hook

### Raycast (optional)

```bash
make raycast-dev
```

Registers two Raycast commands: **Switch Tab Group** and **Browse Tabs**.

## Usage

### Automatic switching

Name your tmux windows (`Ctrl-A n` → type name). Create Chrome tab groups with matching names. Switching tmux windows auto-switches tab groups.

### CLI

```bash
# Tab group operations
tmux-chrome switch               # Switch to group matching current tmux window
tmux-chrome list                 # List all tab groups
tmux-chrome delete               # Delete current window's tab group
tmux-chrome export-group <name>  # Export one group as JSON
tmux-chrome import-group <name>  # Import one group from JSON stdin/file

# Tab management
tmux-chrome add <url>            # Add URL to current window's group (auto-creates group)
tmux-chrome move                 # Move Chrome's active tab into current window's group
tmux-chrome remove               # Remove Chrome's active tab from its group
tmux-chrome clean                # Close all tabs that aren't in any tab group
tmux-chrome sync                 # REPORT tab groups with no matching tmux window (read-only)
tmux-chrome sync --force         # …and ungroup them (snapshots first; caps bulk wipes)
tmux-chrome snapshot             # Save the current tab-group layout to JSON
tmux-chrome restore              # Rebuild groups from the latest snapshot (re-groups loose tabs by URL)
tmux-chrome restore <file>       # Rebuild from a specific snapshot

# Interactive
tmux-chrome grab                 # Extract URLs from tmux panes → fzf multi-select → add to group
tmux-chrome tabs                 # Browse current group's tabs via fzf → focus selected
tmux-chrome tabs --all           # Browse all tabs across all groups
tmux-chrome picker               # Unified picker for apps + current group's tabs
```

### From tmux.sh

If you use [tmux.sh](https://gist.github.com/keyolk), add a forwarder:

```bash
function chrome_dispatch {
  tmux-chrome "$@"
}
```

Then call as `tmux.sh chrome grab`, `tmux.sh chrome tabs`, etc.

### Combined tmux + Chrome save/load

If you already use `tmux.sh save` / `tmux.sh load`, Chrome tab groups can be persisted alongside each tmux window.

How it works:
- tmux window name is the key
- matching Chrome tab group title is snapshotted into the same layout JSON
- on `tmux.sh load`, only the Chrome groups for the restored windows are recreated
- unrelated Chrome groups are left untouched

Saved per window:
- window name/layout/panes (existing tmux behavior)
- matching Chrome group tabs as an ordered list of URLs

Not saved:
- Chrome tab IDs / group IDs
- active tab
- collapsed state
- pinned state

Notes:
- duplicate tmux window names are ambiguous for Chrome mapping; Chrome snapshot is skipped for duplicates
- if the Chrome bridge is unavailable, tmux save/load still works; Chrome snapshot/restore is skipped gracefully
- restore recreates the saved URLs in order for that window's group

Example flow:
```bash
# create tmux windows: harness, civiz
# create matching Chrome groups: harness, civiz

tmux.sh save my-session
# later...
tmux.sh load
```

The saved layout file under `~/.config/tmux/layouts/*.json` will include an optional `chrome` block per window:

```json
{
  "name": "harness",
  "layout": "...",
  "panes": [...],
  "chrome": {
    "group": "harness",
    "tabs": [
      { "url": "https://github.com/...", "title": "..." },
      { "url": "https://grafana...", "title": "..." }
    ]
  }
}
```

## Design decisions

- **Single active group**: Only one tab group is expanded at a time. Any operation that expands a group collapses all others.
- **No focus stealing**: The extension never calls `chrome.windows.update({focused: true})`. It only sets tabs as active in the background.
- **Profile scoping**: Install the Chrome extension only in the profile you want to control. Other profiles are unaffected.
- **Native messaging keepalive**: The persistent native messaging port keeps the MV3 service worker alive — no polling or alarms needed.

## Safety & recovery

`sync` used to ungroup every Chrome group that didn't match a tmux window, with
no confirmation — so a single mismatch (or the `window-unlinked` hook firing on a
window close) could scatter every tab out of its group at once. It is now:

- **Report-only by default.** Plain `sync` (and the tmux hook) only *list* orphans.
  Ungrouping requires an explicit `sync --force`.
- **All-session aware.** Windows from every tmux session count, so a group owned
  by another session is never treated as an orphan.
- **Snapshotted before mutation.** `--force` writes a snapshot to
  `~/.local/state/tmux-chrome/` before touching anything.
- **Bulk-wipe capped.** Ungrouping more than `TMUX_CHROME_SYNC_BULK_LIMIT`
  (default 2) groups at once is refused unless you pass `sync --force-all`.

If groups ever get scattered, the tabs stay **open** (ungroup ≠ close). Recover with:

```bash
tmux-chrome restore                  # re-group loose tabs by URL from the latest snapshot
tmux-chrome restore --reopen-missing # also reopen tabs that were since closed
```

## Dependencies

- Python 3 (for `bridge.py` and socket communication in CLI)
- Chrome with Developer mode enabled
- tmux
- jq, fzf (for CLI interactive features)
- Node.js + bun (for Raycast extension, optional)

## Uninstall

```bash
make uninstall
```

Also remove the `after-select-window` hook line from `~/.tmux.conf` and unload the extension from `chrome://extensions`.

## License

MIT
