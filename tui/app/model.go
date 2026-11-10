// Package app is the tmux-chrome terminal UI: Chrome tab groups on the left,
// their tabs on the right, one search box over both, and every write action
// keyed by id.
package app

import (
	"context"
	"errors"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/async"
	"github.com/keyolk/tuikit/input"
	"github.com/keyolk/tuikit/keys"
	"github.com/keyolk/tuikit/list"
	"github.com/keyolk/tuikit/mouse"
	"github.com/keyolk/tuikit/prefs"
	"github.com/keyolk/tuikit/theme"
	"github.com/keyolk/tuikit/tmux"
	"github.com/keyolk/tuikit/toast"
)

// AppName keys the prefs file and the ASCII-glyph env var (TMUX_CHROME_ASCII).
const AppName = "tmux-chrome"

type pane int

const (
	paneGroups pane = iota
	paneTabs
)

// Config is everything main decides; tests fill it with fakes.
type Config struct {
	Bridge   bridge.Client
	Tmux     tmux.Client
	Window   string // the tmux window (@N) this UI acts for: its group is "current"
	Popup    bool   // opened in a tmux popup: start in search, quit after jumping
	CLI      string // bin/tmux-chrome, for palette commands it already implements
	StateDir string // undo records and snapshots; bin/tmux-chrome's state dir
	Logf     func(string, ...any)
	Now      func() time.Time
	Theme    theme.Theme
	// NoConnect skips the feed in Init; tests drive the model with messages.
	NoConnect bool
}

// uiState survives a restart.
type uiState struct {
	Scope scope `json:"scope"`
	Focus pane  `json:"focus"`
}

type Model struct {
	cfg    Config
	km     keys.Keymap
	th     theme.Theme
	toasts toast.Queue
	saver  prefs.Saver[uiState]
	bc     bridge.Client
	tm     tmux.Client
	logf   func(string, ...any)

	snap   *bridge.Snapshot
	snapAt time.Time
	wins   []tmuxWindow
	all    []groupRow
	groups list.List[groupRow]
	tabs   list.List[tabRow]
	flat   bool

	field     input.Field
	searching bool
	sc        scope
	focus     pane
	// Selections hold what identifies the item if Chrome restarts and every
	// id changes: a tab's URL, a group's title.
	selTabs   map[int]string
	selGroups map[int]string

	conn      connState
	connGen   uint64
	sub       *bridge.Sub
	attempt   int
	downSince time.Time
	revived   bool
	clockOn   bool

	fetchFl    async.Flight
	fetchAgain bool
	refetchTok uint64
	pending    []pendingCheck

	ov      overlay
	help    bool
	helpOff int
	undo    []closedBatch
	clicks  mouse.Clicks
	w, h    int
}

func New(cfg Config) Model {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.CLI == "" {
		cfg.CLI = "tmux-chrome"
	}
	st, err := prefs.Load(AppName, uiState{})
	if err != nil {
		cfg.Logf("load prefs: %v", err)
	}
	m := Model{
		cfg: cfg, km: keymap(), th: cfg.Theme, toasts: toast.New(4),
		saver: prefs.NewSaver[uiState](AppName, 500*time.Millisecond),
		bc:    cfg.Bridge, tm: cfg.Tmux, logf: cfg.Logf,
		sc: st.Scope % scopeCount, focus: st.Focus,
		searching: cfg.Popup,
		field:     input.Field{Placeholder: "검색  (@그룹 /url !제외)"},
		selTabs:   map[int]string{}, selGroups: map[int]string{},
	}
	if cfg.NoConnect {
		m.saver = prefs.Saver[uiState]{} // the zero Saver persists nothing
	}
	m.undo, err = loadUndo(cfg.StateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logf("load undo: %v", err)
	}
	return m
}

// Init cannot change the model, so the first connect is a message to itself.
func (m Model) Init() tea.Cmd {
	if m.cfg.NoConnect {
		return nil
	}
	return func() tea.Msg { return startMsg{} }
}

type startMsg struct{}

func (m Model) now() time.Time { return m.cfg.Now() }

func (m Model) uiState() uiState { return uiState{Scope: m.sc, Focus: m.focus} }

