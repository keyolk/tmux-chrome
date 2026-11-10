package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/input"
	"github.com/keyolk/tuikit/tmux"
	"github.com/keyolk/tuikit/toast"
)

func (m Model) warn(text string) Model {
	m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: text, Level: toast.Warn})
	return m
}

func locator(g groupRow) map[string]any {
	return map[string]any{"group_id": g.GroupID, "name": g.Title}
}

// --- groups ---

// groupEnter expands the group in Chrome and, when a tmux window is bound to
// it, goes to that window too. Switching the window would fire the tmux hook
// that expands the group anyway; asking Chrome directly covers a window
// without the hook and the window the UI is already in.
func (m Model) groupEnter() (Model, tea.Cmd) {
	g, ok := m.groups.Selected()
	if !ok {
		return m, nil
	}
	if g.Virtual {
		return m.warn("그룹 없는 탭은 전환할 대상이 아닙니다. 탭을 고르세요"), nil
	}
	tm := m.tm
	var pane string
	if g.Window != nil {
		pane = g.Window.Pane
	}
	return m.do(op{
		label:     g.label() + "(으)로 전환",
		quitAfter: true,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			if err := bc.Do(ctx, "switch_window", map[string]any{"window_name": g.Title, "locator": locator(g)}, nil); err != nil {
				return result{}, err
			}
			if pane == "" {
				return result{text: g.label() + " 그룹을 펼쳤습니다"}, nil
			}
			if err := tm.Jump(ctx, pane); err != nil {
				return result{}, &bridge.AppError{Msg: "tmux window 이동 실패: " + err.Error()}
			}
			return result{text: g.label() + " window로 이동했습니다"}, nil
		},
	})
}

// groupNew makes a group. Tabs picked with space move into it; with none
// picked it starts with a New Tab. The tab under the cursor is never taken
// implicitly: a cursor left on a search result once moved a tab the user
// did not mean to touch.
func (m Model) groupNew() (Model, tea.Cmd) {
	var tabs []bridge.Tab
	if len(m.selTabs) > 0 {
		tabs = m.targetTabs()
	}
	title := "새 그룹 이름"
	if len(tabs) > 0 {
		title = fmt.Sprintf("새 그룹 이름 (탭 %d개를 넣습니다)", len(tabs))
	}
	ids := tabIDs(tabs)
	m.ov = promptOv{title: title, field: input.Field{Placeholder: "예: ops-k8s"}, submit: func(m Model, name string) (Model, tea.Cmd) {
		if len(ids) == 0 {
			return m.do(op{
				label: name + " 그룹 만들기",
				check: checkGroupExists(name),
				run: func(ctx context.Context, bc bridge.Client) (result, error) {
					err := bc.Do(ctx, "open_tabs", map[string]any{"urls": []string{""}, "title": name}, nil)
					return result{text: name + " 그룹을 만들었습니다"}, err
				},
			})
		}
		return m.do(op{
			label:    fmt.Sprintf("탭 %d개로 %s 그룹 만들기", len(ids), name),
			check:    checkInGroupTitled(ids, name),
			clearSel: true,
			run: func(ctx context.Context, bc bridge.Client) (result, error) {
				var r bridge.BatchResult
				err := bc.Do(ctx, "group_tabs", map[string]any{"tab_ids": ids, "title": name}, &r)
				return result{text: fmt.Sprintf("%s 그룹에 탭 %d개%s", name, r.Grouped, skippedNote(r))}, err
			},
		})
	}}
	return m, nil
}

