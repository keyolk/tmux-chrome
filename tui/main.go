// Command tmux-chrome-tui browses and edits Chrome tab groups from tmux.
// bin/tmux-chrome runs it as `tmux-chrome tui [--popup]`.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/app"
	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/theme"
	"github.com/keyolk/tuikit/tmux"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tmux-chrome tui:", err)
		os.Exit(1)
	}
}

func run() error {
	popup := flag.Bool("popup", false, "opened in a tmux popup: start in search, quit after jumping")
	window := flag.String("window", "", "tmux window id (@N) to act for; default: the one this pane is in")
	socket := flag.String("socket", bridge.DefaultSocket, "bridge socket")
	cli := flag.String("cli", "tmux-chrome", "bin/tmux-chrome, for palette commands")
	flag.Parse()

	stateDir := os.Getenv("TMUX_CHROME_STATE_DIR")
	if stateDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		stateDir = filepath.Join(home, ".local", "state", "tmux-chrome")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	// stdout is the screen; logs go to a file.
	logFile, err := os.OpenFile(filepath.Join(stateDir, "tui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags)

	tm := tmux.New(nil)
	win := *window
	if win == "" && tmux.InTmux(nil) {
		if pane := tmux.PaneFromEnv(nil); pane != "" {
			// A popup has no pane of its own; bin/tmux-chrome passes --window.
			if w, err := tm.Run("display-message", "-p", "-t", pane, "#{window_id}"); err == nil {
				win = w
			} else {
				logger.Printf("resolve window: %v", err)
			}
		}
	}

	m := app.New(app.Config{
		Bridge:   bridge.Client{Socket: *socket},
		Tmux:     tm,
		Window:   win,
		Popup:    *popup,
		CLI:      *cli,
		StateDir: stateDir,
		Logf:     logger.Printf,
		Theme:    theme.NewWith(theme.NewRenderer(os.Stdout), theme.DefaultRoles(), theme.DetectGlyphs(app.AppName)),
	})
	opts := append([]tea.ProgramOption{tea.WithAltScreen()}, mouse.ProgramOptions()...)
	_, err = tea.NewProgram(m, opts...).Run()
	return err
}
