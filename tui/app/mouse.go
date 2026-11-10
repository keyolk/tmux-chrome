package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tuikit/mouse"
)

// onMouse follows E14: a click selects, a second click on the selected row
// runs Enter, the wheel scrolls; overlays own the mouse, and nothing that
// writes is reachable by a single click.
func (m Model) onMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if m.ov != nil || m.help || m.snap == nil {
		return m, nil
	}
	g := m.geometry()
	inGroups := g.groupsBox.Contains(msg.X, msg.Y) && (!g.single || m.focus == paneGroups)
	inTabs := g.tabsBox.Contains(msg.X, msg.Y) && (!g.single || m.focus == paneTabs)

	if d := mouse.Delta(msg); d != 0 {
		switch {
		case inGroups:
			m.groups = m.groups.Move(d)
			return m.syncTabs(), nil
		case inTabs:
			m.tabs = m.tabs.Move(d)
		}
		return m, nil
	}
	if !mouse.IsLeftPress(msg) {
		return m, nil
	}
	switch {
	case inGroups:
		hit := mouse.ListHit(m.groups, g.groups, msg)
		if !hit.HasItem {
			return m, nil
		}
		m.focus = paneGroups
		m.searching = false
		m.groups = m.groups.SelectIndex(hit.Index)
		m = m.syncTabs()
		var double bool
		m.clicks, double = m.clicks.Press(m.groups.SelectedID(), m.now())
		if double {
			return m.run("group.enter")
		}
	case inTabs:
		hit := mouse.ListHit(m.tabs, g.tabs, msg)
		if !hit.HasItem {
			return m, nil
		}
		m.focus = paneTabs
		m.searching = false
		m.tabs = m.tabs.SelectIndex(hit.Index)
		var double bool
		m.clicks, double = m.clicks.Press(m.tabs.SelectedID(), m.now())
		if double {
			return m.run("tab.focus")
		}
	case msg.Y == g.search:
		m.searching = true
	}
	return m, nil
}