func (m Model) groupRename() (Model, tea.Cmd) {
	gs := m.targetGroups()
	if len(gs) != 1 {
		return m.warn("이름은 그룹 하나씩 바꿉니다"), nil
	}
	g := gs[0]
	m.ov = promptOv{title: g.label() + " 이름 바꾸기", field: input.New(g.Title), submit: func(m Model, name string) (Model, tea.Cmd) {
		cur, ok := m.groupByID(g.GroupID)
		if !ok {
			return m.warn("그룹이 그사이 사라졌습니다"), nil
		}
		tm := m.tm
		return m.do(op{
			label: fmt.Sprintf("%s → %s 이름 바꾸기", cur.Title, name),
			check: checkGroupTitle(cur.GroupID, name),
			run: func(ctx context.Context, bc bridge.Client) (result, error) {
				if err := bc.Do(ctx, "update_group", map[string]any{"group_id": cur.GroupID, "expected_title": cur.Title, "title": name}, nil); err != nil {
					return result{}, err
				}
				if cur.Window == nil {
					return result{text: name + "(으)로 바꿨습니다"}, nil
				}
				// The window name is the binding. Store the new title first:
				// the rename hook then finds them equal and leaves Chrome be.
				if err := renameWindow(ctx, tm, cur.Window.ID, name, cur.GroupID); err != nil {
					return result{}, &bridge.AppError{Msg: "그룹은 바꿨지만 tmux window 이름은 못 바꿨습니다: " + err.Error()}
				}
				return result{text: name + "(으)로 바꾸고 tmux window 이름도 맞췄습니다"}, nil
			},
		})
	}}
	return m, nil
}

func renameWindow(ctx context.Context, tm tmux.Client, win, name string, groupID int) error {
	if err := tm.SetOption(ctx, tmux.ScopeWindow, win, "@chrome_group", name); err != nil {
		return err
	}
	if err := tm.SetOption(ctx, tmux.ScopeWindow, win, "@chrome_group_id", fmt.Sprint(groupID)); err != nil {
		return err
	}
	_, err := tm.Run("rename-window", "-t", win, name)
	return err
}

func (m Model) groupColor() (Model, tea.Cmd) {
	gs := m.targetGroups()
	if len(gs) == 0 {
		return m, nil
	}
	ids := make([]int, len(gs))
	for i, g := range gs {
		ids[i] = g.GroupID
	}
	items := make([]pickItem, len(bridge.Colors))
	for i, c := range bridge.Colors {
		items[i] = pickItem{key: c, label: m.colorStyle(c).Render(c)}
	}
	m.ov = newPicker(fmt.Sprintf("그룹 %d개 색", len(gs)), items, func(m Model, it pickItem) (Model, tea.Cmd) {
		color := it.key
		var live []groupRow
		for _, id := range ids {
			if g, ok := m.groupByID(id); ok {
				live = append(live, g)
			}
		}
		return m.do(op{
			label: fmt.Sprintf("그룹 %d개를 %s(으)로", len(live), color),
			check: func(s bridge.Snapshot) bool {
				for _, g := range live {
					if !checkGroupColor(g.GroupID, color)(s) {
						return false
					}
				}
				return true
			},
			clearSel: true,
			run: func(ctx context.Context, bc bridge.Client) (result, error) {
				for _, g := range live {
					if err := bc.Do(ctx, "update_group", map[string]any{"group_id": g.GroupID, "expected_title": g.Title, "color": color}, nil); err != nil {
						return result{}, err
					}
				}
				return result{text: fmt.Sprintf("그룹 %d개를 %s(으)로 바꿨습니다", len(live), color)}, nil
			},
		})
	})
	return m, nil
}

// groupBind binds the UI's own tmux window to the group, the way
// `tmux-chrome rebind-current-window` does: options only, the window keeps
// its name. Only tmux changes, so it works while Chrome is unreachable.
func (m Model) groupBind() (Model, tea.Cmd) {
	g, ok := m.groups.Selected()
	if !ok || g.Virtual {
		return m, nil
	}
	if m.cfg.Window == "" {
		return m.warn("tmux 밖에서는 바인딩할 window가 없습니다"), nil
	}
	tm, win := m.tm, m.cfg.Window
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, kv := range [][2]string{
			{"@chrome_group", g.Title}, {"@chrome_group_id", fmt.Sprint(g.GroupID)}, {"@chrome_window_id", fmt.Sprint(g.WindowID)},
		} {
			if err := tm.SetOption(ctx, tmux.ScopeWindow, win, kv[0], kv[1]); err != nil {
				return externMsg{label: "바인딩", err: err}
			}
		}
		return externMsg{label: "바인딩", out: fmt.Sprintf("이 tmux window를 %s 그룹에 바인딩했습니다", g.label()), refetch: true}
	}
}

