package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/tmux"
	"github.com/keyolk/tuikit/toast"
)

// externMsg reports something run outside the bridge: bin/tmux-chrome, tmux,
// pbcopy. refetch asks for a snapshot afterwards, for commands that change
// what the panes show.
type externMsg struct {
	label   string
	out     string
	err     error
	refetch bool
	quit    bool
}

func (m Model) onExtern(msg externMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "extern", Text: msg.label + " 실패: " + msg.err.Error(), Level: toast.Error})
		return m, nil
	}
	text := msg.out
	if text == "" {
		text = msg.label + " 완료"
	}
	m.toasts = toast.Add(m.toasts, toast.Toast{Source: "extern", Text: text, Level: toast.Success})
	if msg.quit && m.cfg.Popup {
		return m.quit()
	}
	if msg.refetch && m.conn == stConnected {
		return m.fetch()
	}
	return m, nil
}

// runCLI runs a bin/tmux-chrome subcommand that already does the job
// (window layouts, swap, merge) rather than a second copy of it here.
func (m Model) runCLI(label string, refetch bool, args ...string) tea.Cmd {
	cli := m.cfg.CLI
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, cli, args...).CombinedOutput()
		text := lastLine(string(out))
		if err != nil {
			if text != "" {
				err = fmt.Errorf("%w: %s", err, text)
			}
			return externMsg{label: label, err: err}
		}
		return externMsg{label: label, out: text, refetch: refetch}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func copyCmd(text string, n int) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		if out, err := cmd.CombinedOutput(); err != nil {
			return externMsg{label: "URL 복사", err: fmt.Errorf("%w: %s", err, out)}
		}
		return externMsg{label: "URL 복사", out: fmt.Sprintf("URL %d개를 복사했습니다", n)}
	}
}

// --- palette ---

// paletteItem keys: "a:<action>" runs a binding, "c:<subcommand>" runs the
// CLI, "l:<layout>" applies a saved window layout.
func (m Model) openPalette() (Model, tea.Cmd) {
	var items []pickItem
	for _, e := range m.km.Entries(m.stack(), m.keyOpts()) {
		if e.Action == "palette" || e.Action == "quit" {
			continue
		}
		if needsBridge[e.Action] && m.conn != stConnected {
			continue
		}
		key := ""
		if len(e.Keys) > 0 {
			key = m.th.KeyNames().Name(e.Keys[0])
		}
		items = append(items, pickItem{key: "a:" + e.Action, label: e.Help, detail: key})
	}
	for _, c := range []struct{ sub, label string }{
		{"swap-window", "터미널 ↔ Chrome 창 위치 바꾸기"},
		{"distribute", "터미널과 Chrome을 다른 화면으로"},
		{"merge", "Chrome 창 하나로 합치기"},
		{"snapshot", "탭 그룹 snapshot 저장"},
		{"reload", "extension reload"},
	} {
		items = append(items, pickItem{key: "c:" + c.sub, label: c.label, detail: "tmux-chrome " + c.sub})
	}
	for _, app := range []string{"Chrome", "Slack", "Zoom", "Discord"} {
		items = append(items, pickItem{key: "f:" + app, label: app + " 앞으로", detail: "tmux-chrome focus"})
	}
	m.ov = newPicker("명령", items, func(m Model, it pickItem) (Model, tea.Cmd) {
		kind, arg, _ := strings.Cut(it.key, ":")
		switch kind {
		case "a":
			return m.run(arg)
		case "c":
			return m, m.runCLI(it.label, true, arg)
		case "f":
			return m, m.runCLI(it.label, false, "focus", strings.ToLower(arg))
		case "l":
			return m, m.runCLI(it.label, false, "window-layout", "apply", arg)
		}
		return m, nil
	})
	// Layouts come from the CLI's config; they join the list when read.
	cli := m.cfg.CLI
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, cli, "window-layout", "list").Output()
		return layoutsMsg{out: string(out), err: err}
	}
}

