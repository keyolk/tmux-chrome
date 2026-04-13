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
tmux-chrome switch          # Switch to group matching current tmux window
tmux-chrome list            # List all tab groups
tmux-chrome delete          # Delete current window's tab group

# Tab management
tmux-chrome add <url>       # Add URL to current window's group (auto-creates group)
tmux-chrome move            # Move Chrome's active tab into current window's group
tmux-chrome remove          # Remove Chrome's active tab from its group

# Interactive
tmux-chrome grab            # Extract URLs from tmux panes → fzf multi-select → add to group
tmux-chrome tabs            # Browse current group's tabs via fzf → focus selected
tmux-chrome tabs --all      # Browse all tabs across all groups
```

### From tmux.sh

If you use [tmux.sh](https://gist.github.com/keyolk), add a forwarder:

```bash
function chrome_dispatch {
  tmux-chrome "$@"
}
```

Then call as `tmux.sh chrome grab`, `tmux.sh chrome tabs`, etc.

## Design decisions

- **Single active group**: Only one tab group is expanded at a time. Any operation that expands a group collapses all others.
- **No focus stealing**: The extension never calls `chrome.windows.update({focused: true})`. It only sets tabs as active in the background.
- **Profile scoping**: Install the Chrome extension only in the profile you want to control. Other profiles are unaffected.
- **Native messaging keepalive**: The persistent native messaging port keeps the MV3 service worker alive — no polling or alarms needed.

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
