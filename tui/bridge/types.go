package bridge

import (
	"context"
	"encoding/json"
)

// NoGroup is Chrome's groupId for a tab outside any group.
const NoGroup = -1

// Group is a Chrome tab group as the snapshot reports it.
type Group struct {
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Color     string `json:"color"`
	Collapsed bool   `json:"collapsed"`
	WindowID  int    `json:"window_id"`
}

// Tab is a Chrome tab as the snapshot reports it.
type Tab struct {
	ID           int     `json:"id"`
	Title        string  `json:"title"`
	URL          string  `json:"url"`
	GroupID      int     `json:"group_id"`
	WindowID     int     `json:"window_id"`
	Index        int     `json:"index"`
	Active       bool    `json:"active"`
	Pinned       bool    `json:"pinned"`
	Audible      bool    `json:"audible"`
	LastAccessed float64 `json:"last_accessed"`
}

// Snapshot is every group and tab at one moment. Epoch and Seq place it in
// the extension's change feed: an event with the same Epoch and a Seq at or
// below this one is already reflected.
type Snapshot struct {
	Epoch           string  `json:"epoch"`
	Seq             int64   `json:"seq"`
	FocusedWindowID *int    `json:"focused_window_id"`
	Groups          []Group `json:"groups"`
	Tabs            []Tab   `json:"tabs"`
}

// Colors are the values chrome.tabGroups accepts, in Chrome's menu order.
var Colors = []string{"grey", "blue", "red", "yellow", "green", "pink", "purple", "cyan", "orange"}

func (c Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var s Snapshot
	err := c.Do(ctx, "snapshot", nil, &s)
	return s, err
}

// Skipped is a tab a batch request left alone, with Chrome's reason.
type Skipped struct {
	TabID  int    `json:"tab_id"`
	Reason string `json:"reason"`
}

// BatchResult covers the counts every batch handler may return; each handler
// fills the ones that apply.
type BatchResult struct {
	Grouped   int       `json:"grouped"`
	Ungrouped int       `json:"ungrouped"`
	Changed   int       `json:"changed"`
	Reloaded  int       `json:"reloaded"`
	Opened    int       `json:"opened"`
	Closed    []int     `json:"closed"`
	GroupID   *int      `json:"group_id"`
	Skipped   []Skipped `json:"skipped"`
}

// CloseRequest names a tab and the URL it must still show to be closed.
type CloseRequest struct {
	TabID       int    `json:"tab_id"`
	ExpectedURL string `json:"expected_url"`
}

// StaleCandidate is a tab stale.js judged on its URL and title alone.
type StaleCandidate struct {
	Tab
	GroupTitle       string `json:"group_title"`
	Kind             string `json:"kind"`
	Reason           string `json:"reason"`
	SafeForBulkClose bool   `json:"safe_for_bulk_close"`
	DisplayTitle     string `json:"display_title"`
	DisplayURL       string `json:"display_url"`
}

// PullRequestTab is a tab on a GitHub PR, left for the client to verify.
type PullRequestTab struct {
	Tab
	GroupTitle string `json:"group_title"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
}

type StaleScan struct {
	Candidates   []StaleCandidate `json:"candidates"`
	PullRequests []PullRequestTab `json:"pull_requests"`
}

// Event is one line of the change feed.
type Event struct {
	Event string `json:"event"` // subscribed, ping, changed
	Kind  string `json:"kind"`
	Seq   int64  `json:"seq"`
	Epoch string `json:"epoch"`
}

func decodeEvent(raw []byte) (Event, response, error) {
	var probe struct {
		Event
		response
	}
	err := json.Unmarshal(raw, &probe)
	return probe.Event, probe.response, err
}
