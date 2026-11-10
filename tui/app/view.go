package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/input"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/render"
	"github.com/keyolk/tuikit/toast"
)

// Layout floor. Below minW×minH the UI says so instead of drawing a broken
// frame; below splitW it shows one pane at a time (h/l switch).
const (
	minW   = 40
	minH   = 10
	splitW = 70
)

// chromeColors are Chrome's own group colors, so a group reads the same here
// as on the tab strip. They are data (a group's color), not UI chrome, which
// is why they are not theme roles. NO_COLOR drops them with the rest.
var chromeColors = map[string]lipgloss.AdaptiveColor{
	"grey":   {Light: "#5F6368", Dark: "#DADCE0"},
	"blue":   {Light: "#1A73E8", Dark: "#8AB4F8"},
	"red":    {Light: "#D93025", Dark: "#F28B82"},
	"yellow": {Light: "#B06000", Dark: "#FDD663"},
	"green":  {Light: "#188038", Dark: "#81C995"},
	"pink":   {Light: "#D01884", Dark: "#FF8BCB"},
	"purple": {Light: "#9334E6", Dark: "#D7AEFB"},
	"cyan":   {Light: "#007B83", Dark: "#78D9EC"},
	"orange": {Light: "#C26401", Dark: "#FCAD70"},
}

func (m Model) colorStyle(c string) lipgloss.Style {
	st := m.th.Text
	if m.th.NoColor() || m.th.Renderer() == nil {
		return st
	}
	if col, ok := chromeColors[c]; ok {
		return st.Foreground(col)
	}
	return st
}

