package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/render"
	"github.com/keyolk/tuikit/theme"
)

func testModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	m := New(Config{NoConnect: true, Now: func() time.Time { return now }, Theme: theme.Theme{Glyphs: theme.ASCII()}})
	m.w, m.h = 100, 30
	return m
}

func snap() bridge.Snapshot {
	return bridge.Snapshot{
		Epoch: "e1", Seq: 10,
		Groups: []bridge.Group{
			{ID: 1, Title: "ops-k8s", Color: "blue", WindowID: 1},
			{ID: 2, Title: "tweb", Color: "red", WindowID: 1},
			{ID: 3, Title: "native", Color: "grey", WindowID: 1, Collapsed: true},
		},
		Tabs: []bridge.Tab{
			{ID: 11, Title: "Argo CD", URL: "https://argocd.example/app", GroupID: 1, WindowID: 1, Index: 0},
			{ID: 12, Title: "Grafana envoy", URL: "https://grafana.example/d/1", GroupID: 1, WindowID: 1, Index: 1},
			{ID: 21, Title: "PR 20", URL: "https://github.com/keyolk/tmux-chrome/pull/20", GroupID: 2, WindowID: 1, Index: 2},
			{ID: 31, Title: "New Tab", URL: "chrome://newtab/", GroupID: 3, WindowID: 1, Index: 3},
			{ID: 41, Title: "loose", URL: "https://example.com/", GroupID: bridge.NoGroup, WindowID: 1, Index: 4},
		},
	}
}

func step(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(msg)
	return mm.(Model), cmd
}