func (m Model) groupMerge() (Model, tea.Cmd) {
	src := m.targetGroups()
	if len(src) == 0 {
		return m, nil
	}
	skip := map[int]bool{}
	for _, g := range src {
		skip[g.GroupID] = true
	}
	var items []pickItem
	for _, g := range m.all {
		if !g.Virtual && !skip[g.GroupID] {
			items = append(items, pickItem{key: groupKey(g.GroupID), label: g.label(), detail: fmt.Sprintf("탭 %d", g.Tabs)})
		}
	}
	m.ov = newPicker(fmt.Sprintf("그룹 %d개를 어디로 합칠까요", len(src)), items, func(m Model, it pickItem) (Model, tea.Cmd) {
		dst, ok := m.groupByKey(it.key)
		if !ok {
			return m.warn("대상 그룹이 그사이 사라졌습니다"), nil
		}
		var names []string
		for _, g := range src {
			names = append(names, g.label())
		}
		m.ov = confirmOv{
			title: fmt.Sprintf("%s(으)로 합치기", dst.label()),
			lines: append([]string{"이 그룹들의 탭을 옮깁니다 (고정 탭은 남습니다):"}, names...),
			yes: func(m Model) (Model, tea.Cmd) {
				tabs := tabsOfGroups(m.snap, skip)
				ids := tabIDs(tabs)
				if len(ids) == 0 {
					return m.warn("옮길 탭이 없습니다"), nil
				}
				return m.moveTabs(ids, dst)
			},
		}
		return m, nil
	})
	return m, nil
}

func (m Model) groupByKey(key string) (groupRow, bool) {
	for _, g := range m.all {
		if g.key == key {
			return g, true
		}
	}
	return groupRow{}, false
}

func (m Model) groupUngroup() (Model, tea.Cmd) {
	gs := m.targetGroups()
	if len(gs) == 0 {
		return m, nil
	}
	var lines []string
	ids := make([]int, len(gs))
	for i, g := range gs {
		ids[i] = g.GroupID
		lines = append(lines, fmt.Sprintf("%s  (탭 %d개는 그룹 없이 남습니다)", g.label(), g.Tabs))
	}
	m.ov = confirmOv{title: fmt.Sprintf("그룹 %d개 해제", len(gs)), lines: lines, yes: func(m Model) (Model, tea.Cmd) {
		var live []groupRow
		for _, id := range ids {
			if g, ok := m.groupByID(id); ok {
				live = append(live, g)
			}
		}
		return m.do(op{
			label:    fmt.Sprintf("그룹 %d개 해제", len(live)),
			check:    checkGroupsGone(ids),
			clearSel: true,
			run: func(ctx context.Context, bc bridge.Client) (result, error) {
				for _, g := range live {
					if err := bc.Do(ctx, "delete_group", map[string]any{"name": g.Title, "locator": locator(g)}, nil); err != nil {
						return result{}, err
					}
				}
				return result{text: fmt.Sprintf("그룹 %d개를 해제했습니다", len(live))}, nil
			},
		})
	}}
	return m, nil
}

func (m Model) groupClose() (Model, tea.Cmd) {
	gs := m.targetGroups()
	if len(gs) == 0 {
		return m, nil
	}
	ids := map[int]bool{}
	var names []string
	for _, g := range gs {
		ids[g.GroupID] = true
		names = append(names, g.label())
	}
	tabs := tabsOfGroups(m.snap, ids)
	m.ov = confirmOv{
		title: fmt.Sprintf("그룹 %d개와 탭 %d개 닫기", len(gs), len(tabs)),
		lines: append([]string{"그룹: " + strings.Join(names, ", "), ""}, tabLines(tabs)...),
		yes: func(m Model) (Model, tea.Cmd) {
			return m.closeTabs(tabsOfGroups(m.snap, ids), true)
		},
	}
	return m, nil
}

// openURL opens a URL in the group under the cursor (or loose, from the
// "no group" row).
func (m Model) openURL() (Model, tea.Cmd) {
	g, _ := m.groups.Selected()
	if m.focus == paneTabs {
		if t, ok := m.tabs.Selected(); ok {
			if gg, ok := m.groupByID(t.Tab.GroupID); ok {
				g = gg
			}
		}
	}
	where := "그룹 없이"
	if !g.Virtual && g.key != "" {
		where = g.label() + " 그룹에"
	}
	m.ov = promptOv{title: "URL 열기 (" + where + ")", field: input.Field{Placeholder: "https://…"}, submit: func(m Model, raw string) (Model, tea.Cmd) {
		u := normalizeURL(raw)
		return m.openURLs([]string{u}, g)
	}}
	return m, nil
}

