PREFIX ?= $(HOME)/.local
CHROME_NMH_DIR = $(HOME)/Library/Application Support/Google/Chrome/NativeMessagingHosts
NMH_NAME = com.tmux.chrome.bridge

.PHONY: install uninstall link unlink raycast-dev tui test

install: link tui
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
	@ln -sf $(CURDIR)/bin/tmux-chrome-open $(PREFIX)/bin/tmux-chrome-open

tui:
	@cd tui && go build -o ../bin/tmux-chrome-tui .
	@echo "✓ built bin/tmux-chrome-tui"

test:
	@cd tui && go vet ./... && go test ./...
	@python3 -m unittest discover -s host
	@node --test extension/stale.test.js
	@cd raycast && bun test

unlink:
	@rm -f $(PREFIX)/bin/tmux-chrome $(PREFIX)/bin/tmux-chrome-open

raycast-dev:
	@cd raycast && bun install && bun run dev
