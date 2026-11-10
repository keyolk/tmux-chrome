package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/keyolk/tmux-chrome/tui/bridge"
	"github.com/keyolk/tuikit/toast"
)

// connState is the change feed's state; the header shows it as one badge.
//
//	connecting ─▶ resyncing ─▶ connected ─(feed ends)─▶ reconnecting ─▶ resyncing …
//	                                                     │ Chrome not running
//	                                                     ▼
//	                                                   halted (poll; ⏎ launches)
//
// legacy is separate: the bridge answers but the extension predates the
// requests this UI makes, so retrying cannot help — only a reload can.
type connState int

const (
	stConnecting connState = iota
	stResyncing
	stConnected
	stReconnecting
	stReviving
	stHalted
	stLegacy
)

func (s connState) down() bool {
	return s == stReconnecting || s == stReviving || s == stHalted || s == stLegacy || s == stConnecting
}

// reviveAfter is how long the feed may stay down before the UI wakes the
// extension itself. The extension retries on its own, but its backoff grows
// to five minutes; waiting that out is the failure this exists to avoid.
const reviveAfter = 3 * time.Second

// haltedPoll is how often a halted UI checks whether Chrome came back.
const haltedPoll = 5 * time.Second

var backoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second}

func backoffFor(attempt int) time.Duration {
	if attempt <= 0 {
		return backoff[0]
	}
	return backoff[min(attempt-1, len(backoff)-1)]
}

type (
	connectedMsg struct {
		gen uint64
		sub *bridge.Sub
	}
	connFailedMsg struct {
		gen uint64
		err error
	}
	eventMsg struct {
		gen uint64
		ev  bridge.Event
	}
	feedLostMsg struct {
		gen uint64
		err error
	}
	retryMsg  struct{ gen uint64 }
	chromeMsg struct {
		gen     uint64
		running bool
	}
	revivedMsg struct {
		gen uint64
		err error
	}
	launchedMsg   struct{ err error }
	reloadSentMsg struct{ err error }
	clockMsg      struct{}
)

// connect opens a new feed. Every attempt takes a new generation, so the
// results of an older attempt, or the end of a feed already replaced, are
// dropped instead of tearing down the current one.
func (m Model) connect() (Model, tea.Cmd) {
	m.connGen++
	gen := m.connGen
	if m.sub != nil {
		m.sub.Close() // unblocks the old feed's Next; its feedLostMsg carries the old gen
		m.sub = nil
	}
	bc := m.bc
	return m, func() tea.Msg {
		sub, err := bc.Subscribe(context.Background())
		if err != nil {
			return connFailedMsg{gen: gen, err: err}
		}
		return connectedMsg{gen: gen, sub: sub}
	}
}

func waitEvent(gen uint64, sub *bridge.Sub) tea.Cmd {
	return func() tea.Msg {
		ev, err := sub.Next()
		if err != nil {
			return feedLostMsg{gen: gen, err: err}
		}
		return eventMsg{gen: gen, ev: ev}
	}
}

// goDown records an outage and schedules the next attempt.
func (m Model) goDown(err error) (Model, tea.Cmd) {
	if m.downSince.IsZero() {
		m.downSince = m.now()
		m.logf("bridge down: %v", err)
	}
	m.conn = stReconnecting
	m.attempt++
	gen := m.connGen
	cmds := []tea.Cmd{tea.Tick(backoffFor(m.attempt), func(time.Time) tea.Msg { return retryMsg{gen: gen} })}
	m, c := m.startClock()
	return m, tea.Batch(append(cmds, c)...)
}

// startClock ticks once a second while the bridge is down, so the header's
// "data from 12s ago" stays true. Connected, the UI draws nothing on a timer.
func (m Model) startClock() (Model, tea.Cmd) {
	if m.clockOn {
		return m, nil
	}
	m.clockOn = true
	return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return clockMsg{} })
}

