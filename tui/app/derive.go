package app

import (
	"sort"
	"strconv"

	"github.com/keyolk/tmux-chrome/tui/bridge"
)

// reservedGroup is the group clean_tabs sweeps loose tabs into. It belongs to
// no tmux window on purpose, so it is never an orphan.
const reservedGroup = "native"

const ungroupedKey = "g:none"

// groupRow is one line of the group pane. The ungrouped tabs appear as a
// virtual row (Virtual, GroupID == bridge.NoGroup) so they can be browsed and
// moved like any group.
type groupRow struct {
	key       string
	GroupID   int
	Title     string
	Color     string
	Collapsed bool
	WindowID  int
	Tabs      int
	Matches   int // tabs that match the query; -1 with no query
	Current   bool
	Window    *tmuxWindow
	Orphan    bool
	Virtual   bool
	Hit       []int
}

func (g groupRow) ID() string { return g.key }

func (g groupRow) label() string {
	if g.Virtual {
		return "(그룹 없음)"
	}
	if g.Title == "" {
		return "(제목 없음)"
	}
	return g.Title
}

// tabRow is one line of the tab pane.
type tabRow struct {
	key        string
	Tab        bridge.Tab
	GroupTitle string
	GroupColor string
	Hit        []int
	score      int
}

func (t tabRow) ID() string { return t.key }

func groupKey(id int) string {
	if id == bridge.NoGroup {
		return ungroupedKey
	}
	return "g:" + strconv.Itoa(id)
}

func tabKey(id int) string { return "t:" + strconv.Itoa(id) }

// buildGroups lists every group in the order Chrome shows them: by window,
// then by the position of the group's first tab. curWin is the tmux window
// the UI was opened from; its group is marked Current.
func buildGroups(s bridge.Snapshot, wins []tmuxWindow, curWin string) []groupRow {
	first := map[int]int{}
	count := map[int]int{}
	for _, t := range s.Tabs {
		count[t.GroupID]++
		if i, ok := first[t.GroupID]; !ok || t.Index < i {
			first[t.GroupID] = t.Index
		}
	}
	rows := make([]groupRow, 0, len(s.Groups)+1)
	for _, g := range s.Groups {
		r := groupRow{
			key: groupKey(g.ID), GroupID: g.ID, Title: g.Title, Color: g.Color,
			Collapsed: g.Collapsed, WindowID: g.WindowID, Tabs: count[g.ID], Matches: -1,
		}
		if w, ok := windowFor(wins, g.ID, g.Title); ok {
			r.Window = &w
			r.Current = curWin != "" && w.ID == curWin
		} else {
			r.Orphan = normalizeTitle(g.Title) != reservedGroup
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].WindowID != rows[j].WindowID {
			return rows[i].WindowID < rows[j].WindowID
		}
		return first[rows[i].GroupID] < first[rows[j].GroupID]
	})
	if n := count[bridge.NoGroup]; n > 0 {
		rows = append(rows, groupRow{key: ungroupedKey, GroupID: bridge.NoGroup, Tabs: n, Matches: -1, Virtual: true})
	}
	return rows
}

// view is what the two panes show for one snapshot, query and scope.
type view struct {
	groups []groupRow
	// flat is true when the tab pane lists matches across every group
	// instead of following the group cursor.
	flat bool
	tabs []tabRow // set only when flat
}

func deriveView(all []groupRow, s bridge.Snapshot, q query, sc scope) view {
	if q.empty() {
		return view{groups: all}
	}
	if sc == scopeGroups {
		var gs []groupRow
		for _, g := range all {
			if g.Virtual {
				continue
			}
			if _, ok := matchGroup(q, g.Title); ok {
				g.Hit = highlight(g.Title, q.terms)
				gs = append(gs, g)
			}
		}
		return view{groups: gs}
	}

	info := map[int]groupRow{}
	for _, g := range all {
		info[g.GroupID] = g
	}
	matches := map[int]int{}
	var tabs []tabRow
	for _, t := range s.Tabs {
		g := info[t.GroupID]
		score, ok := matchTab(q, sc, t.Title, t.URL, groupTitleOf(g))
		if !ok {
			continue
		}
		matches[t.GroupID]++
		tabs = append(tabs, tabRow{
			key: tabKey(t.ID), Tab: t, GroupTitle: g.label(), GroupColor: g.Color,
			Hit: highlight(t.Title, q.terms), score: score,
		})
	}
	sort.SliceStable(tabs, func(i, j int) bool { return tabs[i].score > tabs[j].score })

	var gs []groupRow
	for _, g := range all {
		_, titleHit := matchGroup(q, g.Title)
		if sc == scopeTabs {
			titleHit = false
		}
		if matches[g.GroupID] == 0 && (!titleHit || g.Virtual) {
			continue
		}
		g.Matches = matches[g.GroupID]
		if titleHit {
			g.Hit = highlight(g.Title, q.terms)
		}
		gs = append(gs, g)
	}
	return view{groups: gs, flat: true, tabs: tabs}
}

// groupTitleOf is the title a query sees: the virtual row has none, so "@x"
// never matches loose tabs by accident.
func groupTitleOf(g groupRow) string {
	if g.Virtual {
		return ""
	}
	return g.Title
}

// tabsIn lists one group's tabs in tab-strip order.
func tabsIn(s bridge.Snapshot, g groupRow) []tabRow {
	var out []tabRow
	for _, t := range s.Tabs {
		if t.GroupID == g.GroupID {
			out = append(out, tabRow{key: tabKey(t.ID), Tab: t, GroupTitle: g.label(), GroupColor: g.Color})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tab.WindowID != out[j].Tab.WindowID {
			return out[i].Tab.WindowID < out[j].Tab.WindowID
		}
		return out[i].Tab.Index < out[j].Tab.Index
	})
	return out
}
