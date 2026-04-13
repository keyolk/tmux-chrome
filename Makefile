PREFIX ?= $(HOME)/.local
CHROME_NMH_DIR = $(HOME)/Library/Application Support/Google/Chrome/NativeMessagingHosts
NMH_NAME = com.tmux.chrome.bridge

.PHONY: install uninstall link unlink raycast-dev

install: link
	@chmod +x bin/tmux-chrome host/bridge.py
	@echo "✓ tmux-chrome installed to $(PREFIX)/bin/tmux-chrome"
	@echo ""
	@echo "Next: run install.sh to set up Chrome extension + native messaging host"

uninstall: unlink
	@rm -f "$(CHROME_NMH_DIR)/$(NMH_NAME).json"
	@echo "✓ Uninstalled tmux-chrome"

link:
	@mkdir -p $(PREFIX)/bin
	@ln -sf $(CURDIR)/bin/tmux-chrome $(PREFIX)/bin/tmux-chrome

unlink:
	@rm -f $(PREFIX)/bin/tmux-chrome

raycast-dev:
	@cd raycast && bun install && bun run dev