func connected(t *testing.T) Model {
	m := testModel(t)
	m.wins = []tmuxWindow{{ID: "@1", Name: "ops-k8s", Pane: "%1"}, {ID: "@2", Name: "tweb", Pane: "%2"}}
	m.cfg.Window = "@1"
	m.conn = stConnected
	m = m.apply(snap())
	return m
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestKeymapHasNoConflicts(t *testing.T) {
	if c := keymap().Conflicts(stacks...); len(c) > 0 {
		t.Fatalf("conflicts: %+v", c)
	}
}

func TestParseQuery(t *testing.T) {
	q := parseQuery("argo @ops /grafana !old @ ")
	if strings.Join(q.terms, ",") != "argo" || q.groups[0] != "ops" || q.urls[0] != "grafana" || q.excludes[0] != "old" {
		t.Fatalf("%+v", q)
	}
}

func TestNormalizeTitleMatchesBashAndExtension(t *testing.T) {
	cases := map[string]string{"⏳ Ops-K8s": "opsk8s", "🟢tweb": "tweb", "ＡＢＣ 1": "abc1", "한글": ""}
	for in, want := range cases {
		if got := normalizeTitle(in); got != want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildGroupsMarksCurrentOrphanAndUngrouped(t *testing.T) {
	m := connected(t)
	byTitle := map[string]groupRow{}
	for _, g := range m.all {
		byTitle[g.label()] = g
	}
	if !byTitle["ops-k8s"].Current || byTitle["tweb"].Current {
		t.Error("current group wrong")
	}
	if byTitle["native"].Orphan {
		t.Error("the reserved native group is never an orphan")
	}
	if u := byTitle["(그룹 없음)"]; !u.Virtual || u.Tabs != 1 {
		t.Errorf("ungrouped row = %+v", u)
	}
}

func TestSearchFlattensAcrossGroups(t *testing.T) {
	m := connected(t)
	m.field = m.field.SetValue("grafana")
	m = m.relayout()
	if !m.flat || m.tabs.Len() != 1 || m.tabs.At(0).Tab.ID != 12 {
		t.Fatalf("flat=%v tabs=%v", m.flat, m.tabs.Items())
	}
	if m.groups.Len() != 1 || m.groups.At(0).Matches != 1 {
		t.Fatalf("groups=%+v", m.groups.Items())
	}
	m.field = m.field.SetValue("@tweb")
	m = m.relayout()
	if m.tabs.Len() != 1 || m.tabs.At(0).Tab.ID != 21 {
		t.Fatalf("@tweb tabs=%v", m.tabs.Items())
	}
	m.sc = scopeGroups
	m.field = m.field.SetValue("tw")
	m = m.relayout()
	if m.flat || m.groups.Len() != 1 {
		t.Fatalf("group scope: flat=%v groups=%d", m.flat, m.groups.Len())
	}
}

func TestCursorFollowsIDAcrossSnapshots(t *testing.T) {
	m := connected(t)
	m.groups, _ = m.groups.Select(groupKey(2))
	m = m.syncTabs()
	s := snap()
	s.Seq = 11
	s.Groups = append([]bridge.Group{{ID: 9, Title: "new", WindowID: 1}}, s.Groups...)
	s.Tabs = append(s.Tabs, bridge.Tab{ID: 91, GroupID: 9, WindowID: 1, Index: -1})
	m = m.apply(s)
	if g, _ := m.groups.Selected(); g.GroupID != 2 {
		t.Fatalf("cursor moved to %d", g.GroupID)
	}
}

func TestChromeRestartRekeysSelectionByURL(t *testing.T) {
	m := connected(t)
	m.selTabs = map[int]string{12: "https://grafana.example/d/1"}
	s := snap()
	s.Epoch = "e2"
	for i := range s.Tabs {
		s.Tabs[i].ID += 100
	}
	m = m.apply(s)
	if _, ok := m.selTabs[112]; !ok || len(m.selTabs) != 1 {
		t.Fatalf("selTabs = %v", m.selTabs)
	}
}

func TestWritesRefusedWhileDisconnected(t *testing.T) {
	m := connected(t)
	m.conn = stReconnecting
	m.focus = paneTabs
	m, cmd := step(t, m, key("x"))
	if cmd != nil {
		// Only a toast tick may come back; no request.
		for _, msg := range drain(cmd) {
			if _, ok := msg.(opDoneMsg); ok {
				t.Fatal("a close was sent while disconnected")
			}
		}
	}
	if !hasToast(m, "끊겨") {
		t.Fatalf("toasts = %+v", m.toasts.Items())
	}
}

func TestReadsWorkWhileDisconnected(t *testing.T) {
	m := connected(t)
	m.conn = stReconnecting
	before := m.groups.SelectedID()
	m, _ = step(t, m, key("j"))
	if m.groups.SelectedID() == before {
		t.Fatal("j did nothing while disconnected")
	}
	m, _ = step(t, m, key("/"))
	if !m.searching {
		t.Fatal("search did not open while disconnected")
	}
}

func TestStaleFeedResultIsDropped(t *testing.T) {
	m := connected(t)
	m.connGen = 5
	m, cmd := step(t, m, feedLostMsg{gen: 4, err: errors.New("old feed")})
	if m.conn != stConnected || cmd != nil {
		t.Fatalf("an old feed's end tore down the current one: conn=%v", m.conn)
	}
}

func TestFeedLossBacksOffThenRevivesOnce(t *testing.T) {
	m := connected(t)
	m.connGen = 1
	m, _ = step(t, m, feedLostMsg{gen: 1, err: errors.New("EOF")})
	if m.conn != stReconnecting || m.attempt != 1 || m.downSince.IsZero() {
		t.Fatalf("conn=%v attempt=%d", m.conn, m.attempt)
	}
	// Chrome is up but the feed has been down past reviveAfter: revive.
	m.downSince = m.now().Add(-reviveAfter - time.Second)
	m, cmd := step(t, m, chromeMsg{gen: 1, running: true})
	if m.conn != stReviving || !m.revived || cmd == nil {
		t.Fatalf("conn=%v revived=%v", m.conn, m.revived)
	}
	// After a revive the loop reconnects instead of reviving again.
	m.conn = stReconnecting
	m, _ = step(t, m, chromeMsg{gen: m.connGen, running: true})
	if m.conn == stReviving {
		t.Fatal("revived twice in one outage")
	}
}

func TestChromeGoneHalts(t *testing.T) {
	m := connected(t)
	m.conn = stReconnecting
	m, _ = step(t, m, chromeMsg{gen: m.connGen, running: false})
	if m.conn != stHalted {
		t.Fatalf("conn = %v", m.conn)
	}
	if b, ok := m.km.Resolve(m.stack(), key("enter"), m.keyOpts()); !ok || b.Action != "chrome.launch" {
		t.Fatalf("enter = %+v, want chrome.launch while halted", b)
	}
}

func TestOldExtensionIsLegacyNotRetried(t *testing.T) {
	m := testModel(t)
	m.connGen = 1
	m, cmd := step(t, m, connFailedMsg{gen: 1, err: bridge.ErrUnsupported})
	if m.conn != stLegacy {
		t.Fatalf("conn = %v", m.conn)
	}
	for _, msg := range drain(cmd) {
		if _, ok := msg.(retryMsg); ok {
			t.Fatal("legacy extension scheduled a retry")
		}
	}
}

func TestEventAlreadyInSnapshotIsIgnored(t *testing.T) {
	m := connected(t)
	m.connGen = 1
	tok := m.refetchTok
	m, _ = step(t, m, eventMsg{gen: 1, ev: bridge.Event{Epoch: "e1", Seq: 9}})
	if m.refetchTok != tok {
		t.Fatal("refetched for an event the snapshot already had")
	}
	m, _ = step(t, m, eventMsg{gen: 1, ev: bridge.Event{Epoch: "e1", Seq: 11}})
	if m.refetchTok == tok {
		t.Fatal("did not refetch for a new event")
	}
}

func TestUnknownOutcomeIsCheckedNotResent(t *testing.T) {
	m := connected(t)
	o := op{label: "탭 1개 닫기", check: checkGone([]int{12})}
	m, _ = step(t, m, opDoneMsg{op: o, err: errors.New("read from bridge: EOF")})
	if len(m.pending) != 1 {
		t.Fatalf("pending = %d", len(m.pending))
	}
	// The tab is still there in the next snapshot: report, do not resend.
	m, cmd := step(t, m, snapshotMsg{id: 0, snap: snap(), wins: m.wins})
	if len(m.pending) != 0 || !hasToast(m, "반영되지 않음") {
		t.Fatalf("pending=%d toasts=%+v", len(m.pending), m.toasts.Items())
	}
	for _, msg := range drain(cmd) {
		if _, ok := msg.(opDoneMsg); ok {
			t.Fatal("the write was re-sent")
		}
	}
}

func TestRefusalIsReportedAsFailure(t *testing.T) {
	m := connected(t)
	m, _ = step(t, m, opDoneMsg{op: op{label: "이름 바꾸기", check: checkGroupTitle(1, "x")}, err: &bridge.AppError{Msg: "Tab group no longer exists"}})
	if len(m.pending) != 0 || !hasToast(m, "실패") {
		t.Fatalf("pending=%d toasts=%+v", len(m.pending), m.toasts.Items())
	}
}

func TestMultiCloseAsksFirstAndReresolves(t *testing.T) {
	m := connected(t)
	m.focus = paneTabs
	m, _ = step(t, m, key(" "))
	m, _ = step(t, m, key(" "))
	if len(m.selTabs) != 2 {
		t.Fatalf("selTabs = %v", m.selTabs)
	}
	m, _ = step(t, m, key("x"))
	if _, ok := m.ov.(confirmOv); !ok {
		t.Fatalf("overlay = %T, want confirm", m.ov)
	}
	// One of the two closes elsewhere before the user confirms.
	s := snap()
	s.Seq = 12
	s.Tabs = s.Tabs[1:]
	m = m.apply(s)
	m, cmd := step(t, m, key("y"))
	if cmd == nil {
		t.Fatal("confirm sent nothing")
	}
	if !hasToast(m, "탭 1개 닫기") {
		t.Fatalf("the vanished tab was still a target: %+v", m.toasts.Items())
	}
}

func TestPromptOwnsLetters(t *testing.T) {
	m := connected(t)
	m, _ = step(t, m, key("r"))
	if _, ok := m.ov.(promptOv); !ok {
		t.Fatalf("overlay = %T", m.ov)
	}
	m, _ = step(t, m, key("q"))
	if m.ov == nil {
		t.Fatal("q in a prompt closed it instead of typing")
	}
	if v := m.ov.(promptOv).field.Value(); v != "ops-k8sq" {
		t.Fatalf("field = %q", v)
	}
}

func TestViewFitsAndDegrades(t *testing.T) {
	m := connected(t)
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 20}} {
		m.w, m.h = sz[0], sz[1]
		m = m.sizeLists()
		lines := strings.Split(m.View(), "\n")
		if len(lines) != m.h {
			t.Errorf("%dx%d: %d lines", sz[0], sz[1], len(lines))
		}
		for i, l := range lines {
			if w := render.Width(l); w > m.w {
				t.Errorf("%dx%d line %d is %d wide", sz[0], sz[1], i, w)
			}
		}
	}
	m.w, m.h = 30, 8
	if !strings.Contains(m.View(), "너무 작습니다") {
		t.Error("no too-small message")
	}
}

func TestNewGroupNeverTakesTheCursorTab(t *testing.T) {
	m := connected(t)
	m.focus = paneTabs
	m, _ = step(t, m, key("n"))
	o, ok := m.ov.(promptOv)
	if !ok {
		t.Fatalf("overlay = %T", m.ov)
	}
	if strings.Contains(o.title, "탭") {
		t.Fatalf("with nothing selected the prompt targets tabs: %q", o.title)
	}
	m.ov = nil
	m, _ = step(t, m, key(" "))
	m, _ = step(t, m, key("n"))
	if o := m.ov.(promptOv); !strings.Contains(o.title, "탭 1개") {
		t.Fatalf("selected tab not offered: %q", o.title)
	}
}

func TestFindURLs(t *testing.T) {
	got := findURLs("see https://a.example/x, and (https://b.example/y). https://a.example/x")
	if strings.Join(got, " ") != "https://a.example/x https://b.example/y" {
		t.Fatalf("%v", got)
	}
}

func hasToast(m Model, sub string) bool {
	for _, it := range m.toasts.Items() {
		if strings.Contains(it.Text, sub) {
			return true
		}
	}
	return false
}

// drain runs a command batch without waiting on timers.
func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, drain(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(50 * time.Millisecond):
		return nil // a tick; not what these tests look for
	}
}

