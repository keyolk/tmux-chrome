package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/toast"
)

// op is one write. Every write goes through do, which is where the rules
// live: none while disconnected, progress shown, a refusal reported, and an
// unknown outcome checked against the next snapshot instead of retried.
type op struct {
	label string
	run   func(ctx context.Context, bc bridge.Client) (result, error)
	// check says whether the write is visible in a snapshot. It decides an
	// unknown outcome after reconnecting; nil means there is nothing to check
	// (focusing a tab, say).
	check func(s bridge.Snapshot) bool
	// closing lists the tabs this op closes, for the undo record and the
	// snapshot written first.
	closing   []closedTab
	quitAfter bool
	clearSel  bool
}

// result is what a write reports: a toast line, and for a close, the tabs
// Chrome actually closed so the undo record lists exactly those.
type result struct {
	text   string
	closed []int
}

type opDoneMsg struct {
	op     op
	text   string
	err    error
	closed []int
}

// pendingCheck is a write whose outcome is unknown, waiting for a snapshot.
type pendingCheck struct {
	label string
	check func(bridge.Snapshot) bool
}

func (m Model) do(o op) (Model, tea.Cmd) {
	if m.conn != stConnected {
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "conn", Text: "Chrome과 연결이 끊겨 실행하지 않았습니다", Level: toast.Warn})
		return m, nil
	}
	m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: o.label + "…", Level: toast.Progress})
	bc, dir, snap := m.bc, m.cfg.StateDir, m.snap
	return m, func() tea.Msg {
		if len(o.closing) > 0 && snap != nil {
			// Before closing anything, leave what `tmux-chrome restore`
			// can rebuild from. A failure here stops the close.
			if err := writeSnapshotFile(dir, *snap, time.Now()); err != nil {
				return opDoneMsg{op: o, err: &prepError{err}}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		res, err := o.run(ctx, bc)
		return opDoneMsg{op: o, text: res.text, err: err, closed: res.closed}
	}
}

// prepError is a failure before the request was sent: nothing happened.
type prepError struct{ err error }

func (e *prepError) Error() string { return "준비 실패: " + e.err.Error() }

func (m Model) onOpDone(msg opDoneMsg) (Model, tea.Cmd) {
	o := msg.op
	var cmds []tea.Cmd
	var prep *prepError
	switch {
	case msg.err == nil:
		text := msg.text
		if text == "" {
			text = o.label + " 완료"
		}
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: text, Level: toast.Success})
		if o.clearSel {
			m.selTabs, m.selGroups = map[int]string{}, map[int]string{}
		}
		if len(o.closing) > 0 && len(msg.closed) > 0 {
			var c tea.Cmd
			m, c = m.recordClosed(o.closing, msg.closed)
			cmds = append(cmds, c)
		}
		if o.quitAfter && m.cfg.Popup {
			mm, c := m.quit()
			return mm, tea.Batch(append(cmds, c)...)
		}
	case asPrep(msg.err, &prep):
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: o.label + ": " + prep.Error(), Level: toast.Error})
	case bridge.Unknown(msg.err):
		m.logf("%s: outcome unknown: %v", o.label, msg.err)
		if o.check == nil {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: o.label + ": 결과를 알 수 없습니다", Level: toast.Warn})
			break
		}
		m.pending = append(m.pending, pendingCheck{label: o.label, check: o.check})
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: o.label + ": 연결 문제로 결과 확인 중", Level: toast.Progress})
		if m.conn == stConnected {
			var c tea.Cmd
			m, c = m.fetch()
			cmds = append(cmds, c)
		}
	default:
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: o.label + " 실패: " + msg.err.Error(), Level: toast.Error})
	}
	return m, tea.Batch(cmds...)
}

func asPrep(err error, out **prepError) bool {
	p, ok := err.(*prepError)
	if ok {
		*out = p
	}
	return ok
}

// settlePending judges unknown outcomes against a fresh snapshot. A write
// that did not land is reported, never re-sent: the user decides.
func (m Model) settlePending() Model {
	if len(m.pending) == 0 || m.snap == nil || m.conn != stConnected {
		return m
	}
	for _, p := range m.pending {
		if p.check(*m.snap) {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: p.label + ": 반영됨", Level: toast.Success})
		} else {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "op", Text: p.label + ": 반영되지 않음. 다시 실행하세요", Level: toast.Error})
		}
	}
	m.pending = nil
	return m
}

// --- targets, re-resolved against the newest snapshot ---