type layoutsMsg struct {
	out string
	err error
}

// onLayouts adds saved layouts to an open palette. Lines are
// "<n>\t<name>\t<label>\t…" as cmd_window_layout_list prints them.
func (m Model) onLayouts(msg layoutsMsg) Model {
	o, ok := m.ov.(pickerOv)
	if !ok || o.title != "명령" {
		return m
	}
	if msg.err != nil {
		m.logf("window-layout list: %v", msg.err)
		return m
	}
	for _, line := range strings.Split(strings.TrimSpace(msg.out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 || f[1] == "" {
			continue
		}
		o.items = append(o.items, pickItem{key: "l:" + f[1], label: "레이아웃: " + f[2], detail: f[1]})
	}
	m.ov = o.filter()
	return m
}

// --- grab ---

type grabMsg struct {
	urls []string
	err  error
}

// urlPattern is cmd_grab's: URLs in pane text, trailing punctuation cut.
var (
	urlPattern = regexp.MustCompile(`https?://[A-Za-z0-9._~:/?#@!$&()*+,;=%\[\]-]+`)
	urlTrail   = regexp.MustCompile(`[.,;:!)]+$`)
)

func findURLs(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urlPattern.FindAllString(text, -1) {
		u = urlTrail.ReplaceAllString(u, "")
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}

// grab reads every pane in the UI's tmux window (history included) for URLs,
// then lets the user check which to open in the group under the cursor.
func (m Model) grab() (Model, tea.Cmd) {
	if m.cfg.Window == "" {
		return m.warn("tmux 밖에서는 가져올 pane이 없습니다"), nil
	}
	tm, win := m.tm, m.cfg.Window
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		panes, err := tm.Panes(ctx, tmux.PaneFilter{Target: win})
		if err != nil {
			return grabMsg{err: err}
		}
		var text strings.Builder
		for _, p := range panes {
			out, err := tm.CapturePane(ctx, p.ID, tmux.CaptureOpts{Start: "-2000", Join: true})
			if err != nil {
				return grabMsg{err: err}
			}
			text.WriteString(out)
			text.WriteByte('\n')
		}
		return grabMsg{urls: findURLs(text.String())}
	}
}

func (m Model) onGrab(msg grabMsg) (Model, tea.Cmd) {
	if msg.err != nil {
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "grab", Text: "pane 읽기 실패: " + msg.err.Error(), Level: toast.Error})
		return m, nil
	}
	if len(msg.urls) == 0 {
		return m.warn("이 window의 pane에서 URL을 찾지 못했습니다"), nil
	}
	g, _ := m.groups.Selected()
	open := map[string]bool{}
	for _, t := range snapTabs(m.snap) {
		open[t.URL] = true
	}
	items := make([]checkItem, len(msg.urls))
	for i, u := range msg.urls {
		it := checkItem{key: u, label: u}
		if open[u] {
			it.tag = "열림"
		}
		items[i] = it
	}
	where := "그룹 없이"
	if !g.Virtual && g.key != "" {
		where = g.label() + " 그룹에"
	}
	m.ov = newChecklist("pane의 URL 열기 ("+where+")", "", items, nil, func(m Model, picked []checkItem) (Model, tea.Cmd) {
		urls := make([]string, len(picked))
		for i, p := range picked {
			urls[i] = p.key
		}
		if cur, ok := m.groupByID(g.GroupID); ok {
			g = cur
		}
		return m.openURLs(urls, g)
	})
	return m, nil
}

// --- clean ---

// staleMsg carries the extension's stale scan with PRs already verified.
type staleMsg struct {
	scan    bridge.StaleScan
	ended   map[int]string // tab id → "merged"/"closed"
	err     error
	ghErr   error
	orphans []groupRow
	loose   int
}

