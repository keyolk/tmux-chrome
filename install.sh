#!/bin/bash
set -euo pipefail

BRIDGE_DIR="$HOME/src/keyolk/tmux-chrome-bridge"
NATIVE_HOST_DIR="$HOME/Library/Application Support/Google/Chrome/NativeMessagingHosts"
NATIVE_HOST_NAME="com.tmux.chrome.bridge"
MANIFEST_PATH="$NATIVE_HOST_DIR/$NATIVE_HOST_NAME.json"

echo "=== tmux-Chrome Tab Group Bridge Installer ==="
echo ""

# 1. Make scripts executable
chmod +x "$BRIDGE_DIR/host/bridge.py"
echo "[1/5] Made bridge.py executable"

# 2. Load extension
echo ""
echo "[2/5] Load the Chrome extension:"
echo "  1. Open Chrome (the profile you want to use)"
echo "  2. Go to: chrome://extensions"
echo "  3. Enable 'Developer mode' (top right toggle)"
echo "  4. Click 'Load unpacked'"
echo "  5. Select: $BRIDGE_DIR/extension"
echo ""
read -rp "Press Enter after loading the extension..."

# 3. Get extension ID
echo ""
echo "[3/5] Find the extension ID:"
echo "  On chrome://extensions, look for 'tmux Tab Group Bridge'"
echo "  The ID is shown below the extension name (e.g., abcdefghijklmnopqrstuvwxyz)"
echo ""
read -rp "Enter the extension ID: " EXTENSION_ID

if [[ -z "$EXTENSION_ID" || ${#EXTENSION_ID} -lt 20 ]]; then
  echo "Error: Invalid extension ID. It should be 32 lowercase letters." >&2
  exit 1
fi

# 4. Generate and install native messaging host manifest
mkdir -p "$NATIVE_HOST_DIR"

cat > "$MANIFEST_PATH" << EOF
{
  "name": "$NATIVE_HOST_NAME",
  "description": "tmux to Chrome tab group bridge",
  "path": "$BRIDGE_DIR/host/bridge.py",
  "type": "stdio",
  "allowed_origins": [
    "chrome-extension://$EXTENSION_ID/"
  ]
}
EOF

echo "[4/5] Installed native messaging host manifest: $MANIFEST_PATH"

# 5. Add tmux hook
TMUX_HOOK='set-hook -g after-select-window '"'"'run-shell -b "~/.local/bin/tmux.sh chrome switch"'"'"''

if grep -q "tmux-chrome-bridge\|chrome switch" ~/.tmux.conf 2>/dev/null; then
  echo "[5/5] tmux hook already exists in ~/.tmux.conf (skipped)"
else
  echo "" >> ~/.tmux.conf
  echo "# tmux-chrome tab group bridge" >> ~/.tmux.conf
  echo "$TMUX_HOOK" >> ~/.tmux.conf
  echo "[5/5] Added after-select-window hook to ~/.tmux.conf"
fi

# Reload tmux if running
if tmux info &>/dev/null; then
  tmux source-file ~/.tmux.conf 2>/dev/null && echo "  tmux config reloaded"
fi

echo ""
echo "=== Installation complete ==="
echo ""
echo "Next steps:"
echo "  1. Close and reopen Chrome (to activate native messaging)"
echo "  2. Name your tmux windows: Ctrl-A n → 'my-project'"
echo "  3. Create matching tab groups: tmux.sh chrome grab"
echo "  4. Switch tmux windows → Chrome tab groups auto-switch"
echo ""
echo "Commands:"
echo "  tmux.sh chrome list              # List tab groups"
echo "  tmux.sh chrome add <url>         # Add URL to current window's group"
echo "  tmux.sh chrome grab              # Extract URLs from panes → fzf → add"
echo "  tmux.sh chrome tabs              # Browse group tabs via fzf"
echo "  tmux.sh chrome tabs --all        # Browse all tabs via fzf"
echo "  tmux.sh chrome delete            # Delete current window's group"