func (m Model) tabByID(id int) (bridge.Tab, bool) {
	if m.snap == nil {
		return bridge.Tab{}, false
	}
	for _, t := range m.snap.Tabs {
		if t.ID == id {
			return t, true
		}
	}
	return bridge.Tab{}, false
}

func (m Model) groupByID(id int) (groupRow, bool) {
	for _, g := range m.all {
		if g.GroupID == id && !g.Virtual {
			return g, true
		}
	}
	return groupRow{}, false
}

// targetTabs is the selection if there is one, else the tab under the
// cursor. Ids that vanished since they were picked are dropped.
func (m Model) targetTabs() []bridge.Tab {
	var out []bridge.Tab
	if len(m.selTabs) > 0 {
		for _, t := range snapTabs(m.snap) {
			if _, ok := m.selTabs[t.ID]; ok {
				out = append(out, t)
			}
		}
		return out
	}
	if t, ok := m.tabs.Selected(); ok {
		if cur, ok := m.tabByID(t.Tab.ID); ok {
			out = append(out, cur)
		}
	}
	return out
}

func (m Model) targetGroups() []groupRow {
	var out []groupRow
	if len(m.selGroups) > 0 {
		for _, g := range m.all {
			if _, ok := m.selGroups[g.GroupID]; ok && !g.Virtual {
				out = append(out, g)
			}
		}
		return out
	}
	if g, ok := m.groups.Selected(); ok && !g.Virtual {
		if cur, ok := m.groupByID(g.GroupID); ok {
			out = append(out, cur)
		}
	}
	return out
}

func snapTabs(s *bridge.Snapshot) []bridge.Tab {
	if s == nil {
		return nil
	}
	return s.Tabs
}

func tabsOfGroups(s *bridge.Snapshot, ids map[int]bool) []bridge.Tab {
	var out []bridge.Tab
	for _, t := range snapTabs(s) {
		if ids[t.GroupID] {
			out = append(out, t)
		}
	}
	return out
}

func tabIDs(tabs []bridge.Tab) []int {
	ids := make([]int, len(tabs))
	for i, t := range tabs {
		ids[i] = t.ID
	}
	return ids
}

func tabLines(tabs []bridge.Tab) []string {
	out := make([]string, len(tabs))
	for i, t := range tabs {
		out[i] = fmt.Sprintf("%s  %s", orUntitled(t.Title), hostOf(t.URL))
	}
	return out
}

func orUntitled(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(제목 없음)"
	}
	return s
}

// --- checks for unknown outcomes ---

func checkGone(ids []int) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		want := map[int]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for _, t := range s.Tabs {
			if want[t.ID] {
				return false
			}
		}
		return true
	}
}

func checkInGroup(ids []int, groupID int) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		want := map[int]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for _, t := range s.Tabs {
			if want[t.ID] && t.GroupID != groupID {
				return false
			}
		}
		return true
	}
}

func checkInGroupTitled(ids []int, title string) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		titles := map[int]string{}
		for _, g := range s.Groups {
			titles[g.ID] = g.Title
		}
		want := map[int]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for _, t := range s.Tabs {
			if want[t.ID] && titles[t.GroupID] != title {
				return false
			}
		}
		return true
	}
}

func checkGroupTitle(id int, title string) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		for _, g := range s.Groups {
			if g.ID == id {
				return g.Title == title
			}
		}
		return false
	}
}

func checkGroupColor(id int, color string) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		for _, g := range s.Groups {
			if g.ID == id {
				return g.Color == color
			}
		}
		return false
	}
}

func checkGroupsGone(ids []int) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		want := map[int]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for _, g := range s.Groups {
			if want[g.ID] {
				return false
			}
		}
		return true
	}
}

func checkGroupExists(title string) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		for _, g := range s.Groups {
			if g.Title == title {
				return true
			}
		}
		return false
	}
}

func checkPinned(ids []int, pinned bool) func(bridge.Snapshot) bool {
	return func(s bridge.Snapshot) bool {
		want := map[int]bool{}
		for _, id := range ids {
			want[id] = true
		}
		for _, t := range s.Tabs {
			if want[t.ID] && t.Pinned != pinned {
				return false
			}
		}
		return true
	}
}

// skippedNote turns a batch's skipped list into a short suffix.
func skippedNote(r bridge.BatchResult) string {
	if len(r.Skipped) == 0 {
		return ""
	}
	reasons := map[string]int{}
	for _, s := range r.Skipped {
		reasons[s.Reason]++
	}
	var parts []string
	for reason, n := range reasons {
		parts = append(parts, fmt.Sprintf("%s %d", reason, n))
	}
	return " (건너뜀: " + strings.Join(parts, ", ") + ")"
}