// highlightRunes styles the runes of s at positions (rune indexes).
func highlightRunes(s string, pos []int, st lipgloss.Style) string {
	if len(pos) == 0 {
		return s
	}
	hit := map[int]bool{}
	for _, p := range pos {
		hit[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if hit[i] {
			b.WriteString(st.Render(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// geometry is where things are on screen; View and the mouse both use it,
// so a click always lands on what was drawn.
type geometry struct {
	header, search     int // rows
	groups, tabs       mouse.Rect
	groupsBox, tabsBox mouse.Rect
	single             bool
	footer             int
}

func (m Model) geometry() geometry {
	var g geometry
	g.header, g.search = 0, 1
	toastH := 0
	if t := toast.View(m.toasts, m.w, m.th.ToastStyles()); t != "" {
		toastH = strings.Count(t, "\n") + 1
	}
	bodyY := 2
	bodyH := max(3, m.h-bodyY-1-toastH)
	g.footer = m.h - 1
	g.single = m.w < splitW
	if g.single {
		box := mouse.Rect{X: 0, Y: bodyY, W: m.w, H: bodyH}
		g.groupsBox, g.tabsBox = box, box
	} else {
		gw := min(max(24, m.w*32/100), 44)
		g.groupsBox = mouse.Rect{X: 0, Y: bodyY, W: gw, H: bodyH}
		g.tabsBox = mouse.Rect{X: gw, Y: bodyY, W: m.w - gw, H: bodyH}
	}
	g.groups = g.groupsBox.Inset(1, 1, 1, 1)
	g.tabs = g.tabsBox.Inset(1, 1, 1, 1)
	return g
}

func (m Model) sizeLists() Model {
	if m.w == 0 {
		return m
	}
	g := m.geometry()
	m.groups = m.groups.SetHeight(max(1, g.groups.H))
	m.tabs = m.tabs.SetHeight(max(1, g.tabs.H))
	return m
}

func (m Model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.w < minW || m.h < minH {
		return render.FitLines(fmt.Sprintf("터미널이 너무 작습니다 (%dx%d, 최소 %dx%d)", m.w, m.h, minW, minH), m.w, m.h)
	}
	g := m.geometry()
	lines := []string{
		render.Safe("header", m.w, 1, m.logf, m.headerView),
		render.Safe("search", m.w, 1, m.logf, m.searchView),
	}
	body := m.bodyView(g)
	lines = append(lines, body)
	if t := toast.View(m.toasts, m.w, m.th.ToastStyles()); t != "" {
		lines = append(lines, t)
	}
	lines = append(lines, m.footerView())
	frame := render.FitLines(strings.Join(lines, "\n"), m.w, m.h)

	switch {
	case m.help:
		return render.Center(render.Dim(frame), m.helpView(), m.w, m.h)
	case m.ov != nil:
		box := render.Safe("overlay", m.w, m.h, m.logf, func() string { return m.ov.view(m, m.w, m.h) })
		return render.Center(render.Dim(frame), box, m.w, m.h)
	}
	return frame
}

func (m Model) headerView() string {
	text, lvl := m.badge()
	st := m.th.Muted
	switch lvl {
	case toast.Success:
		st = m.th.Success
	case toast.Warn:
		st = m.th.Warn
	case toast.Error:
		st = m.th.Error
	}
	left := m.th.Title.Render("tmux-chrome") + "  " + st.Render(text)
	var right []string
	if m.snap != nil {
		right = append(right, fmt.Sprintf("그룹 %d · 탭 %d", len(m.snap.Groups), len(m.snap.Tabs)))
	}
	if n := len(m.selTabs) + len(m.selGroups); n > 0 {
		right = append(right, m.th.Accent.Render(fmt.Sprintf("선택 %d", n)))
	}
	if h := m.undoHint(); h != "" {
		right = append(right, m.th.Muted.Render(h))
	}
	r := strings.Join(right, m.th.Glyphs.Sep)
	gap := m.w - render.Width(left) - render.Width(r)
	if gap < 1 {
		return render.Truncate(left, m.w)
	}
	return left + strings.Repeat(" ", gap) + r
}

func (m Model) searchView() string {
	scope := m.th.Muted.Render("[" + m.sc.String() + "]")
	prompt := m.th.Accent.Render("> ")
	w := m.w - render.Width(prompt) - render.Width(scope) - 1
	field := m.field.View(max(1, w), m.searching, input.Style{Placeholder: m.th.Subtle})
	if !m.searching && m.field.Value() == "" {
		field = render.Pad(m.th.Subtle.Render("/ 검색  (@그룹 /url !제외)"), max(1, w))
	}
	return prompt + field + " " + scope
}

func (m Model) bodyView(g geometry) string {
	if m.snap == nil {
		msg := "Chrome에서 탭을 읽는 중…"
		if m.conn.down() && m.conn != stConnecting {
			text, _ := m.badge()
			msg = "아직 받은 데이터가 없습니다 · " + text
		}
		return render.FitLines(m.th.Muted.Render("  "+msg), m.w, g.groupsBox.H)
	}
	gp := render.Safe("groups", g.groupsBox.W, g.groupsBox.H, m.logf, func() string { return m.groupsView(g.groupsBox) })
	tp := render.Safe("tabs", g.tabsBox.W, g.tabsBox.H, m.logf, func() string { return m.tabsView(g.tabsBox) })
	if g.single {
		if m.focus == paneGroups {
			return gp
		}
		return tp
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, gp, tp)
}

func (m Model) boxStyle(p pane) lipgloss.Style {
	if m.focus == p && m.ov == nil && !m.help {
		return m.th.BoxFocus
	}
	return m.th.Box
}

func (m Model) groupsView(r mouse.Rect) string {
	cw, ch := render.BoxContent(r.W, r.H, m.th.Box)
	items := m.groups.Items()
	start, end := m.groups.Window()
	var lines []string
	sel := m.groups.SelectedID()
	stale := m.conn != stConnected
	for i := start; i < end; i++ {
		g := items[i]
		lines = append(lines, m.groupLine(g, g.key == sel, cw, stale))
	}
	if len(items) == 0 {
		lines = append(lines, m.th.Muted.Render("맞는 그룹이 없습니다"))
	}
	above, below := m.groups.Hidden()
	if more := render.MoreLine(above, below); more != "" && len(lines) >= ch {
		lines = append(lines[:ch-1], m.th.Subtle.Render(more))
	}
	title := fmt.Sprintf("그룹 %d", len(items))
	return render.Box(title, strings.Join(lines, "\n"), r.W, r.H, m.boxStyle(paneGroups))
}

func (m Model) groupLine(g groupRow, cursor bool, w int, stale bool) string {
	gl := m.th.Glyphs
	mark := " "
	if _, on := m.selGroups[g.GroupID]; on {
		mark = m.th.Accent.Render(gl.Marked)
	}
	dot := m.colorStyle(g.Color).Render(gl.Bullet)
	if g.Virtual {
		dot = " "
	}
	name := highlightRunes(g.label(), g.Hit, m.th.Accent.Underline(true))
	if g.Virtual {
		name = m.th.Muted.Render(name)
	}
	count := fmt.Sprint(g.Tabs)
	if g.Matches >= 0 {
		count = fmt.Sprintf("%d/%d", g.Matches, g.Tabs)
	}
	var flags []string
	if g.Current {
		flags = append(flags, m.th.Accent.Render("◆"))
	}
	if !g.Collapsed && !g.Virtual {
		flags = append(flags, m.th.Muted.Render("●"))
	}
	if g.Orphan {
		flags = append(flags, m.th.Warn.Render(gl.Warn))
	}
	right := strings.Join(flags, "") + " " + m.th.Muted.Render(count)
	left := mark + dot + " "
	nameW := w - render.Width(left) - render.Width(right) - 1
	line := left + render.Pad(render.Truncate(name, max(1, nameW)), max(1, nameW)) + " " + right
	if cursor {
		return m.cursorLine(line, w, m.focus == paneGroups)
	}
	if stale {
		return m.th.Muted.Render(render.Pad(line, w))
	}
	return render.Pad(line, w)
}

// cursorLine draws the cursor row: a reversed bar in the focused pane, a
// leading glyph in the other one, so the eye finds the active pane first.
func (m Model) cursorLine(line string, w int, focused bool) string {
	if focused {
		return m.th.Selected.Render(render.Pad(render.Truncate(line, w), w))
	}
	return m.th.Bold.Render(render.Pad(render.Truncate(line, w), w))
}

func (m Model) tabsView(r mouse.Rect) string {
	cw, ch := render.BoxContent(r.W, r.H, m.th.Box)
	items := m.tabs.Items()
	start, end := m.tabs.Window()
	var lines []string
	sel := m.tabs.SelectedID()
	for i := start; i < end; i++ {
		t := items[i]
		lines = append(lines, m.tabLine(t, t.key == sel, cw))
	}
	if len(items) == 0 {
		msg := "탭이 없습니다"
		if m.flat {
			msg = "맞는 탭이 없습니다"
		}
		lines = append(lines, m.th.Muted.Render(msg))
	}
	above, below := m.tabs.Hidden()
	if more := render.MoreLine(above, below); more != "" && len(lines) >= ch {
		lines = append(lines[:ch-1], m.th.Subtle.Render(more))
	}
	title := fmt.Sprintf("탭 %d", len(items))
	if m.flat {
		title = fmt.Sprintf("검색 결과 %d", len(items))
	} else if g, ok := m.groups.Selected(); ok {
		title = fmt.Sprintf("%s %s %d", g.label(), m.th.Glyphs.Bullet, len(items))
	}
	return render.Box(title, strings.Join(lines, "\n"), r.W, r.H, m.boxStyle(paneTabs))
}

func (m Model) tabLine(t tabRow, cursor bool, w int) string {
	gl := m.th.Glyphs
	mark := " "
	if _, on := m.selTabs[t.Tab.ID]; on {
		mark = m.th.Accent.Render(gl.Marked)
	}
	var flags []string
	if t.Tab.Active {
		flags = append(flags, m.th.Success.Render("▸"))
	}
	if t.Tab.Pinned {
		flags = append(flags, m.th.Muted.Render("p"))
	}
	if t.Tab.Audible {
		flags = append(flags, m.th.Muted.Render("♪"))
	}
	left := mark + render.PadRight(strings.Join(flags, ""), 2) + " "
	title := highlightRunes(orUntitled(t.Tab.Title), t.Hit, m.th.Accent.Underline(true))
	host := hostOf(t.Tab.URL)
	if m.flat && t.Tab.GroupID != bridge.NoGroup {
		host = m.colorStyle(t.GroupColor).Render("@"+t.GroupTitle) + " " + host
	} else if m.flat {
		host = m.th.Muted.Render("@-") + " " + host
	}
	avail := w - render.Width(left)
	titleW := max(10, avail*55/100)
	if avail-titleW < 8 {
		titleW = avail
	}
	line := left + render.Pad(render.Truncate(title, titleW), titleW)
	if rest := avail - titleW - 1; rest > 0 {
		line += " " + m.th.Muted.Render(render.Truncate(host, rest))
	}
	if cursor {
		return m.cursorLine(line, w, m.focus == paneTabs)
	}
	return render.Pad(line, w)
}

func (m Model) footerView() string {
	fs := m.th.FooterStyle()
	return m.km.Footer(m.activeStack(), m.w, m.keyOpts(), fs)
}

func (m Model) helpView() string {
	bw, bh := min(76, m.w-4), min(30, m.h-2)
	cw, ch := render.BoxContent(bw, bh, m.th.BoxFocus)
	secs := m.km.Help(m.keyOpts(), stacks[0], stacks[1], stacks[4], stacks[2])
	secs = append(secs, keys.Section{ID: "notes", Title: "표시", Notes: []string{
		"◆ 이 tmux window의 그룹  ● Chrome에서 펼쳐진 그룹  ! tmux window가 없는 고아 그룹  ◉ 선택됨",
		"검색: 단어는 fuzzy, @그룹 은 그룹 제목, /단어 는 URL 일부, !단어 는 제외. tab 키로 범위를 바꿉니다.",
		"연결이 끊기면 마지막 데이터를 보여 주고 쓰기 키는 막습니다. 끊긴 동안 보낸 요청은 다시 보내지 않고, 다시 연결된 뒤 반영 여부를 알려 줍니다.",
		"닫기 전에 snapshot을 ~/.local/state/tmux-chrome 에 쓰고, U로 마지막에 닫은 탭들을 원래 그룹에 다시 엽니다.",
	}})
	lines, _ := keys.RenderHelp(secs, cw, ch, m.helpOff, m.th.HelpStyle())
	return render.Box("도움말", strings.Join(lines, "\n"), bw, bh, m.th.BoxFocus)
}