type (
	snapshotMsg struct {
		id      uint64
		snap    bridge.Snapshot
		err     error
		wins    []tmuxWindow
		winsErr error
	}
	refetchMsg struct{ tok uint64 }
)

// refetchDelay coalesces a burst of change events (a page load fires
// several) into one snapshot.
const refetchDelay = 120 * time.Millisecond

func (m Model) scheduleRefetch() (Model, tea.Cmd) {
	m.refetchTok++
	tok := m.refetchTok
	return m, tea.Tick(refetchDelay, func(time.Time) tea.Msg { return refetchMsg{tok: tok} })
}

// fetch takes a snapshot and the tmux windows together, so the group pane's
// bindings and Chrome's state come from the same moment. A fetch requested
// while one runs is remembered and run once the first lands.
func (m Model) fetch() (Model, tea.Cmd) {
	fl, id, ok := m.fetchFl.Start()
	if !ok {
		m.fetchAgain = true
		return m, nil
	}
	m.fetchFl = fl
	bc, tm := m.bc, m.tm
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		s, err := bc.Snapshot(ctx)
		wins, werr := listWindows(ctx, tm)
		return snapshotMsg{id: id, snap: s, err: err, wins: wins, winsErr: werr}
	}
}

func (m Model) onSnapshot(msg snapshotMsg) (Model, tea.Cmd) {
	m.fetchFl = m.fetchFl.Done(msg.id)
	var cmds []tea.Cmd
	if m.fetchAgain {
		m.fetchAgain = false
		var c tea.Cmd
		m, c = m.fetch()
		cmds = append(cmds, c)
	}
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, bridge.ErrUnsupported):
			m, c, _ := m.goLegacy()
			return m, tea.Batch(append(cmds, c)...)
		case bridge.Unknown(msg.err):
			// The feed decides whether the bridge is gone; a slow Chrome only
			// costs this snapshot. Keep the last good one and try again.
			m.logf("snapshot: %v", msg.err)
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "fetch", Text: "Chrome 응답이 늦어 다시 읽습니다", Level: toast.Warn})
			if m.conn == stConnected || m.conn == stResyncing {
				cmds = append(cmds, tea.Tick(time.Second, func(time.Time) tea.Msg { return refetchMsg{tok: 0} }))
			}
		default:
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "fetch", Text: "스냅샷 실패: " + msg.err.Error(), Level: toast.Error})
		}
		return m, tea.Batch(cmds...)
	}
	if msg.winsErr != nil {
		// Outside tmux, or tmux unreachable: groups still work, bindings do not.
		m.logf("tmux windows: %v", msg.winsErr)
	} else {
		m.wins = msg.wins
	}
	m = m.apply(msg.snap)
	if m.conn == stResyncing {
		m.conn = stConnected
	}
	m = m.settlePending()
	return m, tea.Batch(cmds...)
}

// apply installs a snapshot. Cursors and selections follow ids; when Chrome
// restarted (a new epoch) every id is new, so they follow a group's title
// and a tab's URL instead.
func (m Model) apply(s bridge.Snapshot) Model {
	restarted := m.snap != nil && m.snap.Epoch != s.Epoch
	m.snap = &s
	m.snapAt = m.now()
	if restarted {
		m.selTabs, m.selGroups = rekeySelections(s, m.selTabs, m.selGroups)
	}
	m.all = buildGroups(s, m.wins, m.cfg.Window)
	return m.relayout()
}

func rekeySelections(s bridge.Snapshot, tabs, groups map[int]string) (map[int]string, map[int]string) {
	nt, ng := map[int]string{}, map[int]string{}
	wantURL := map[string]bool{}
	for _, u := range tabs {
		wantURL[u] = true
	}
	for _, t := range s.Tabs {
		if wantURL[t.URL] {
			nt[t.ID] = t.URL
		}
	}
	wantTitle := map[string]bool{}
	for _, title := range groups {
		wantTitle[title] = true
	}
	for _, g := range s.Groups {
		if wantTitle[g.Title] {
			ng[g.ID] = g.Title
		}
	}
	return nt, ng
}

