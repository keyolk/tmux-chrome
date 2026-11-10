package app

import (
	"context"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/keyolk/tuikit/tmux"
)

// tmuxWindow is a tmux window and the Chrome group bin/tmux-chrome bound it
// to. The binding lives in window options that the tmux hooks write:
// @chrome_group (title) and @chrome_group_id.
type tmuxWindow struct {
	ID          string // @3
	Name        string
	SessionName string
	Pane        string // a pane in the window, the active one when known
	GroupID     int    // 0 when @chrome_group_id is unset; Chrome ids start at 1
	GroupTitle  string
}

func listWindows(ctx context.Context, c tmux.Client) ([]tmuxWindow, error) {
	panes, err := c.Panes(ctx, tmux.PaneFilter{Options: []string{"@chrome_group", "@chrome_group_id"}})
	if err != nil {
		return nil, err
	}
	var out []tmuxWindow
	at := map[string]int{}
	for _, p := range panes {
		i, seen := at[p.WindowID]
		if seen {
			if p.Active {
				out[i].Pane = p.ID
			}
			continue
		}
		gid, _ := strconv.Atoi(p.Options["@chrome_group_id"]) // unset or garbage both mean "no id"
		at[p.WindowID] = len(out)
		out = append(out, tmuxWindow{
			ID: p.WindowID, Name: p.WindowName, SessionName: p.SessionName, Pane: p.ID,
			GroupID: gid, GroupTitle: p.Options["@chrome_group"],
		})
	}
	return out, nil
}

// Window names carry a status prefix while an agent works in them; the
// bash side strips the same two before comparing (normalize_window_title).
var titlePrefixes = []string{"⏳", "🟢"}

// normalizeTitle is the comparison key the extension (normalizeGroupTitle)
// and bin/tmux-chrome (normalize_window_title) use: NFKC, lower case, only
// ASCII letters and digits. Keep the three in step, or a window and its
// group stop recognising each other.
func normalizeTitle(s string) string {
	for _, p := range titlePrefixes {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimPrefix(s, " ")
	s = strings.ToLower(norm.NFKC.String(s))
	var b strings.Builder
	for _, r := range s {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// windowFor finds the tmux window bound to a group: by stored group id
// first, then by title. A title with nothing left after normalising (all
// Hangul, say) matches nothing, as on the bash side.
func windowFor(wins []tmuxWindow, groupID int, title string) (tmuxWindow, bool) {
	for _, w := range wins {
		if w.GroupID != 0 && w.GroupID == groupID {
			return w, true
		}
	}
	key := normalizeTitle(title)
	if key == "" {
		return tmuxWindow{}, false
	}
	for _, w := range wins {
		if normalizeTitle(w.Name) == key {
			return w, true
		}
	}
	return tmuxWindow{}, false
}
