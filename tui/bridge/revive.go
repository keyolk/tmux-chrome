package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ReviveLock is the lock directory bin/tmux-chrome's bridge_revive takes.
// Sharing it means a burst of tmux hooks and this UI ask Chrome to reload the
// extension once between them, not once each.
const ReviveLock = "/tmp/tmux-chrome-revive.lock"

// staleLock is how old a lock may be before it is taken to belong to a
// revive that died: the bash side holds it for ~5s at most.
const staleLock = 30 * time.Second

// DefaultExtensionID matches CHROME_EXTENSION_ID in bin/tmux-chrome.
const DefaultExtensionID = "dplackkpcodnphbdcmfppmcbhkonlecg"

// Reviver brings a dead bridge back by opening the extension's reload page,
// which restarts its service worker and with it the native host.
type Reviver struct {
	Client      Client
	ExtensionID string
	Lock        string
	// Open runs the reload; nil uses osascript. Tests replace it.
	Open func(ctx context.Context, url string) error
}

func (r Reviver) lock() string {
	if r.Lock != "" {
		return r.Lock
	}
	return ReviveLock
}

func (r Reviver) extensionID() string {
	if r.ExtensionID != "" {
		return r.ExtensionID
	}
	if id := os.Getenv("TMUX_CHROME_EXT_ID"); id != "" {
		return id
	}
	return DefaultExtensionID
}

// Revive returns nil once the bridge accepts connections again.
func (r Reviver) Revive(ctx context.Context) error {
	lock := r.lock()
	if err := os.Mkdir(lock, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("take revive lock: %w", err)
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > staleLock {
			if rmErr := os.Remove(lock); rmErr != nil {
				return fmt.Errorf("remove stale revive lock: %w", rmErr)
			}
			return r.Revive(ctx)
		}
		// Someone else is reviving: wait for them rather than pile on.
		return r.waitAlive(ctx, 10*time.Second)
	}
	defer os.Remove(lock)

	open := r.Open
	if open == nil {
		open = osascriptOpen
	}
	url := "chrome-extension://" + r.extensionID() + "/reload.html"
	if err := open(ctx, url); err != nil {
		return fmt.Errorf("open %s: %w", url, err)
	}
	// Measured by cmd_reload: the new host can take ~15s to appear.
	return r.waitAlive(ctx, 20*time.Second)
}

func (r Reviver) waitAlive(ctx context.Context, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if r.Client.Alive() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("bridge did not come back")
}

func osascriptOpen(ctx context.Context, url string) error {
	script := fmt.Sprintf(`tell application "Google Chrome" to open location %q`, url)
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// ChromeRunning reports whether Google Chrome is running. Reviving without it
// would launch Chrome, which a background retry loop must never do.
func ChromeRunning(ctx context.Context) bool {
	return exec.CommandContext(ctx, "pgrep", "-x", "Google Chrome").Run() == nil
}

// LaunchChrome starts Chrome without bringing it to the front; the user asked
// for it from the terminal and the terminal keeps focus.
func LaunchChrome(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "open", "-g", "-a", "Google Chrome").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