// normalizeURL adds https:// to a bare host the way an address bar would.
func normalizeURL(raw string) string {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "about:") || strings.HasPrefix(raw, "chrome:") {
		return raw
	}
	return "https://" + raw
}

func (m Model) openURLs(urls []string, g groupRow) (Model, tea.Cmd) {
	params := map[string]any{"urls": urls}
	where := "그룹 없이"
	if !g.Virtual && g.key != "" {
		params["group_id"] = g.GroupID
		params["title"] = g.Title
		where = g.label() + " 그룹에"
	}
	return m.do(op{
		label: fmt.Sprintf("%s 탭 %d개 열기", where, len(urls)),
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var r bridge.BatchResult
			err := bc.Do(ctx, "open_tabs", params, &r)
			return result{text: fmt.Sprintf("%s 탭 %d개를 열었습니다", where, r.Opened)}, err
		},
	})
}

// --- tabs ---

func (m Model) tabFocus() (Model, tea.Cmd) {
	t, ok := m.tabs.Selected()
	if !ok {
		return m, nil
	}
	id, title := t.Tab.ID, orUntitled(t.Tab.Title)
	return m.do(op{
		label:     "탭으로 이동",
		quitAfter: true,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			err := bc.Do(ctx, "focus_tab", map[string]any{"tab_id": id}, nil)
			return result{text: title + "(으)로 이동했습니다"}, err
		},
	})
}

const newGroupKey = "new"

func (m Model) tabMove() (Model, tea.Cmd) {
	tabs := m.targetTabs()
	if len(tabs) == 0 {
		return m, nil
	}
	ids := tabIDs(tabs)
	from := map[int]bool{}
	for _, t := range tabs {
		from[t.GroupID] = true
	}
	items := []pickItem{{key: newGroupKey, label: "+ 새 그룹" + "…"}}
	for _, g := range m.all {
		if g.Virtual || (len(from) == 1 && from[g.GroupID]) {
			continue
		}
		items = append(items, pickItem{key: groupKey(g.GroupID), label: g.label(), detail: fmt.Sprintf("탭 %d", g.Tabs)})
	}
	m.ov = newPicker(fmt.Sprintf("탭 %d개를 어느 그룹으로", len(ids)), items, func(m Model, it pickItem) (Model, tea.Cmd) {
		if it.key == newGroupKey {
			m.ov = promptOv{title: "새 그룹 이름", field: input.Field{}, submit: func(m Model, name string) (Model, tea.Cmd) {
				return m.do(op{
					label:    fmt.Sprintf("탭 %d개로 %s 그룹 만들기", len(ids), name),
					check:    checkInGroupTitled(ids, name),
					clearSel: true,
					run: func(ctx context.Context, bc bridge.Client) (result, error) {
						var r bridge.BatchResult
						err := bc.Do(ctx, "group_tabs", map[string]any{"tab_ids": ids, "title": name}, &r)
						return result{text: fmt.Sprintf("%s 그룹에 탭 %d개%s", name, r.Grouped, skippedNote(r))}, err
					},
				})
			}}
			return m, nil
		}
		dst, ok := m.groupByKey(it.key)
		if !ok {
			return m.warn("대상 그룹이 그사이 사라졌습니다"), nil
		}
		return m.moveTabs(ids, dst)
	})
	return m, nil
}

func (m Model) moveTabs(ids []int, dst groupRow) (Model, tea.Cmd) {
	return m.do(op{
		label:    fmt.Sprintf("탭 %d개를 %s(으)로", len(ids), dst.label()),
		check:    checkInGroup(ids, dst.GroupID),
		clearSel: true,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var r bridge.BatchResult
			err := bc.Do(ctx, "group_tabs", map[string]any{"tab_ids": ids, "group_id": dst.GroupID, "expected_title": dst.Title}, &r)
			return result{text: fmt.Sprintf("%s(으)로 탭 %d개를 옮겼습니다%s", dst.label(), r.Grouped, skippedNote(r))}, err
		},
	})
}