func (m Model) updateConn(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case connectedMsg:
		if msg.gen != m.connGen {
			msg.sub.Close()
			return m, nil, true
		}
		m.sub = msg.sub
		m.conn = stResyncing
		m.attempt = 0
		m.downSince = time.Time{}
		m.revived = false
		m.toasts = toast.Dismiss(m.toasts, "conn")
		m, fetch := m.fetch()
		return m, tea.Batch(waitEvent(msg.gen, msg.sub), fetch), true

	case connFailedMsg:
		if msg.gen != m.connGen {
			return m, nil, true
		}
		if errors.Is(msg.err, bridge.ErrUnsupported) {
			return m.goLegacy()
		}
		m, cmd := m.goDown(msg.err)
		return m, cmd, true

	case feedLostMsg:
		if msg.gen != m.connGen {
			return m, nil, true
		}
		m.sub = nil
		m, cmd := m.goDown(msg.err)
		return m, cmd, true

	case eventMsg:
		if msg.gen != m.connGen {
			return m, nil, true
		}
		next := waitEvent(msg.gen, m.sub)
		if m.snap != nil && msg.ev.Epoch == m.snap.Epoch && msg.ev.Seq <= m.snap.Seq {
			return m, next, true // already in the snapshot we hold
		}
		m, refetch := m.scheduleRefetch()
		return m, tea.Batch(next, refetch), true

	case retryMsg:
		if msg.gen != m.connGen || !m.conn.down() || m.conn == stLegacy {
			return m, nil, true
		}
		gen := m.connGen
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return chromeMsg{gen: gen, running: bridge.ChromeRunning(ctx)}
		}, true

	case chromeMsg:
		if msg.gen != m.connGen {
			return m, nil, true
		}
		if !msg.running {
			m.conn = stHalted
			gen := m.connGen
			return m, tea.Tick(haltedPoll, func(time.Time) tea.Msg { return retryMsg{gen: gen} }), true
		}
		if !m.revived && !m.downSince.IsZero() && m.now().Sub(m.downSince) >= reviveAfter {
			m, cmd := m.revive()
			return m, cmd, true
		}
		m.conn = stReconnecting
		m, cmd := m.connect()
		return m, cmd, true

	case revivedMsg:
		if msg.gen != m.connGen {
			return m, nil, true
		}
		if msg.err != nil {
			m.logf("revive: %v", msg.err)
		}
		m.conn = stReconnecting
		m, cmd := m.connect()
		return m, cmd, true

	case launchedMsg:
		if msg.err != nil {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "conn", Text: "Chrome 실행 실패: " + msg.err.Error(), Level: toast.Error})
			return m, nil, true
		}
		m.conn = stReconnecting
		m.attempt = 0
		gen := m.connGen
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return retryMsg{gen: gen} }), true

	case reloadSentMsg:
		if msg.err != nil && !bridge.Unknown(msg.err) {
			m.toasts = toast.Add(m.toasts, toast.Toast{Source: "conn", Text: "reload 요청 실패: " + msg.err.Error(), Level: toast.Error})
			return m, nil, true
		}
		// The reload drops the native port; the old host exits and a new one
		// comes up in ~15s. The ordinary retry loop finds it. revived is set
		// so that loop does not reload a second time on top of this one.
		m.revived = true
		m, cmd := m.goDown(errors.New("extension reloading"))
		return m, cmd, true

	case clockMsg:
		if !m.conn.down() && m.conn != stResyncing {
			m.clockOn = false
			return m, nil, true
		}
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return clockMsg{} }), true
	}
	return m, nil, false
}

func (m Model) goLegacy() (Model, tea.Cmd, bool) {
	m.conn = stLegacy
	m.toasts = toast.Add(m.toasts, toast.Toast{
		Source: "conn", Level: toast.Error,
		Text: "extension이 이 UI보다 오래됐습니다. ctrl+r로 reload하세요.",
	})
	return m, nil, true
}

func (m Model) revive() (Model, tea.Cmd) {
	m.revived = true
	m.conn = stReviving
	gen := m.connGen
	rv := bridge.Reviver{Client: m.bc}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return revivedMsg{gen: gen, err: rv.Revive(ctx)}
	}
}

// manualReconnect is ctrl+r. Connected, it refetches. Down, it skips the
// backoff and wakes the extension now. Legacy, it asks the old extension to
// reload itself, the one request every version understands.
func (m Model) manualReconnect() (Model, tea.Cmd) {
	switch m.conn {
	case stConnected, stResyncing:
		return m.fetch()
	case stLegacy:
		bc := m.bc
		m.toasts = toast.Add(m.toasts, toast.Toast{Source: "conn", Text: "extension reload 중…", Level: toast.Progress})
		return m, func() tea.Msg {
			return reloadSentMsg{err: bc.Do(context.Background(), "reload_extension", nil, nil)}
		}
	case stHalted:
		return m, launchChrome()
	}
	m.attempt = 0
	if m.downSince.IsZero() {
		m.downSince = m.now()
	}
	return m.revive()
}

func launchChrome() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return launchedMsg{err: bridge.LaunchChrome(ctx)}
	}
}

// badge is the header's connection text and its severity.
func (m Model) badge() (string, toast.Level) {
	age := ""
	if !m.snapAt.IsZero() {
		age = fmt.Sprintf(" · %s 전 데이터", ago(m.now().Sub(m.snapAt)))
	}
	switch m.conn {
	case stConnected:
		return m.th.Glyphs.Check + " 연결됨", toast.Success
	case stResyncing:
		return "재동기화 중", toast.Info
	case stConnecting:
		return "연결 중", toast.Info
	case stReconnecting:
		return fmt.Sprintf("끊김 · 재연결 %d회%s", m.attempt, age), toast.Warn
	case stReviving:
		return "끊김 · extension 깨우는 중" + age, toast.Warn
	case stHalted:
		return "Chrome 꺼짐 · ⏎ 실행" + age, toast.Error
	case stLegacy:
		return "extension 구버전 · ctrl+r reload", toast.Error
	}
	return "", toast.Info
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d초", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d분", int(d.Minutes()))
	}
	return fmt.Sprintf("%d시간", int(d.Hours()))
}
