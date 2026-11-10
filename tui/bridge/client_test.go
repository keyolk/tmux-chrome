package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeBridge serves one reply per connection from handle. Unix socket paths
// are capped near 104 bytes on macOS, so it lives under /tmp, not t.TempDir.
func fakeBridge(t *testing.T, handle func(req map[string]any, w net.Conn)) Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tcb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req map[string]any
				if json.Unmarshal(line, &req) != nil {
					return
				}
				handle(req, conn)
			}()
		}
	}()
	return Client{Socket: path, Timeout: 2 * time.Second}
}

func reply(w net.Conn, v any) {
	b, _ := json.Marshal(v)
	w.Write(append(b, '\n'))
}

func TestDoDecodesData(t *testing.T) {
	c := fakeBridge(t, func(req map[string]any, w net.Conn) {
		if req["type"] != "snapshot" || req["id"] == "" {
			reply(w, map[string]any{"success": false, "error": "bad request"})
			return
		}
		reply(w, map[string]any{"id": req["id"], "success": true, "data": map[string]any{
			"epoch": "e1", "seq": 4,
			"groups": []any{map[string]any{"id": 7, "title": "ops", "color": "blue", "window_id": 1}},
			"tabs":   []any{map[string]any{"id": 9, "title": "t", "url": "https://x", "group_id": 7, "window_id": 1}},
		}})
	})
	s, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Epoch != "e1" || s.Seq != 4 || s.Groups[0].Title != "ops" || s.Tabs[0].GroupID != 7 {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		reply       string
		unknown     bool
		unsupported bool
	}{
		{"Tab group no longer exists", false, false},
		{"Unknown message type: snapshot", false, true},
		{bridgeTimeout, true, false},
	}
	for _, tc := range cases {
		c := fakeBridge(t, func(req map[string]any, w net.Conn) {
			reply(w, map[string]any{"id": req["id"], "success": false, "error": tc.reply})
		})
		err := c.Do(context.Background(), "x", nil, nil)
		if err == nil {
			t.Fatalf("%q: no error", tc.reply)
		}
		if Unknown(err) != tc.unknown {
			t.Errorf("%q: Unknown = %v", tc.reply, Unknown(err))
		}
		if errors.Is(err, ErrUnsupported) != tc.unsupported {
			t.Errorf("%q: unsupported = %v", tc.reply, errors.Is(err, ErrUnsupported))
		}
	}
}

func TestDeadBridgeIsUnknownOutcome(t *testing.T) {
	c := Client{Socket: "/tmp/tmux-chrome-test-missing.sock", Timeout: time.Second}
	err := c.Do(context.Background(), "close_tabs", nil, nil)
	if err == nil || !Unknown(err) {
		t.Fatalf("err = %v, want an unknown-outcome error", err)
	}
	if c.Alive() {
		t.Fatal("Alive on a missing socket")
	}
}

func TestSubscribeSkipsPingsAndEndsOnClose(t *testing.T) {
	c := fakeBridge(t, func(req map[string]any, w net.Conn) {
		reply(w, map[string]any{"event": "subscribed"})
		reply(w, map[string]any{"event": "ping"})
		reply(w, map[string]any{"event": "changed", "seq": 3, "epoch": "e"})
	})
	sub, err := c.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ev, err := sub.Next()
	if err != nil || ev.Seq != 3 || ev.Epoch != "e" {
		t.Fatalf("Next = %+v, %v", ev, err)
	}
	if _, err := sub.Next(); err == nil {
		t.Fatal("Next after the bridge hung up returned no error")
	}
}

func TestSubscribeOnOldBridgeIsUnsupported(t *testing.T) {
	c := fakeBridge(t, func(req map[string]any, w net.Conn) {
		reply(w, map[string]any{"id": req["id"], "success": false, "error": "Unknown message type: subscribe"})
	})
	if _, err := c.Subscribe(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestReviveSharesTheLock(t *testing.T) {
	alive := fakeBridge(t, func(map[string]any, net.Conn) {})
	dir, err := os.MkdirTemp("/tmp", "tcl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	lock := filepath.Join(dir, "lock")

	opened := 0
	r := Reviver{Client: alive, Lock: lock, ExtensionID: "abc", Open: func(_ context.Context, url string) error {
		opened++
		if url != "chrome-extension://abc/reload.html" {
			t.Errorf("url = %s", url)
		}
		return nil
	}}
	if err := r.Revive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("opened = %d", opened)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}

	// Held by someone else: wait for the bridge, do not open a second reload.
	if err := os.Mkdir(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.Revive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("revived while another revive held the lock (opened = %d)", opened)
	}
}
