package app

import (
	"maps"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/toast"
)

func (m Model) stack() []keys.Ctx {
	p := ctxGroups
	if m.focus == paneTabs {
		p = ctxTabs
	}
	if m.conn == stHalted {
		return []keys.Ctx{ctxHalted, p, ctxList, ctxGlobal}
	}
	return []keys.Ctx{p, ctxList, ctxGlobal}
}

// activeStack is the stack keys resolve against right now, overlays first.
func (m Model) activeStack() []keys.Ctx {
	switch {
	case m.help:
		return []keys.Ctx{ctxHelp}
	case m.ov != nil:
		return []keys.Ctx{m.ov.ctx()}
	case m.searching:
		return []keys.Ctx{ctxSearch}
	}
	return m.stack()
}

func (m Model) keyOpts() keys.Opts {
	return keys.Opts{
		Visible: func(a string) bool { return !needsBridge[a] || m.conn == stConnected },
		State: func(a string) string {
			if a != "refresh" {
				return ""
			}
			switch {
			case m.conn == stLegacy:
				return "legacy"
			case m.conn.down():
				return "down"
			}
			return ""
		},
	}
}

func (m Model) onKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case m.help:
		return m.helpKey(msg)
	case m.ov != nil:
		return m.ov.key(m, msg)
	case m.searching:
		// The field owns printable keys, so resolve without the Korean
		// jamo-to-QWERTY mapping and give the field whatever is left.
		if b, ok := m.km.ResolveRaw([]keys.Ctx{ctxSearch}, msg, m.keyOpts()); ok {
			return m.run(b.Action)
		}
		f, used := m.field.Update(msg)
		if !used {
			return m, nil
		}
		m.field = f
		m = m.relayout()
		m.focus = m.resultsPane()
		return m, nil
	}
	b, ok := m.km.Resolve(m.stack(), msg, m.keyOpts())
	if !ok {
		return m, nil
	}
	return m.run(b.Action)
}

// run performs an action by name: keys, the palette and mouse all come here.
func (m Model) run(action string) (Model, tea.Cmd) {
	if needsBridge[action] && m.conn != stConnected {
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "conn", Text: "Chrome과 연결이 끊겨 실행하지 않았습니다", Level: toast.Warn})
		return m, nil
	}
	switch action {
	case "quit":
		return m.quit()
	case "help":
		m.help, m.helpOff = true, 0
		return m, nil
	case "search":
		m.searching = true
		m.focus = m.resultsPane()
		return m, nil
	case "scope", "search.scope":
		m.sc = (m.sc + 1) % scopeCount
		m = m.relayout()
		m.focus = m.resultsPane()
		return m.savePrefs()
	case "palette":
		return m.openPalette()
	case "refresh":
		return m.manualReconnect()
	case "chrome.launch":
		return m, launchChrome()
	case "undo":
		return m.undoLast()
	case "clean":
		return m.openClean()

	case "down", "up", "top", "bottom", "halfdown", "halfup":
		return m.move(m.focus, action), nil
	case "left":
		m.focus = paneGroups
		return m.savePrefs()
	case "right":
		m.focus = paneTabs
		return m.savePrefs()
	case "select":
		return m.toggleSelect(m.focus), nil
	case "selectall":
		return m.selectAll(), nil
	case "clear":
		switch {
		case len(m.selTabs)+len(m.selGroups) > 0:
			m.selTabs, m.selGroups = map[int]string{}, map[int]string{}
		case m.field.Value() != "":
			m.field = m.field.SetValue("")
			m = m.relayout()
		case m.cfg.Popup:
			return m.quit()
		}
		return m, nil

	case "search.exit":
		m.searching = false
		return m, nil
	case "search.down":
		return m.move(m.resultsPane(), "down"), nil
	case "search.up":
		return m.move(m.resultsPane(), "up"), nil
	case "search.select":
		return m.toggleSelect(m.resultsPane()), nil
	case "search.open":
		if m.resultsPane() == paneTabs {
			return m.run("tab.focus")
		}
		return m.run("group.enter")

	case "group.enter":
		return m.groupEnter()
	case "group.new":
		return m.groupNew()
	case "group.rename":
		return m.groupRename()
	case "group.color":
		return m.groupColor()
	case "group.bind":
		return m.groupBind()
	case "group.merge":
		return m.groupMerge()
	case "group.ungroup":
		return m.groupUngroup()
	case "group.close":
		return m.groupClose()
	case "group.open", "tab.open":
		return m.openURL()
	case "group.grab", "tab.grab":
		return m.grab()

	case "tab.focus":
		return m.tabFocus()
	case "tab.move":
		return m.tabMove()
	case "tab.ungroup":
		return m.tabUngroup()
	case "tab.close":
		return m.tabClose()
	case "tab.pin":
		return m.tabPin()
	case "tab.reload":
		return m.tabReload()
	case "tab.copy":
		return m.tabCopy()
	case "tab.new":
		return m.groupNew()
	}
	return m, nil
}

func (m Model) move(p pane, action string) Model {
	if p == paneGroups {
		m.groups = moveList(m.groups, action)
		return m.syncTabs()
	}
	m.tabs = moveList(m.tabs, action)
	return m
}

type mover[T any] interface {
	Move(int) T
	Top() T
	Bottom() T
	HalfDown() T
	HalfUp() T
}

func moveList[T mover[T]](l T, action string) T {
	switch action {
	case "down":
		return l.Move(1)
	case "up":
		return l.Move(-1)
	case "top":
		return l.Top()
	case "bottom":
		return l.Bottom()
	case "halfdown":
		return l.HalfDown()
	case "halfup":
		return l.HalfUp()
	}
	return l
}

func (m Model) toggleSelect(p pane) Model {
	if p == paneGroups {
		g, ok := m.groups.Selected()
		if !ok || g.Virtual {
			return m
		}
		m.selGroups = maps.Clone(m.selGroups)
		if _, on := m.selGroups[g.GroupID]; on {
			delete(m.selGroups, g.GroupID)
		} else {
			m.selGroups[g.GroupID] = g.Title
		}
		m.groups = m.groups.Move(1)
		return m.syncTabs()
	}
	t, ok := m.tabs.Selected()
	if !ok {
		return m
	}
	m.selTabs = maps.Clone(m.selTabs)
	if _, on := m.selTabs[t.Tab.ID]; on {
		delete(m.selTabs, t.Tab.ID)
	} else {
		m.selTabs[t.Tab.ID] = t.Tab.URL
	}
	m.tabs = m.tabs.Move(1)
	return m
}

func (m Model) selectAll() Model {
	if m.focus == paneGroups {
		m.selGroups = maps.Clone(m.selGroups)
		for _, g := range m.groups.Items() {
			if !g.Virtual {
				m.selGroups[g.GroupID] = g.Title
			}
		}
		return m
	}
	m.selTabs = maps.Clone(m.selTabs)
	for _, t := range m.tabs.Items() {
		m.selTabs[t.Tab.ID] = t.Tab.URL
	}
	return m
}

func (m Model) helpKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	b, ok := m.km.Resolve([]keys.Ctx{ctxHelp}, msg, m.keyOpts())
	if !ok {
		return m, nil
	}
	switch b.Action {
	case "quit":
		return m.quit()
	case "help.close":
		m.help = false
	case "help.down":
		m.helpOff++
	case "help.up":
		m.helpOff = max(0, m.helpOff-1)
	}
	return m, nil
}