// relayout recomputes both panes from the snapshot, query and scope.
func (m Model) relayout() Model {
	if m.snap == nil {
		return m
	}
	prevG, hadG := m.groups.Selected()
	v := deriveView(m.all, *m.snap, parseQuery(m.field.Value()), m.sc)
	m.groups = m.groups.SetItems(v.groups)
	if hadG {
		m.groups = reselect(m.groups, prevG.key, func(g groupRow) bool { return g.Title == prevG.Title && g.Title != "" })
	}
	m.flat = v.flat
	if m.flat {
		m = m.setTabs(v.tabs)
	} else {
		m = m.syncTabs()
	}
	m = m.sizeLists()
	return m
}

// syncTabs points the tab pane at the group under the cursor.
func (m Model) syncTabs() Model {
	if m.flat || m.snap == nil {
		return m
	}
	var rows []tabRow
	if g, ok := m.groups.Selected(); ok {
		rows = tabsIn(*m.snap, g)
	}
	return m.setTabs(rows)
}

func (m Model) setTabs(rows []tabRow) Model {
	prev, had := m.tabs.Selected()
	m.tabs = m.tabs.SetItems(rows)
	if had {
		m.tabs = reselect(m.tabs, prev.key, func(t tabRow) bool { return t.Tab.URL == prev.Tab.URL })
	}
	return m
}

// reselect keeps the cursor on key, or failing that on the first item that
// is the same thing under a new id.
func reselect[T list.Item](l list.List[T], key string, same func(T) bool) list.List[T] {
	if l.SelectedID() == key {
		return l
	}
	if nl, ok := l.Select(key); ok {
		return nl
	}
	for _, it := range l.Items() {
		if same(it) {
			nl, _ := l.Select(it.ID())
			return nl
		}
	}
	return l
}

func (m Model) resultsPane() pane {
	if m.flat {
		return paneTabs
	}
	return paneGroups
}

// Update wraps update so every toast added anywhere below gets its expiry
// tick, without each branch threading toast commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	old := m.toasts
	mm, cmd := m.update(msg)
	return mm, tea.Batch(cmd, toast.Ticks(old, mm.toasts))
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	if mm, cmd, ok := m.updateConn(msg); ok {
		return mm, cmd
	}
	switch msg := msg.(type) {
	case startMsg:
		m.conn = stConnecting
		return m.connect()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m.sizeLists(), nil
	case snapshotMsg:
		return m.onSnapshot(msg)
	case refetchMsg:
		if msg.tok != 0 && msg.tok != m.refetchTok {
			return m, nil
		}
		return m.fetch()
	case opDoneMsg:
		return m.onOpDone(msg)
	case staleMsg:
		return m.onStale(msg)
	case grabMsg:
		return m.onGrab(msg)
	case externMsg:
		return m.onExtern(msg)
	case layoutsMsg:
		return m.onLayouts(msg), nil
	case undoSavedMsg:
		if msg.err != nil {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "undo", Text: "되살리기 기록 저장 실패: " + msg.err.Error(), Level: toast.Error})
		}
		return m, nil
	case toast.Expired:
		m.toasts = toast.Update(m.toasts, msg)
		return m, nil
	case prefs.SaveTick:
		var cmd tea.Cmd
		m.saver, cmd = m.saver.Update(msg)
		return m, cmd
	case prefs.Saved:
		if msg.Err != nil {
			m.logf("save prefs: %v", msg.Err)
		}
		if msg.Final {
			return m, tea.Quit
		}
		return m, nil
	case tea.MouseMsg:
		return m.onMouse(msg)
	case tea.KeyMsg:
		m.toasts = toast.OnKey(m.toasts)
		// A double click is two presses with nothing between; a key is something.
		m.clicks = mouse.Clicks{}
		return m.onKey(msg)
	}
	return m, nil
}

func (m Model) quit() (Model, tea.Cmd) {
	if m.sub != nil {
		m.sub.Close()
	}
	var cmd tea.Cmd
	m.saver, cmd = m.saver.FlushWith(m.uiState())
	if cmd == nil {
		return m, tea.Quit
	}
	return m, cmd
}

func (m Model) savePrefs() (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.saver, cmd = m.saver.Save(m.uiState())
	return m, cmd
}