// openClean gathers every cleanup the Raycast commands offer into one
// checklist: stale tabs (bulk-safe ones pre-checked), the loose-tab sweep and
// orphan groups. Nothing is pre-checked that the bulk rules would not close.
func (m Model) openClean() (Model, tea.Cmd) {
	bc := m.bc
	var orphans []groupRow
	for _, g := range m.all {
		if g.Orphan {
			orphans = append(orphans, g)
		}
	}
	loose := 0
	for _, t := range snapTabs(m.snap) {
		if t.GroupID == bridge.NoGroup && !t.Pinned {
			loose++
		}
	}
	m.toasts = toast.Add(m.toasts, toast.Toast{Source: "clean", Text: "정리 대상 찾는 중…", Level: toast.Progress})
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var scan bridge.StaleScan
		if err := bc.Do(ctx, "stale_candidates", nil, &scan); err != nil {
			return staleMsg{err: err}
		}
		ended, ghErr := prStates(ctx, scan.PullRequests)
		return staleMsg{scan: scan, ended: ended, ghErr: ghErr, orphans: orphans, loose: loose}
	}
}

// ghBin matches the Raycast side's default.
func ghBin() string {
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	return "/opt/homebrew/bin/gh"
}

// prStates asks GitHub, in one GraphQL call, which PR tabs point at ended
// PRs. The query is the one stale-tabs.ts builds.
func prStates(ctx context.Context, prs []bridge.PullRequestTab) (map[int]string, error) {
	out := map[int]string{}
	if len(prs) == 0 {
		return out, nil
	}
	type ref struct {
		owner, repo string
		number      int
	}
	idx := map[ref]int{}
	var refs []ref
	for _, p := range prs {
		r := ref{p.Owner, p.Repo, p.Number}
		if _, ok := idx[r]; !ok {
			idx[r] = len(refs)
			refs = append(refs, r)
		}
	}
	var q strings.Builder
	q.WriteString("query CleanupPullRequests {\n")
	for i, r := range refs {
		fmt.Fprintf(&q, "p%d: repository(owner: %q, name: %q) { pullRequest(number: %d) { state merged } }\n", i, r.owner, r.repo, r.number)
	}
	q.WriteString("}")
	raw, err := exec.CommandContext(ctx, ghBin(), "api", "graphql", "-f", "query="+q.String()).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("gh: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("gh: %w", err)
	}
	var resp struct {
		Data map[string]*struct {
			PullRequest *struct {
				State  string `json:"state"`
				Merged bool   `json:"merged"`
			} `json:"pullRequest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return out, fmt.Errorf("gh: decode: %w", err)
	}
	for _, p := range prs {
		r := resp.Data[fmt.Sprintf("p%d", idx[ref{p.Owner, p.Repo, p.Number}])]
		if r == nil || r.PullRequest == nil || r.PullRequest.State == "OPEN" {
			continue
		}
		if r.PullRequest.Merged || r.PullRequest.State == "MERGED" {
			out[p.ID] = "merged"
		} else {
			out[p.ID] = "closed"
		}
	}
	return out, nil
}

type cleanKind int

const (
	cleanTab cleanKind = iota
	cleanLoose
	cleanOrphan
)

type cleanData struct {
	kind  cleanKind
	tab   bridge.Tab
	group groupRow
}

func (m Model) onStale(msg staleMsg) (Model, tea.Cmd) {
	m.toasts = toast.Dismiss(m.toasts, "clean")
	if msg.err != nil {
		if errors.Is(msg.err, bridge.ErrUnsupported) {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "clean", Text: "extension이 오래돼 정리를 못 합니다. ctrl+r로 reload하세요", Level: toast.Error})
			return m, nil
		}
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "clean", Text: "정리 대상 찾기 실패: " + msg.err.Error(), Level: toast.Error})
		return m, nil
	}
	var items []checkItem
	checked := map[string]bool{}
	protected := func(t bridge.Tab) bool { return t.Active || t.Pinned || t.Audible }
	for _, c := range msg.scan.Candidates {
		key := tabKey(c.ID)
		items = append(items, checkItem{key: key, tag: c.Kind, label: orUntitled(c.DisplayTitle), detail: c.Reason + " · " + c.GroupTitle, data: cleanData{kind: cleanTab, tab: c.Tab}})
		checked[key] = c.SafeForBulkClose && !protected(c.Tab)
	}
	for _, p := range msg.scan.PullRequests {
		state, ok := msg.ended[p.ID]
		if !ok {
			continue
		}
		key := tabKey(p.ID)
		items = append(items, checkItem{key: key, tag: "pr-" + state, label: orUntitled(p.Title), detail: fmt.Sprintf("%s/%s#%d %s · %s", p.Owner, p.Repo, p.Number, state, p.GroupTitle), data: cleanData{kind: cleanTab, tab: p.Tab}})
		checked[key] = !protected(p.Tab)
	}
	if msg.loose > 0 {
		items = append(items, checkItem{key: "loose", tag: "sweep", label: fmt.Sprintf("그룹 없는 탭 %d개를 %s 그룹으로", msg.loose, reservedGroup), data: cleanData{kind: cleanLoose}})
	}
	for _, g := range msg.orphans {
		items = append(items, checkItem{key: "o:" + g.key, tag: "고아", label: g.label() + " 그룹 해제", detail: fmt.Sprintf("tmux window 없음 · 탭 %d개는 남김", g.Tabs), data: cleanData{kind: cleanOrphan, group: g}})
	}
	if len(items) == 0 {
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "clean", Text: "정리할 것이 없습니다", Level: toast.Success})
		return m, nil
	}
	note := ""
	if msg.ghErr != nil {
		m.logf("pr states: %v", msg.ghErr)
		note = "GitHub 확인 실패: PR 탭은 제외됨"
	}
	m.ov = newChecklist("정리", note, items, checked, func(m Model, picked []checkItem) (Model, tea.Cmd) {
		return m.runClean(picked)
	})
	return m, nil
}

// runClean closes the checked tabs (with the bulk guards: active, pinned and
// audible tabs are skipped by Chrome), then sweeps and ungroups as checked.
func (m Model) runClean(picked []checkItem) (Model, tea.Cmd) {
	var tabs []bridge.Tab
	sweep := false
	var orphans []groupRow
	for _, it := range picked {
		d := it.data.(cleanData)
		switch d.kind {
		case cleanTab:
			tabs = append(tabs, d.tab)
		case cleanLoose:
			sweep = true
		case cleanOrphan:
			orphans = append(orphans, d.group)
		}
	}
	reqs := make([]bridge.CloseRequest, len(tabs))
	closing := make([]closedTab, len(tabs))
	for i, t := range tabs {
		reqs[i] = bridge.CloseRequest{TabID: t.ID, ExpectedURL: t.URL}
		closing[i] = m.closedTabOf(t)
	}
	return m.do(op{
		label:   "정리",
		closing: closing,
		run: func(ctx context.Context, bc bridge.Client) (result, error) {
			var parts []string
			var res result
			if len(reqs) > 0 {
				var r bridge.BatchResult
				if err := bc.Do(ctx, "close_tabs", map[string]any{"tabs": reqs}, &r); err != nil {
					return res, err
				}
				res.closed = r.Closed
				parts = append(parts, fmt.Sprintf("탭 %d개 닫음%s", len(r.Closed), skippedNote(r)))
			}
			if sweep {
				var r struct {
					Moved int `json:"moved"`
				}
				if err := bc.Do(ctx, "clean_tabs", nil, &r); err != nil {
					return res, err
				}
				parts = append(parts, fmt.Sprintf("그룹 없는 탭 %d개 이동", r.Moved))
			}
			for _, g := range orphans {
				if err := bc.Do(ctx, "delete_group", map[string]any{"name": g.Title, "locator": locator(g)}, nil); err != nil {
					return res, err
				}
			}
			if len(orphans) > 0 {
				parts = append(parts, fmt.Sprintf("고아 그룹 %d개 해제", len(orphans)))
			}
			res.text = strings.Join(parts, " · ")
			return res, nil
		},
	})
}
