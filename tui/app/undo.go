package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
)

// closedTab is enough to reopen a tab where it was: its URL and its group,
// by id while that group lives and by title after.
type closedTab struct {
	TabID      int    `json:"-"` // the closed tab's id, to match Chrome's report
	URL        string `json:"url"`
	Title      string `json:"title"`
	GroupID    int    `json:"group_id"`
	GroupTitle string `json:"group_title"`
	GroupColor string `json:"group_color"`
}

type closedBatch struct {
	At   time.Time   `json:"at"`
	Tabs []closedTab `json:"tabs"`
}

// undoKeep bounds the file. U pops the newest batch; older ones wait.
const undoKeep = 20

func undoPath(dir string) string { return filepath.Join(dir, "tui-closed.json") }

// loadUndo reads the batches a previous run closed, so U works across popup
// launches. An empty dir disables persistence (tests).
func loadUndo(dir string) ([]closedBatch, error) {
	if dir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(undoPath(dir))
	if err != nil {
		return nil, err
	}
	var out []closedBatch
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", undoPath(dir), err)
	}
	return out, nil
}

type undoSavedMsg struct{ err error }

func saveUndoCmd(dir string, batches []closedBatch) tea.Cmd {
	if dir == "" {
		return nil
	}
	batches = append([]closedBatch(nil), batches...)
	return func() tea.Msg {
		return undoSavedMsg{err: writeJSON(undoPath(dir), batches)}
	}
}

// writeJSON writes through a temp file and a rename, so a crash leaves the
// old file or the new one, never half of either.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m Model) closedTabOf(t bridge.Tab) closedTab {
	c := closedTab{TabID: t.ID, URL: t.URL, Title: t.Title, GroupID: t.GroupID}
	if g, ok := m.groupByID(t.GroupID); ok {
		c.GroupTitle, c.GroupColor = g.Title, g.Color
	}
	return c
}

// recordClosed keeps the tabs Chrome reported closed; a skipped tab is still
// open and must not come back as a duplicate.
func (m Model) recordClosed(closing []closedTab, closedIDs []int) (Model, tea.Cmd) {
	done := map[int]bool{}
	for _, id := range closedIDs {
		done[id] = true
	}
	var tabs []closedTab
	for _, c := range closing {
		if done[c.TabID] {
			tabs = append(tabs, c)
		}
	}
	if len(tabs) == 0 {
		return m, nil
	}
	m.undo = append(m.undo, closedBatch{At: m.now(), Tabs: tabs})
	if len(m.undo) > undoKeep {
		m.undo = m.undo[len(m.undo)-undoKeep:]
	}
	return m, saveUndoCmd(m.cfg.StateDir, m.undo)
}

// undoLast reopens the newest closed batch, each tab into its old group.
func (m Model) undoLast() (Model, tea.Cmd) {
	if len(m.undo) == 0 {
		return m.warn("되살릴 탭이 없습니다"), nil
	}
	batch := m.undo[len(m.undo)-1]
	type dest struct {
		id           int
		title, color string
	}
	byDest := map[dest][]string{}
	var order []dest
	for _, t := range batch.Tabs {
		d := dest{id: t.GroupID, title: t.GroupTitle, color: t.GroupColor}
		if _, seen := byDest[d]; !seen {
			order = append(order, d)
		}
		byDest[d] = append(byDest[d], t.URL)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].title < order[j].title })
	m.undo = m.undo[:len(m.undo)-1]
	save := saveUndoCmd(m.cfg.StateDir, m.undo)
	m, run := m.do(op{
		label: fmt.Sprintf("닫은 탭 %d개 되살리기", len(batch.Tabs)),
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			opened := 0
			for _, d := range order {
				params := map[string]any{"urls": byDest[d]}
				if d.id != bridge.NoGroup {
					params["group_id"], params["title"], params["color"] = d.id, d.title, d.color
				}
				var r bridge.BatchResult
				if err := bc.Do(ctx, "open_tabs", params, &r); err != nil {
					return result{}, err
				}
				opened += r.Opened
			}
			return result{text: fmt.Sprintf("탭 %d개를 되살렸습니다", opened)}, nil
		},
	})
	if run == nil {
		// Not sent (disconnected): keep the batch for the next try.
		m.undo = append(m.undo, batch)
		return m, nil
	}
	return m, tea.Batch(run, save)
}

// snapshotFile is the layout `tmux-chrome restore` reads (snapshot_groups in
// bin/tmux-chrome): grouped tabs only, grouped by group id.
type snapshotFile struct {
	SavedAt string          `json:"saved_at"`
	Groups  []snapshotGroup `json:"groups"`
}

type snapshotGroup struct {
	GroupID int              `json:"group_id"`
	Title   string           `json:"title"`
	Tabs    []snapshotTabRef `json:"tabs"`
}

type snapshotTabRef struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

func writeSnapshotFile(dir string, s bridge.Snapshot, now time.Time) error {
	if dir == "" {
		return nil
	}
	titles := map[int]string{}
	for _, g := range s.Groups {
		titles[g.ID] = g.Title
	}
	var groups []snapshotGroup
	at := map[int]int{}
	for _, t := range s.Tabs {
		if t.GroupID == bridge.NoGroup {
			continue
		}
		i, ok := at[t.GroupID]
		if !ok {
			i = len(groups)
			at[t.GroupID] = i
			groups = append(groups, snapshotGroup{GroupID: t.GroupID, Title: titles[t.GroupID]})
		}
		groups[i].Tabs = append(groups[i].Tabs, snapshotTabRef{URL: t.URL, Title: t.Title})
	}
	f := snapshotFile{SavedAt: now.UTC().Format(time.RFC3339), Groups: groups}
	return writeJSON(filepath.Join(dir, "snapshot-"+now.Format("20060102-150405")+".json"), f)
}

func (m Model) undoHint() string {
	if len(m.undo) == 0 {
		return ""
	}
	return fmt.Sprintf("U 되살리기 %d", len(m.undo[len(m.undo)-1].Tabs))
}