func (m Model) tabUngroup() (Model, tea.Cmd) {
	tabs := m.targetTabs()
	if len(tabs) == 0 {
		return m, nil
	}
	ids := tabIDs(tabs)
	return m.do(op{
		label:    fmt.Sprintf("탭 %d개를 그룹에서 빼기", len(ids)),
		check:    checkInGroup(ids, bridge.NoGroup),
		clearSel: true,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var r bridge.BatchResult
			err := bc.Do(ctx, "ungroup_tabs", map[string]any{"tab_ids": ids}, &r)
			return result{text: fmt.Sprintf("탭 %d개를 그룹에서 뺐습니다%s", r.Ungrouped, skippedNote(r))}, err
		},
	})
}

// tabClose closes one tab at once and asks first for more than one.
func (m Model) tabClose() (Model, tea.Cmd) {
	tabs := m.targetTabs()
	switch len(tabs) {
	case 0:
		return m, nil
	case 1:
		return m.closeTabs(tabs, true)
	}
	ids := tabIDs(tabs)
	m.ov = confirmOv{title: fmt.Sprintf("탭 %d개 닫기", len(tabs)), lines: tabLines(tabs), yes: func(m Model) (Model, tea.Cmd) {
		var live []bridge.Tab
		for _, id := range ids {
			if t, ok := m.tabByID(id); ok {
				live = append(live, t)
			}
		}
		return m.closeTabs(live, true)
	}}
	return m, nil
}

// closeTabs closes tabs that still show the URL they showed when picked.
// force drops the active/pinned/audible guards: the user picked these by
// hand. Stale cleanup passes false and keeps them.
func (m Model) closeTabs(tabs []bridge.Tab, force bool) (Model, tea.Cmd) {
	if len(tabs) == 0 {
		return m.warn("닫을 탭이 없습니다"), nil
	}
	reqs := make([]bridge.CloseRequest, len(tabs))
	closing := make([]closedTab, len(tabs))
	for i, t := range tabs {
		reqs[i] = bridge.CloseRequest{TabID: t.ID, ExpectedURL: t.URL}
		closing[i] = m.closedTabOf(t)
	}
	ids := tabIDs(tabs)
	return m.do(op{
		label:    fmt.Sprintf("탭 %d개 닫기", len(tabs)),
		check:    checkGone(ids),
		closing:  closing,
		clearSel: true,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var r bridge.BatchResult
			if err := bc.Do(ctx, "close_tabs", map[string]any{"tabs": reqs, "force": force}, &r); err != nil {
				return result{}, err
			}
			return result{text: fmt.Sprintf("탭 %d개를 닫았습니다 (U로 되살리기)%s", len(r.Closed), skippedNote(r)), closed: r.Closed}, nil
		},
	})
}

func (m Model) tabPin() (Model, tea.Cmd) {
	tabs := m.targetTabs()
	if len(tabs) == 0 {
		return m, nil
	}
	pin := false
	for _, t := range tabs {
		pin = pin || !t.Pinned
	}
	ids := tabIDs(tabs)
	verb := "고정"
	if !pin {
		verb = "고정 해제"
	}
	return m.do(op{
		label:    fmt.Sprintf("탭 %d개 %s", len(ids), verb),
		check:    checkPinned(ids, pin),
		clearSel: true,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var r bridge.BatchResult
			err := bc.Do(ctx, "pin_tabs", map[string]any{"tab_ids": ids, "pinned": pin}, &r)
			return result{text: fmt.Sprintf("탭 %d개를 %s했습니다%s", r.Changed, verb, skippedNote(r))}, err
		},
	})
}

func (m Model) tabReload() (Model, tea.Cmd) {
	tabs := m.targetTabs()
	if len(tabs) == 0 {
		return m, nil
	}
	ids := tabIDs(tabs)
	return m.do(op{
		label: fmt.Sprintf("탭 %d개 새로고침", len(ids)),
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var r bridge.BatchResult
			err := bc.Do(ctx, "reload_tabs", map[string]any{"tab_ids": ids}, &r)
			return result{text: fmt.Sprintf("탭 %d개를 새로고침했습니다%s", r.Reloaded, skippedNote(r))}, err
		},
	})
}

func (m Model) tabCopy() (Model, tea.Cmd) {
	tabs := m.targetTabs()
	if len(tabs) == 0 {
		return m, nil
	}
	urls := make([]string, len(tabs))
	for i, t := range tabs {
		urls[i] = t.URL
	}
	return m, copyCmd(strings.Join(urls, "\n"), len(urls))
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host + u.EscapedPath()
}
